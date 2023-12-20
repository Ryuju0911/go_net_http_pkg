// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// HTTP client implementation. See RFC 7230 through 7235.
//
// This is the low-level Transport implementation of RoundTripper.
// The high-level interface is in client.go.

package http

import (
	"bufio"
	"container/list"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"sync"
	"time"
)

// DefaultTransport is the default implementation of Transport and is
// used by DefaultClient. It establishes network connections as needed
// and caches them for reuse by subsequent calls. It uses HTTP proxies
// as directed by the environment variables HTTP_PROXY, HTTPS_PROXY
// and NO_PROXY (or the lowercase versions thereof).
var DefaultTransport RoundTripper = &Transport{
	// Proxy: ProxyFromEnvironment,
	// DialContext: defaultTransportDialContext(&net.Dialer{
	// 	Timeout:   30 * time.Second,
	// 	KeepAlive: 30 * time.Second,
	// }),
	// ForceAttemptHTTP2:     true,
	MaxIdleConns:    100,
	IdleConnTimeout: 90 * time.Second,
	// TLSHandshakeTimeout:   10 * time.Second,
	// ExpectContinueTimeout: 1 * time.Second,
}

// DefaultMaxIdleConnsPerHost is the default value of Transport's
// MaxIdleConnsPerHost.
const DefaultMaxIdleConnsPerHost = 2

