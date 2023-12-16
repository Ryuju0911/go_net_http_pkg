// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// HTTP client implementation. See RFC 7230 through 7235.
//
// This is the low-level Transport implementation of RoundTripper.
// The high-level interface is in client.go.

package http

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
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
	// MaxIdleConns:          100,
	// IdleConnTimeout:       90 * time.Second,
	// TLSHandshakeTimeout:   10 * time.Second,
	// ExpectContinueTimeout: 1 * time.Second,
}

type Transport struct {
	// idleMu       sync.Mutex
	// closeIdle    bool                                // user has requested to close all idle conns
	// idleConn     map[connectMethodKey][]*persistConn // most recently used at end
	// idleConnWait map[connectMethodKey]wantConnQueue  // waiting getConns
	// idleLRU      connLRU

	// reqMu       sync.Mutex
	// reqCanceler map[cancelKey]func(error)

	// altMu    sync.Mutex   // guards changing altProto only
	// altProto atomic.Value // of nil or map[string]RoundTripper, key is URI scheme

	// connsPerHostMu   sync.Mutex
	// connsPerHost     map[connectMethodKey]int
	// connsPerHostWait map[connectMethodKey]wantConnQueue // waiting getConns

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

	// // DialContext specifies the dial function for creating unencrypted TCP connections.
	// // If DialContext is nil (and the deprecated Dial below is also nil),
	// // then the transport dials using package net.
	// //
	// // DialContext runs concurrently with calls to RoundTrip.
	// // A RoundTrip call that initiates a dial may end up using
	// // a connection dialed previously when the earlier connection
	// // becomes idle before the later DialContext completes.
	// DialContext func(ctx context.Context, network, addr string) (net.Conn, error)

	// // Dial specifies the dial function for creating unencrypted TCP connections.
	// //
	// // Dial runs concurrently with calls to RoundTrip.
	// // A RoundTrip call that initiates a dial may end up using
	// // a connection dialed previously when the earlier connection
	// // becomes idle before the later Dial completes.
	// //
	// // Deprecated: Use DialContext instead, which allows the transport
	// // to cancel dials as soon as they are no longer needed.
	// // If both are set, DialContext takes priority.
	// Dial func(network, addr string) (net.Conn, error)

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

	// // DisableKeepAlives, if true, disables HTTP keep-alives and
	// // will only use the connection to the server for a single
	// // HTTP request.
	// //
	// // This is unrelated to the similarly named TCP keep-alives.
	// DisableKeepAlives bool

	// // DisableCompression, if true, prevents the Transport from
	// // requesting compression with an "Accept-Encoding: gzip"
	// // request header when the Request contains no existing
	// // Accept-Encoding value. If the Transport requests gzip on
	// // its own and gets a gzipped response, it's transparently
	// // decoded in the Response.Body. However, if the user
	// // explicitly requested gzip it is not automatically
	// // uncompressed.
	// DisableCompression bool

	// // MaxIdleConns controls the maximum number of idle (keep-alive)
	// // connections across all hosts. Zero means no limit.
	// MaxIdleConns int

	// // MaxIdleConnsPerHost, if non-zero, controls the maximum idle
	// // (keep-alive) connections to keep per-host. If zero,
	// // DefaultMaxIdleConnsPerHost is used.
	// MaxIdleConnsPerHost int

	// // MaxConnsPerHost optionally limits the total number of
	// // connections per host, including connections in the dialing,
	// // active, and idle states. On limit violation, dials will block.
	// //
	// // Zero means no limit.
	// MaxConnsPerHost int

	// // IdleConnTimeout is the maximum amount of time an idle
	// // (keep-alive) connection will remain idle before closing
	// // itself.
	// // Zero means no limit.
	// IdleConnTimeout time.Duration

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

	// // MaxResponseHeaderBytes specifies a limit on how many
	// // response bytes are allowed in the server's response
	// // header.
	// //
	// // Zero means to use a default limit.
	// MaxResponseHeaderBytes int64

	// // WriteBufferSize specifies the size of the write buffer used
	// // when writing to the transport.
	// // If zero, a default (currently 4KB) is used.
	// WriteBufferSize int

	// // ReadBufferSize specifies the size of the read buffer used
	// // when reading from the transport.
	// // If zero, a default (currently 4KB) is used.
	// ReadBufferSize int

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
			// t.setReqCanceler(cancelKey, nil)
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

// getConn dials and creates a new persistConn to the target as
// specified in the connectMethod. This includes doing a proxy CONNECT
// and/or setting up TLS.  If this doesn't return an error, the persistConn
// is ready to write requests to.
func (t *Transport) getConn(treq *transportRequest, cm connectMethod) (pc *persistConn, err error) {
	// TODO: Implement logic.
	return nil, nil
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

// persistConn wraps a connection, usually a persistent one
// (but may be used for non-keep-alive requests as well)
type persistConn struct {
	// // alt optionally specifies the TLS NextProto RoundTripper.
	// // This is used for HTTP/2 today and future protocols later.
	// // If it's non-nil, the rest of the fields are unused.
	// alt RoundTripper

	// t         *Transport
	// cacheKey  connectMethodKey
	// conn      net.Conn
	// tlsState  *tls.ConnectionState
	// br        *bufio.Reader       // from conn
	// bw        *bufio.Writer       // to conn
	// nwrite    int64               // bytes written
	// reqch     chan requestAndChan // written by roundTrip; read by readLoop
	// writech   chan writeRequest   // written by roundTrip; read by writeLoop
	// closech   chan struct{}       // closed when conn closed
	// isProxy   bool
	// sawEOF    bool  // whether we've seen EOF from conn; owned by readLoop
	// readLimit int64 // bytes allowed to be read; owned by readLoop
	// // writeErrCh passes the request write error (usually nil)
	// // from the writeLoop goroutine to the readLoop which passes
	// // it off to the res.Body reader, which then uses it to decide
	// // whether or not a connection can be reused. Issue 7569.
	// writeErrCh chan error

	// writeLoopDone chan struct{} // closed when write loop ends

	// // Both guarded by Transport.idleMu:
	// idleAt    time.Time   // time it last become idle
	// idleTimer *time.Timer // holding an AfterFunc to close it

	// mu                   sync.Mutex // guards following fields
	// numExpectedResponses int
	// closed               error // set non-nil when conn is closed, before closech is closed
	// canceledErr          error // set non-nil if conn is canceled
	// broken               bool  // an error has happened on this connection; marked broken so it's not reused.
	// reused               bool  // whether conn has had successful request/response and is being reused.
	// // mutateHeaderFunc is an optional func to modify extra
	// // headers on each outbound request before it's written. (the
	// // original Request given to RoundTrip is not modified)
	// mutateHeaderFunc func(Header)
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