// Transport is an implementation of RoundTripper that supports HTTP,
// HTTPS, and HTTP proxies (for either HTTP or HTTPS with CONNECT).
//
// By default, Transport caches connections for future re-use.
// This may leave many open connections when accessing many hosts.
// This behavior can be managed using Transport's CloseIdleConnections method
// and the MaxIdleConnsPerHost and DisableKeepAlives fields.
//
// Transports should be reused instead of created as needed.
// Transports are safe for concurrent use by multiple goroutines.
//
// A Transport is a low-level primitive for making HTTP and HTTPS requests.
// For high-level functionality, such as cookies and redirects, see Client.
//
// Transport uses HTTP/1.1 for HTTP URLs and either HTTP/1.1 or HTTP/2
// for HTTPS URLs, depending on whether the server supports HTTP/2,
// and how the Transport is configured. The DefaultTransport supports HTTP/2.
// To explicitly enable HTTP/2 on a transport, use golang.org/x/net/http2
// and call ConfigureTransport. See the package docs for more about HTTP/2.
//
// Responses with status codes in the 1xx range are either handled
// automatically (100 expect-continue) or ignored. The one
// exception is HTTP status code 101 (Switching Protocols), which is
// considered a terminal status and returned by RoundTrip. To see the
// ignored 1xx responses, use the httptrace trace package's
// ClientTrace.Got1xxResponse.
//
// Transport only retries a request upon encountering a network error
// if the connection has been already been used successfully and if the
// request is idempotent and either has no body or has its Request.GetBody
// defined. HTTP requests are considered idempotent if they have HTTP methods
// GET, HEAD, OPTIONS, or TRACE; or if their Header map contains an
// "Idempotency-Key" or "X-Idempotency-Key" entry. If the idempotency key
// value is a zero-length slice, the request is treated as idempotent but the
// header is not sent on the wire.
type Transport struct {
	idleMu       sync.Mutex
	closeIdle    bool                                // user has requested to close all idle conns
	idleConn     map[connectMethodKey][]*persistConn // most recently used at end
	idleConnWait map[connectMethodKey]wantConnQueue  // waiting getConns
	idleLRU      connLRU

	reqMu       sync.Mutex
	reqCanceler map[cancelKey]func(error)

	// altMu    sync.Mutex   // guards changing altProto only
	// altProto atomic.Value // of nil or map[string]RoundTripper, key is URI scheme

	connsPerHostMu   sync.Mutex
	connsPerHost     map[connectMethodKey]int
	connsPerHostWait map[connectMethodKey]wantConnQueue // waiting getConns

	// // Proxy specifies a function to return a proxy for a given
	// // Request. If the function returns a non-nil error, the
	// // request is aborted with the provided error.
	// //
	// // The proxy type is determined by the URL scheme. "http",
	// // "https", and "socks5" are supported. If the scheme is empty,
	// // "http" is assumed.
	// //
	// // If the proxy URL contains a userinfo subcomponent,
	// // the proxy request will pass the username and password
	// // in a Proxy-Authorization header.
	// //
	// // If Proxy is nil or returns a nil *URL, no proxy is used.
	// Proxy func(*Request) (*url.URL, error)

	// // OnProxyConnectResponse is called when the Transport gets an HTTP response from
	// // a proxy for a CONNECT request. It's called before the check for a 200 OK response.
	// // If it returns an error, the request fails with that error.
	// OnProxyConnectResponse func(ctx context.Context, proxyURL *url.URL, connectReq *Request, connectRes *Response) error

	// DialContext specifies the dial function for creating unencrypted TCP connections.
	// If DialContext is nil (and the deprecated Dial below is also nil),
	// then the transport dials using package net.
	//
	// DialContext runs concurrently with calls to RoundTrip.
	// A RoundTrip call that initiates a dial may end up using
	// a connection dialed previously when the earlier connection
	// becomes idle before the later DialContext completes.
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)

	// Dial specifies the dial function for creating unencrypted TCP connections.
	//
	// Dial runs concurrently with calls to RoundTrip.
	// A RoundTrip call that initiates a dial may end up using
	// a connection dialed previously when the earlier connection
	// becomes idle before the later Dial completes.
	//
	// Deprecated: Use DialContext instead, which allows the transport
	// to cancel dials as soon as they are no longer needed.
	// If both are set, DialContext takes priority.
	Dial func(network, addr string) (net.Conn, error)

	// // DialTLSContext specifies an optional dial function for creating
	// // TLS connections for non-proxied HTTPS requests.
	// //
	// // If DialTLSContext is nil (and the deprecated DialTLS below is also nil),
	// // DialContext and TLSClientConfig are used.
	// //
	// // If DialTLSContext is set, the Dial and DialContext hooks are not used for HTTPS
	// // requests and the TLSClientConfig and TLSHandshakeTimeout
	// // are ignored. The returned net.Conn is assumed to already be
	// // past the TLS handshake.
	// DialTLSContext func(ctx context.Context, network, addr string) (net.Conn, error)

	// // DialTLS specifies an optional dial function for creating
	// // TLS connections for non-proxied HTTPS requests.
	// //
	// // Deprecated: Use DialTLSContext instead, which allows the transport
	// // to cancel dials as soon as they are no longer needed.
	// // If both are set, DialTLSContext takes priority.
	// DialTLS func(network, addr string) (net.Conn, error)

	// // TLSClientConfig specifies the TLS configuration to use with
	// // tls.Client.
	// // If nil, the default configuration is used.
	// // If non-nil, HTTP/2 support may not be enabled by default.
	// TLSClientConfig *tls.Config

	// // TLSHandshakeTimeout specifies the maximum amount of time to
	// // wait for a TLS handshake. Zero means no timeout.
	// TLSHandshakeTimeout time.Duration

	// DisableKeepAlives, if true, disables HTTP keep-alives and
	// will only use the connection to the server for a single
	// HTTP request.
	//
	// This is unrelated to the similarly named TCP keep-alives.
	DisableKeepAlives bool

	// // DisableCompression, if true, prevents the Transport from
	// // requesting compression with an "Accept-Encoding: gzip"
	// // request header when the Request contains no existing
	// // Accept-Encoding value. If the Transport requests gzip on
	// // its own and gets a gzipped response, it's transparently
	// // decoded in the Response.Body. However, if the user
	// // explicitly requested gzip it is not automatically
	// // uncompressed.
	// DisableCompression bool

	// MaxIdleConns controls the maximum number of idle (keep-alive)
	// connections across all hosts. Zero means no limit.
	MaxIdleConns int

	// MaxIdleConnsPerHost, if non-zero, controls the maximum idle
	// (keep-alive) connections to keep per-host. If zero,
	// DefaultMaxIdleConnsPerHost is used.
	MaxIdleConnsPerHost int

	// MaxConnsPerHost optionally limits the total number of
	// connections per host, including connections in the dialing,
	// active, and idle states. On limit violation, dials will block.
	//
	// Zero means no limit.
	MaxConnsPerHost int

	// IdleConnTimeout is the maximum amount of time an idle
	// (keep-alive) connection will remain idle before closing
	// itself.
	// Zero means no limit.
	IdleConnTimeout time.Duration

	// // ResponseHeaderTimeout, if non-zero, specifies the amount of
	// // time to wait for a server's response headers after fully
	// // writing the request (including its body, if any). This
	// // time does not include the time to read the response body.
	// ResponseHeaderTimeout time.Duration

	// // ExpectContinueTimeout, if non-zero, specifies the amount of
	// // time to wait for a server's first response headers after fully
	// // writing the request headers if the request has an
	// // "Expect: 100-continue" header. Zero means no timeout and
	// // causes the body to be sent immediately, without
	// // waiting for the server to approve.
	// // This time does not include the time to send the request header.
	// ExpectContinueTimeout time.Duration

	// // TLSNextProto specifies how the Transport switches to an
	// // alternate protocol (such as HTTP/2) after a TLS ALPN
	// // protocol negotiation. If Transport dials a TLS connection
	// // with a non-empty protocol name and TLSNextProto contains a
	// // map entry for that key (such as "h2"), then the func is
	// // called with the request's authority (such as "example.com"
	// // or "example.com:1234") and the TLS connection. The function
	// // must return a RoundTripper that then handles the request.
	// // If TLSNextProto is not nil, HTTP/2 support is not enabled
	// // automatically.
	// TLSNextProto map[string]func(authority string, c *tls.Conn) RoundTripper

	// // ProxyConnectHeader optionally specifies headers to send to
	// // proxies during CONNECT requests.
	// // To set the header dynamically, see GetProxyConnectHeader.
	// ProxyConnectHeader Header

	// // GetProxyConnectHeader optionally specifies a func to return
	// // headers to send to proxyURL during a CONNECT request to the
	// // ip:port target.
	// // If it returns an error, the Transport's RoundTrip fails with
	// // that error. It can return (nil, nil) to not add headers.
	// // If GetProxyConnectHeader is non-nil, ProxyConnectHeader is
	// // ignored.
	// GetProxyConnectHeader func(ctx context.Context, proxyURL *url.URL, target string) (Header, error)

	// MaxResponseHeaderBytes specifies a limit on how many
	// response bytes are allowed in the server's response
	// header.
	//
	// Zero means to use a default limit.
	MaxResponseHeaderBytes int64

	// WriteBufferSize specifies the size of the write buffer used
	// when writing to the transport.
	// If zero, a default (currently 4KB) is used.
	WriteBufferSize int

	// ReadBufferSize specifies the size of the read buffer used
	// when reading from the transport.
	// If zero, a default (currently 4KB) is used.
	ReadBufferSize int

	// // nextProtoOnce guards initialization of TLSNextProto and
	// // h2transport (via onceSetNextProtoDefaults)
	// nextProtoOnce sync.Once
	// h2transport        h2Transport // non-nil if http2 wired up
	// tlsNextProtoWasNil bool        // whether TLSNextProto was nil when the Once fired

	// // ForceAttemptHTTP2 controls whether HTTP/2 is enabled when a non-zero
	// // Dial, DialTLS, or DialContext func or TLSClientConfig is provided.
	// // By default, use of any those fields conservatively disables HTTP/2.
	// // To use a custom dialer or TLS config and still attempt HTTP/2
	// // upgrades, set this to true.
	// ForceAttemptHTTP2 bool
}

// A cancelKey is the key of the reqCanceler map.
// We wrap the *Request in this type since we want to use the original request,
// not any transient one created by roundTrip.
type cancelKey struct {
	req *Request
}

func (t *Transport) writeBufferSize() int {
	if t.WriteBufferSize > 0 {
		return t.WriteBufferSize
	}
	return 4 << 10
}

func (t *Transport) readBufferSize() int {
	if t.ReadBufferSize > 0 {
		return t.ReadBufferSize
	}
	return 4 << 10
}

// transportRequest is a wrapper around a *Request that adds
// optional extra headers to write and stores any error to return
// from roundTrip.
type transportRequest struct {
	*Request // original request, not to be mutated
	// 	extra     Header                 // extra headers to write, or nil
	// 	trace     *httptrace.ClientTrace // optional
	cancelKey cancelKey

	// mu  sync.Mutex // guards err
	// err error      // first setError value for mapRoundTripError to consider
}

// roundTrip implements a RoundTripper over HTTP.
func (t *Transport) roundTrip(req *Request) (*Response, error) {
	// t.nextProtoOnce.Do(t.onceSetNextProtoDefaults)
	ctx := req.Context()
	// trace := httptrace.ContextClientTrace(ctx)

	if req.URL == nil {
		req.closeBody()
		return nil, errors.New("http: nil Request.URL")
	}
	if req.Header == nil {
		req.closeBody()
		return nil, errors.New("http: nil Request.Header")
	}
	scheme := req.URL.Scheme
	isHTTP := scheme == "http" || scheme == "https"
	// if isHTTP {
	// 	for k, vv := range req.Header {
	// 		if !httpguts.ValidHeaderFieldName(k) {
	// 			req.closeBody()
	// 			return nil, fmt.Errorf("net/http: invalid header field name %q", k)
	// 		}
	// 		for _, v := range vv {
	// 			if !httpguts.ValidHeaderFieldValue(v) {
	// 				req.closeBody()
	// 				// Don't include the value in the error, because it may be sensitive.
	// 				return nil, fmt.Errorf("net/http: invalid header field value for %q", k)
	// 			}
	// 		}
	// 	}
	// }

	origReq := req
	cancelKey := cancelKey{origReq}
	req = setupRewindBody(req)

	// if altRT := t.alternateRoundTripper(req); altRT != nil {
	// 	if resp, err := altRT.RoundTrip(req); err != ErrSkipAltProtocol {
	// 		return resp, err
	// 	}
	// 	var err error
	// 	req, err = rewindBody(req)
	// 	if err != nil {
	// 		return nil, err
	// 	}
	// }
	if !isHTTP {
		req.closeBody()
		return nil, badStringError("unsupported protocol scheme", scheme)
	}
	if req.Method != "" && !validMethod(req.Method) {
		req.closeBody()
		return nil, fmt.Errorf("net/http: invalid method %q", req.Method)
	}
	if req.URL.Host == "" {
		req.closeBody()
		return nil, errors.New("http: no Host in request URL")
	}

	for {
		select {
		case <-ctx.Done():
			req.closeBody()
			return nil, ctx.Err()
		default:
		}
		// treq gets modified by roundTrip, so we need to recreate for each retry.
		// treq := &transportRequest{Request: req, trace: trace, cancelKey: cancelKey}
		treq := &transportRequest{Request: req, cancelKey: cancelKey}
		cm, err := t.connectMethodForRequst(treq)
		if err != nil {
			req.closeBody()
			return nil, err
		}

		pconn, err := t.getConn(treq, cm)
		if err != nil {
			t.setReqCanceler(cancelKey, nil)
			req.closeBody()
			return nil, err
		}

		var resp *Response
		// TODO: Support HTTP/2 and handle it here.
		resp, err = pconn.roundTrip(treq)
		if err == nil {
			resp.Request = origReq
			return resp, nil
		}

		// Failed. Clean up and determine whether to retry.
	}
}

type readTrackingBody struct {
	io.ReadCloser
	didRead  bool
	didClose bool
}

func (r *readTrackingBody) Read(data []byte) (int, error) {
	r.didRead = true
	return r.ReadCloser.Read(data)
}

func (r *readTrackingBody) Close() error {
	r.didClose = true
	return r.ReadCloser.Close()
}

// setupRewindBody returns a new request with a custom body wrapper
// that can report whether the body needs rewinding.
// This lets rewindBody avoid an error result when the request
// does not have GetBody but the body hasn't been read at all yet.
func setupRewindBody(req *Request) *Request {
	if req.Body == nil || req.Body == NoBody {
		return req
	}
	newReq := *req
	newReq.Body = &readTrackingBody{ReadCloser: req.Body}
	return &newReq
}

func (t *Transport) connectMethodForRequst(treq *transportRequest) (cm connectMethod, err error) {
	cm.targetScheme = treq.URL.Scheme
	cm.targetAddr = canonicalAddr(treq.URL)
	// if t.Proxy != nil {
	// 	cm.proxyURL, err = t.Proxy(treq.Request)
	// }
	// cm.onlyH1 = treq.requiresHTTP1()
	cm.onlyH1 = true
	return cm, err
}

// error values for debugging and testing, not seen by users.
var (
	errKeepAlivesDisabled = errors.New("http: putIdleConn: keep alives disabled")
	errConnBroken         = errors.New("http: putIdleConn: connection is in bad state")
	errCloseIdle          = errors.New("http: putIdleConn: CloseIdleConnections was called")
	errTooManyIdle        = errors.New("http: putIdleConn: too many idle connections")
	errTooManyIdleHost    = errors.New("http: putIdleConn: too many idle connections for host")
	// errCloseIdleConns     = errors.New("http: CloseIdleConnections called")
	// errReadLoopExiting    = errors.New("http: persistConn.readLoop exiting")
	errIdleConnTimeout = errors.New("http: idle connection timeout")

	// // errServerClosedIdle is not seen by users for idempotent requests, but may be
	// // seen by a user if the server shuts down an idle connection and sends its FIN
	// // in flight with already-written POST body bytes from the client.
	// // See https://github.com/golang/go/issues/19943#issuecomment-355607646
	// errServerClosedIdle = errors.New("http: server closed idle connection")
)

func (t *Transport) putOrCloseIdleConn(pconn *persistConn) {
	if err := t.tryPutIdleConn(pconn); err != nil {
		pconn.close(err)
	}
}

func (t *Transport) maxIdleConnsPerHost() int {
	if v := t.MaxIdleConnsPerHost; v != 0 {
		return v
	}
	return DefaultMaxIdleConnsPerHost
}

// tryPutIdleConn adds pconn to the list of idle persistent connections awaiting
// a new request.
// If pconn is no longer needed or not in a good state, tryPutIdleConn returns
// an error explaining why it wasn't registered.
// tryPutIdleConn does not close pconn. Use putOrCloseIdleConn instead for that.
func (t *Transport) tryPutIdleConn(pconn *persistConn) error {
	if t.DisableKeepAlives || t.MaxIdleConnsPerHost < 0 {
		return errKeepAlivesDisabled
	}
	if pconn.isBroken() {
		return errConnBroken
	}
	pconn.markReused()

	t.idleMu.Lock()
	defer t.idleMu.Unlock()

	// HTTP/2 (pconn.alt != nil) connections do not come out of the idle list,
	// because multiple goroutines can use them simultaneously.
	// If this is an HTTP/2 connection being “returned,” we're done.
	if pconn.alt != nil && t.idleLRU.m[pconn] != nil {
		return nil
	}

	// Deliver pconn to goroutine waiting for idle connection, if any.
	// (They may be actively dialing, but this conn is ready first.
	// Chrome calls this socket late binding.
	// See https://www.chromium.org/developers/design-documents/network-stack#TOC-Connection-Management.)
	key := pconn.cacheKey
	if q, ok := t.idleConnWait[key]; ok {
		done := false
		// HTTP/1.
		// Loop over the waiting list until we find a w that isn't done already, and hand it pconn.
		for q.len() > 0 {
			w := q.popFront()
			if w.tryDeliver(pconn, nil) {
				done = true
				break
			}
		}
		// TODO: Handle HTTP/2

		if q.len() == 0 {
			delete(t.idleConnWait, key)
		} else {
			t.idleConnWait[key] = q
		}
		if done {
			return nil
		}
	}

	if t.closeIdle {
		return errCloseIdle
	}
	if t.idleConn == nil {
		t.idleConn = make(map[connectMethodKey][]*persistConn)
	}
	idles := t.idleConn[key]
	if len(idles) >= t.maxIdleConnsPerHost() {
		return errTooManyIdleHost
	}
	for _, exist := range idles {
		if exist == pconn {
			log.Fatalf("dup idle pconn %p in freelist", pconn)
		}
	}
	t.idleConn[key] = append(idles, pconn)
	t.idleLRU.add(pconn)
	if t.MaxIdleConns != 0 && t.idleLRU.len() > t.MaxIdleConns {
		oldest := t.idleLRU.removeOldest()
		oldest.close(errTooManyIdle)
		t.removeIdleConnLocked(oldest)
	}

	// Set idle timer, but only for HTTP/1 (pconn.alt == nil).
	// The HTTP/2 implementation manages the idle timer itself
	// (see idleConnTimeout in h2_bundle.go).
	if t.IdleConnTimeout > 0 && pconn.alt == nil {
		if pconn.idleTimer != nil {
			pconn.idleTimer.Reset(t.IdleConnTimeout)
		} else {
			pconn.idleTimer = time.AfterFunc(t.IdleConnTimeout, pconn.closeConnIfStillIdle)
		}
	}
	pconn.idleAt = time.Now()
	return nil
}

// queueForIdleConn queues w to receive the next idle connection for w.cm.
// As an optimization hint to the caller, queueForIdleConn reports whether
// it successfully delivered an already-idle connection.
func (t *Transport) queueForIdleConn(w *wantConn) (delivered bool) {
	if t.DisableKeepAlives {
		return false
	}

	t.idleMu.Lock()
	defer t.idleMu.Unlock()

	// Stop closing connections that become idle - we might want one.
	// (That is, undo the effect of t.CloseIdleConnections.)
	t.closeIdle = false

	if w == nil {
		// Happens in test hook.
		return false
	}

	// If IdleConnTimeout is set, calculate the oldest
	// persistConn.idleAt time we're willing to use a cached idle
	// conn.
	var oldTime time.Time
	if t.IdleConnTimeout > 0 {
		oldTime = time.Now().Add(-t.IdleConnTimeout)
	}

	// Look for most recently-used idle connection.
	if list, ok := t.idleConn[w.key]; ok {
		stop := false
		delivered := false
		for len(list) > 0 && !stop {
			pconn := list[len(list)-1]

			// See whether this connection has been idle too long, considering
			// only the wall time (the Round(0)), in case this is a laptop or VM
			// coming out of suspend with previously cached idle connections.
			tooOld := !oldTime.IsZero() && pconn.idleAt.Round(0).Before(oldTime)
			if tooOld {
				// Async cleanup. Launch in its own goroutine (as if a
				// time.AfterFunc called it); it acquires idleMu, which we're
				// holding, and does a synchronous net.Conn.Close.
				go pconn.closeConnIfStillIdle()
			}
			if pconn.isBroken() || tooOld {
				// If either persistConn.readLoop has marked the connection
				// broken, but Transport.removeIdleConn has not yet removed it
				// from the idle list, or if this persistConn is too old (it was
				// idle too long), then ignore it and look for another. In both
				// cases it's already in the process of being closed.
				list = list[:len(list)-1]
				continue
			}
			delivered = w.tryDeliver(pconn, nil)
			if delivered {
				if pconn.alt != nil {
					// HTTP/2: multiple clients can share pconn.
					// Leave it in the list.
				} else {
					// HTTP/1: only one client can use pconn.
					// Remove it from the list.
					t.idleLRU.remove(pconn)
					list = list[:len(list)-1]
				}
			}
			stop = true
		}
		if len(list) > 0 {
			t.idleConn[w.key] = list
		} else {
			delete(t.idleConn, w.key)
		}
		if stop {
			return delivered
		}
	}

	// Register to receive next connection that becomes idle.
	if t.idleConnWait == nil {
		t.idleConnWait = make(map[connectMethodKey]wantConnQueue)
	}
	q := t.idleConnWait[w.key]
	q.cleanFront()
	q.pushBack(w)
	t.idleConnWait[w.key] = q
	return false
}

// t.idleMu must be held.
func (t *Transport) removeIdleConnLocked(pconn *persistConn) bool {
	if pconn.idleTimer != nil {
		pconn.idleTimer.Stop()
	}
	t.idleLRU.remove(pconn)
	key := pconn.cacheKey
	pconns := t.idleConn[key]
	var removed bool
	switch len(pconns) {
	case 0:
		// Nothing
	case 1:
		if pconns[0] == pconn {
			delete(t.idleConn, key)
			removed = true
		}
	default:
		for i, v := range pconns {
			if v != pconn {
				continue
			}
			// Slide down, keeping most recently-used
			// conns at the end.
			copy(pconns[i:], pconns[i+1:])
			t.idleConn[key] = pconns[:len(pconns)-1]
			removed = true
			break
		}
	}
	return removed
}

func (t *Transport) setReqCanceler(key cancelKey, fn func(error)) {
	t.reqMu.Lock()
	defer t.reqMu.Unlock()
	if t.reqCanceler == nil {
		t.reqCanceler = make(map[cancelKey]func(error))
	}
	if fn != nil {
		t.reqCanceler[key] = fn
	} else {
		delete(t.reqCanceler, key)
	}
}

var zeroDialer net.Dialer

func (t *Transport) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	if t.DialContext != nil {
		c, err := t.DialContext(ctx, network, addr)
		if c == nil && err == nil {
			err = errors.New("net/http: Transport.DialContext hook returned (nil, nil)")
		}
		return c, err
	}
	if t.Dial != nil {
		c, err := t.Dial(network, addr)
		if c == nil && err == nil {
			err = errors.New("net/http: Transport.Dial hook returned (nil, nil)")
		}
		return c, err
	}
	return zeroDialer.DialContext(ctx, network, addr)
}

// A wantConn records state about a wanted connection
// (that is, an active call to getConn).
// The conn may be gotten by dialing or by finding an idle connection,
// or a cancellation may make the conn no longer wanted.
// These three options are racing against each other and use
// wantConn to coordinate and agree about the winning outcome.
type wantConn struct {
	cm    connectMethod
	key   connectMethodKey // cm.key()
	ready chan struct{}    // closed when pc, err pair is delivered

	// // hooks for testing to know when dials are done
	// // beforeDial is called in the getConn goroutine when the dial is queued.
	// // afterDial is called when the dial is completed or canceled.
	// beforeDial func()
	// afterDial  func()

	mu  sync.Mutex      // protects ctx, pc, err, close(ready)
	ctx context.Context // context for dial, cleared after delivered or canceled
	pc  *persistConn
	err error
}

// waiting reports whether w is still waiting for an answer (connection or error).
func (w *wantConn) waiting() bool {
	select {
	case <-w.ready:
		return false
	default:
		return true
	}
}

// getCtxForDial returns context for dial or nil if connection wans delivered or canceled.
func (w *wantConn) getCtxForDial() context.Context {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ctx
}

// tryDeliver attempts to deliver pc, err to w and reports whether it succeeded.
func (w *wantConn) tryDeliver(pc *persistConn, err error) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.pc != nil || w.err != nil {
		return false
	}

	w.ctx = nil
	w.pc = pc
	w.err = err
	if w.pc == nil && w.err == nil {
		panic("net/http: internal error: misuse of tryDeliver")
	}
	close(w.ready)
	return true
}

// cancel marks w as no longer wanting a result (for example, due to cancellation).
// If a connection has been delivered already, cancel returns it with t.putOrCloseIdleConn.
func (w *wantConn) cancel(t *Transport, err error) {
	w.mu.Lock()
	if w.pc == nil && w.err == nil {
		close(w.ready) // catch misbehavior in future delivery
	}
	pc := w.pc
	w.ctx = nil
	w.pc = nil
	w.err = err
	w.mu.Unlock()

	if pc != nil {
		t.putOrCloseIdleConn(pc)
	}
}

// A wantConnQueue is a queue of wantConns.
type wantConnQueue struct {
	// This is a queue, not a deque.
	// It is split into two stages - head[headPos:] and tail.
	// popFront is trivial (headPos++) on the first stage, and
	// pushBack is trivial (append) on the second stage.
	// If the first stage is empty, popFront can swap the
	// first and second stages to remedy the situation.
	//
	// This two-stage split is analogous to the use of two lists
	// in Okasaki's purely functional queue but without the
	// overhead of reversing the list when swapping stages.
	head    []*wantConn
	headPos int
	tail    []*wantConn
}

// len returns the number of items in the queue.
func (q *wantConnQueue) len() int {
	return len(q.head) - q.headPos + len(q.tail)
}

// pushBack adds w to the back of the queue.
func (q *wantConnQueue) pushBack(w *wantConn) {
	q.tail = append(q.tail, w)
}

// popFront removes and returns the wantConn at the front of the queue.
func (q *wantConnQueue) popFront() *wantConn {
	if q.headPos >= len(q.head) {
		if len(q.tail) == 0 {
			return nil
		}
		// Pick up tail as new head, clear tail.
		q.head, q.headPos, q.tail = q.tail, 0, q.head[:0]
	}
	w := q.head[q.headPos]
	q.head[q.headPos] = nil
	q.headPos++
	return w
}

// peekFront returns the wantConn at the front of the queue without removing it.
func (q *wantConnQueue) peekFront() *wantConn {
	if q.headPos < len(q.head) {
		return q.head[q.headPos]
	}
	if len(q.tail) > 0 {
		return q.tail[0]
	}
	return nil
}

// cleanFront pops any wantConns that are no longer waiting from the head of the
// queue, reporting whether any were popped.
func (q *wantConnQueue) cleanFront() (cleaned bool) {
	for {
		w := q.peekFront()
		if w == nil || w.waiting() {
			return cleaned
		}
		q.popFront()
		cleaned = true
	}
}

// getConn dials and creates a new persistConn to the target as
// specified in the connectMethod. This includes doing a proxy CONNECT
// and/or setting up TLS.  If this doesn't return an error, the persistConn
// is ready to write requests to.
func (t *Transport) getConn(treq *transportRequest, cm connectMethod) (pc *persistConn, err error) {
	req := treq.Request
	// trace := treq.trace
	ctx := req.Context()
	// if trace != nil && trace.GetConn != nil {
	// 	trace.GetConn(cm.addr())
	// }

	w := &wantConn{
		cm:    cm,
		key:   cm.key(),
		ctx:   ctx,
		ready: make(chan struct{}, 1),
		// beforeDial: testHookPrePendingDial,
		// afterDial:  testHookPostPendingDial,
	}
	defer func() {
		if err != nil {
			w.cancel(t, err)
		}
	}()

	// Queue for idle connection.
	if delivered := t.queueForIdleConn(w); delivered {
		pc := w.pc
		// // Trace only for HTTP/1.
		// // HTTP/2 calls trace.GotConn itself.
		// if pc.alt == nil && trace != nil && trace.GotConn != nil {
		// 	trace.GotConn(pc.gotIdleConnTrace(pc.idleAt))
		// }
		// set request canceler to some non-nil function so we
		// can detect whether it was cleared between now and when
		// we enter roundTrip
		t.setReqCanceler(treq.cancelKey, func(error) {})
		return pc, nil
	}

	cancelc := make(chan error, 1)
	t.setReqCanceler(treq.cancelKey, func(err error) { cancelc <- err })

	// Queue for permission to dial.
	t.queueForDial(w)
	return nil, nil
}

// queueForDial queues w to wait for permission to begin dialing.
// Once w receives permission to dial, it will do so in a separate goroutine.
func (t *Transport) queueForDial(w *wantConn) {
	// w.beforeDial()
	if t.MaxConnsPerHost <= 0 {
		go t.dialConnFor(w)
		return
	}

	t.connsPerHostMu.Lock()
	defer t.connsPerHostMu.Unlock()

	if n := t.connsPerHost[w.key]; n < t.MaxConnsPerHost {
		if t.connsPerHost == nil {
			t.connsPerHost = make(map[connectMethodKey]int)
		}
		t.connsPerHost[w.key] = n + 1
		go t.dialConnFor(w)
		return
	}

	if t.connsPerHostWait == nil {
		t.connsPerHostWait = make(map[connectMethodKey]wantConnQueue)
	}
	q := t.connsPerHostWait[w.key]
	q.cleanFront()
	q.pushBack(w)
	t.connsPerHostWait[w.key] = q
}

// dialConnFor dials on behalf of w and delivers the result to w.
// dialConnFor has received permission to dial w.cm and is counted in t.connCount[w.cm.key()].
// If the dial is canceled or unsuccessful, dialConnFor decrements t.connCount[w.cm.key()].
func (t *Transport) dialConnFor(w *wantConn) {
	// defer w.afterDial()
	ctx := w.getCtxForDial()
	if ctx == nil {
		return
	}

	pc, err := t.dialConn(ctx, w.cm)
	delivered := w.tryDeliver(pc, err)
	if err == nil && (!delivered || pc.alt != nil) {
		// pconn was not passed to w,
		// or it is HTTP/2 and can be shared.
		// Add to the idle connection pool.
		t.putOrCloseIdleConn(pc)
	}
	if err != nil {
		t.decConnsPerHost(w.key)
	}
}

// decConnsPerHost decrements the per-host connection count for key,
// which may in turn give a different waiting goroutine permission to dial.
func (t *Transport) decConnsPerHost(key connectMethodKey) {
	if t.MaxConnsPerHost <= 0 {
		return
	}

	t.connsPerHostMu.Lock()
	defer t.connsPerHostMu.Unlock()
	n := t.connsPerHost[key]
	if n == 0 {
		// Shouldn't happen, but if it does, the counting is buggy and could
		// easily lead to a silent deadlock, so report the problem loudly.
		panic("net/http: internal error: connCount underflow")
	}

	// Can we hand this count to a goroutine still waiting to dial?
	// (Some goroutines on the wait list may have timed out or
	// gotten a connection another way. If they're all gone,
	// we don't want to kick off any spurious dial operations.)
	if q := t.connsPerHostWait[key]; q.len() > 0 {
		done := false
		for q.len() > 0 {
			w := q.popFront()
			if w.waiting() {
				go t.dialConnFor(w)
				done = true
				break
			}
		}
		if q.len() == 0 {
			delete(t.connsPerHostWait, key)
		} else {
			// q is a value (like a slice), so we have to store
			// the updated q back into the map.
			t.connsPerHostWait[key] = q
		}
		if done {
			return
		}
	}

	// Otherwise, decrement the recorded count.
	if n--; n == 0 {
		delete(t.connsPerHost, key)
	} else {
		t.connsPerHost[key] = n
	}
}

func (t *Transport) dialConn(ctx context.Context, cm connectMethod) (pconn *persistConn, err error) {
	pconn = &persistConn{
		t:        t,
		cacheKey: cm.key(),
		// reqch:         make(chan requestAndChan, 1),
		// writech:       make(chan writeRequest, 1),
		// closech:       make(chan struct{}),
		// writeErrCh:    make(chan error, 1),
		// writeLoopDone: make(chan struct{}),
	}
	wrapErr := func(err error) error {
		if cm.proxyURL != nil {
			// Return a typed error, per Issue 16997
			return &net.OpError{Op: "proxyconnect", Net: "tcp", Err: err}
		}
		return err
	}

	// TODO: Handle HTTPS.

	conn, err := t.dial(ctx, "tcp", cm.addr())
	if err != nil {
		return nil, wrapErr(err)
	}
	pconn.conn = conn
	// TODO: Handle HTTPS

	// Proxy setup.
	switch {
	case cm.proxyURL == nil:
		// Do nothing. Not using a proxy.
		// TODO: Use a proxy.
	}

	// if cm.proxyURL != nil && cm.targetScheme == "https" {
	// 	if err := pconn.addTLS(ctx, cm.tlsHost(), trace); err != nil {
	// 		return nil, err
	// 	}
	// }

	// if s := pconn.tlsState; s != nil && s.NegotiatedProtocolIsMutual && s.NegotiatedProtocol != "" {
	// 	if next, ok := t.TLSNextProto[s.NegotiatedProtocol]; ok {
	// 		alt := next(cm.targetAddr, pconn.conn.(*tls.Conn))
	// 		if e, ok := alt.(erringRoundTripper); ok {
	// 			// pconn.conn was closed by next (http2configureTransports.upgradeFn).
	// 			return nil, e.RoundTripErr()
	// 		}
	// 		return &persistConn{t: t, cacheKey: pconn.cacheKey, alt: alt}, nil
	// 	}
	// }

	pconn.br = bufio.NewReaderSize(pconn, t.readBufferSize())
	pconn.bw = bufio.NewWriterSize(persistConnWriter{pconn}, t.writeBufferSize())

	go pconn.readLoop()
	go pconn.writeLoop()
	return pconn, nil
}

// persistConnWriter is the io.Writer written to by pc.bw.
// It accumulates the number of bytes written to the underlying conn,
// so the retry logic can determine whether any bytes made it across
// the wire.
// This is exactly 1 pointer field wide so it can go into an interface
// without allocation.
type persistConnWriter struct {
	pc *persistConn
}

func (w persistConnWriter) Write(p []byte) (n int, err error) {
	n, err = w.pc.conn.Write(p)
	w.pc.nwrite += int64(n)
	return
}

// connectMethod is the map key (in its String form) for keeping persistent
// TCP connections alive for subsequent HTTP requests.
//
// A connect method may be of the following types:
//
//	connectMethod.key().String()      Description
//	------------------------------    -------------------------
//	|http|foo.com                     http directly to server, no proxy
//	|https|foo.com                    https directly to server, no proxy
//	|https,h1|foo.com                 https directly to server w/o HTTP/2, no proxy
//	http://proxy.com|https|foo.com    http to proxy, then CONNECT to foo.com
//	http://proxy.com|http             http to proxy, http to anywhere after that
//	socks5://proxy.com|http|foo.com   socks5 to proxy, then http to foo.com
//	socks5://proxy.com|https|foo.com  socks5 to proxy, then https to foo.com
//	https://proxy.com|https|foo.com   https to proxy, then CONNECT to foo.com
//	https://proxy.com|http            https to proxy, http to anywhere after that
type connectMethod struct {
	_            incomparable
	proxyURL     *url.URL // nil for no proxy, else full proxy URL
	targetScheme string   // "http" or "https"
	// If proxyURL specifies an http or https proxy, and targetScheme is http (not https),
	// then targetAddr is not included in the connect method key, because the socket can
	// be reused for different targetAddr values.
	targetAddr string
	onlyH1     bool // whether to disable HTTP/2 and force HTTP/1
}

func (cm *connectMethod) key() connectMethodKey {
	proxyStr := ""
	targetAddr := cm.targetAddr
	if cm.proxyURL != nil {
		proxyStr = cm.proxyURL.String()
		if (cm.proxyURL.Scheme == "http" || cm.proxyURL.Scheme == "https") && cm.targetScheme == "http" {
			targetAddr = ""
		}
	}
	return connectMethodKey{
		proxy:  proxyStr,
		scheme: cm.targetScheme,
		addr:   targetAddr,
		onlyH1: cm.onlyH1,
	}
}

// addr returns the first hop "host:port" to which we need to TCP connect.
func (cm *connectMethod) addr() string {
	if cm.proxyURL != nil {
		return canonicalAddr(cm.proxyURL)
	}
	return cm.targetAddr
}

// connectMethodKey is the map key version of connectMethod, with a
// stringified proxy URL (or the empty string) instead of a pointer to
// a URL.
type connectMethodKey struct {
	proxy, scheme, addr string
	onlyH1              bool
}

// persistConn wraps a connection, usually a persistent one
// (but may be used for non-keep-alive requests as well)
type persistConn struct {
	// alt optionally specifies the TLS NextProto RoundTripper.
	// This is used for HTTP/2 today and future protocols later.
	// If it's non-nil, the rest of the fields are unused.
	alt RoundTripper

	t        *Transport
	cacheKey connectMethodKey
	conn     net.Conn
	// tlsState  *tls.ConnectionState
	br     *bufio.Reader // from conn
	bw     *bufio.Writer // to conn
	nwrite int64         // bytes written
	// reqch     chan requestAndChan // written by roundTrip; read by readLoop
	// writech   chan writeRequest   // written by roundTrip; read by writeLoop
	// closech   chan struct{}       // closed when conn closed
	// isProxy   bool
	sawEOF    bool  // whether we've seen EOF from conn; owned by readLoop
	readLimit int64 // bytes allowed to be read; owned by readLoop
	// // writeErrCh passes the request write error (usually nil)
	// // from the writeLoop goroutine to the readLoop which passes
	// // it off to the res.Body reader, which then uses it to decide
	// // whether or not a connection can be reused. Issue 7569.
	// writeErrCh chan error

	// writeLoopDone chan struct{} // closed when write loop ends

	// Both guarded by Transport.idleMu:
	idleAt    time.Time   // time it last become idle
	idleTimer *time.Timer // holding an AfterFunc to close it

	mu sync.Mutex // guards following fields
	// numExpectedResponses int
	closed error // set non-nil when conn is closed, before closech is closed
	// canceledErr          error // set non-nil if conn is canceled
	broken bool // an error has happened on this connection; marked broken so it's not reused.
	reused bool // whether conn has had successful request/response and is being reused.
	// mutateHeaderFunc is an optional func to modify extra
	// headers on each outbound request before it's written. (the
	// original Request given to RoundTrip is not modified)
	mutateHeaderFunc func(Header)
}

func (pc *persistConn) maxHeaderResponseSize() int64 {
	if v := pc.t.MaxResponseHeaderBytes; v != 0 {
		return v
	}
	return 10 << 20 // conservative default; same as http2
}

func (pc *persistConn) Read(p []byte) (n int, err error) {
	if pc.readLimit <= 0 {
		return 0, fmt.Errorf("read limit of %d bytes exhausted", pc.maxHeaderResponseSize())
	}
	if int64(len(p)) > pc.readLimit {
		p = p[:pc.readLimit]
	}
	n, err = pc.conn.Read(p)
	if err == io.EOF {
		pc.sawEOF = true
	}
	pc.readLimit -= int64(n)
	return
}

// isBroken reports whether this connection is in a known broken state.
func (pc *persistConn) isBroken() bool {
	pc.mu.Lock()
	b := pc.closed != nil
	pc.mu.Unlock()
	return b
}

// closeConnIfStillIdle closes the connection if it's still sitting idle.
// This is what's called by the persistConn's idleTimer, and is run in its
// own goroutine.
func (pc *persistConn) closeConnIfStillIdle() {
	t := pc.t
	t.idleMu.Lock()
	defer t.idleMu.Unlock()
	if _, ok := t.idleLRU.m[pc]; !ok {
		// Not idle.
		return
	}
	t.removeIdleConnLocked(pc)
	pc.close(errIdleConnTimeout)
}

func (pc *persistConn) readLoop() {
	// TODO: Implement logic.
}

func (pc *persistConn) writeLoop() {
	// TODO: Implement logic.
}

func (pc *persistConn) markReused() {
	pc.mu.Lock()
	pc.reused = true
	pc.mu.Unlock()
}

// close closes the underlying TCP connection and closes
// the pc.closech channel.
//
// The provided err is only for testing and debugging; in normal
// circumstances it should never be seen by users.
func (pc *persistConn) close(err error) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.closeLocked(err)
}

func (pc *persistConn) closeLocked(err error) {
	if err == nil {
		panic("nil error")
	}
	pc.broken = true
	if pc.closed == nil {
		pc.closed = err
		pc.t.decConnsPerHost(pc.cacheKey)
		// // Close HTTP/1 (pc.alt == nil) connection.
		// // HTTP/2 closes its connection itself.
		// if pc.alt == nil {
		// 	if err != errCallerOwnsConn {
		// 		pc.conn.Close()
		// 	}
		// 	close(pc.closech)
		// }
	}
	pc.mutateHeaderFunc = nil
}

var portMap = map[string]string{
	"http":   "80",
	"https":  "443",
	"socks5": "1080",
}

func idnaASCIIFromURL(url *url.URL) string {
	addr := url.Hostname()
	if v, err := idnaASCII(addr); err == nil {
		addr = v
	}
	return addr
}

// canonicalAddr returns url.Host but always with a ":port" suffix.
func canonicalAddr(url *url.URL) string {
	port := url.Port()
	if port == "" {
		port = portMap[url.Scheme]
	}
	return net.JoinHostPort(idnaASCIIFromURL(url), port)
}

func (pc *persistConn) roundTrip(req *transportRequest) (resp *Response, err error) {
	// TODO: Implement logic.
	return nil, nil
}

type connLRU struct {
	ll *list.List // list.Element.Value type is of *persistConn.
	m  map[*persistConn]*list.Element
}

// add adds pc to the head of the linked list.
func (cl *connLRU) add(pc *persistConn) {
	if cl.ll == nil {
		cl.ll = list.New()
		cl.m = make(map[*persistConn]*list.Element)
	}
	ele := cl.ll.PushFront(pc)
	if _, ok := cl.m[pc]; ok {
		panic("persistConn was already in LRU")
	}
	cl.m[pc] = ele
}

func (cl *connLRU) removeOldest() *persistConn {
	ele := cl.ll.Back()
	pc := ele.Value.(*persistConn)
	cl.ll.Remove(ele)
	delete(cl.m, pc)
	return pc
}

// remove removes pc from cl.
func (cl *connLRU) remove(pc *persistConn) {
	if ele, ok := cl.m[pc]; ok {
		cl.ll.Remove(ele)
		delete(cl.m, pc)
	}
}

// len returns the number of items in the cache.
func (cl *connLRU) len() int {
	return len(cl.m)
}
