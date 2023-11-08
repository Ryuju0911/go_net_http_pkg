// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// HTTP server. See RFC 7230 through 7235.

package http

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
)

// A Handler responds to an HTTP request.
type Handler interface {
	ServeHTTP(ResponseWriter, *Request)
}

// A ResponseWriter interface is used by an HTTP handler to
// construct an HTTP response.
type ResponseWriter interface {
	Write([]byte) (int, error)
}

var (
	// ServerContextKey is a context key. It can be used in HTTP
	// handlers with Context.Value to access the server that
	// started the handler. The associated value will be of
	// type *Server.
	ServerContextKey = &contextKey{"http-server"}

	// LocalAddrContextKey is a context key. It can be used in
	// HTTP handlers with Context.Value to access the local
	// address the connection arrived on.
	// The associated value will be of type net.Addr.
	LocalAddrContextKey = &contextKey{"local-addr"}
)

type conn struct {
	// server is the server on which the connection arrived.
	// Immutable; never nil.
	server *Server

	// cancelCtx cancels the connection-level context.
	cancelCtx context.CancelFunc

	// rwc is the underlying network connection.
	// This is never wrapped by other types and is the value given out
	// to CloseNotifier callers. It is usually of type *net.TCPConn or
	// *tls.Conn.
	rwc net.Conn

	// // remoteAddr is rwc.RemoteAddr().String(). It is not populated synchronously
	// // inside the Listener's Accept goroutine, as some implementations block.
	// // It is populated immediately inside the (*conn).serve goroutine.
	// // This is the value of a Handler's (*Request).RemoteAddr.
	// remoteAddr string

	// werr is set to the first write error to rwc.
	// It is set via checkConnErrorWriter{w}, where bufw writes.
	werr error

	// r is bufr's read source. It's a wrapper around rwc that provides
	// io.LimitedReader-style limiting (while reading request headers)
	// and functionality to support CloseNotifier. See *connReader docs.
	r *connReader

	// bufr reads from r.
	bufr *bufio.Reader

	// bufw writes to checkConnErrorWriter{c}, which populates werr on error.
	bufw *bufio.Writer

	// lastMethod is the method of the most recent request
	// on this connection, if any.
	lastMethod string

	curReq atomic.Pointer[response] // (which has a Request in it)

	// curState atomic.Uint64 // packed (unixtime<<8|uint8(ConnState))

	// // mu guards hijackedv
	// mu sync.Mutex
}

// This should be >= 512 bytes for DetectContentType,
// but otherwise it's somewhat arbitrary.
const bufferBeforeChunkingSize = 2048

// chunkWriter writes to a response's conn buffer, and is the writer
// wrapped by the response.w buffered writer.
//
// chunkWriter also is responsible for finalizing the Header, including
// conditionally setting the Content-Type and setting a Content-Length
// in cases where the handler's final output is smaller than the buffer
// size. It also conditionally adds chunk headers, when in chunking mode.
//
// See the comment above (*response).Write for the entire write flow.
type chunkWriter struct {
	res *response

	// header is either nil or a deep clone of res.handlerHeader
	// at the time of res.writeHeader, if res.writeHeader is
	// called and extra buffering is being done to calculate
	// Content-Type and/or Content-Length.
	// header Header

	// wroteHeader tells whether the header's been written to "the
	// wire" (or rather: w.conn.buf). this is unlike
	// (*response).wroteHeader, which tells only whether it was
	// logically written.
	// wroteHeader bool

	// set by the writeHeader method:
	// chunking bool // using chunked transfer encoding for reply body
}

func (cw *chunkWriter) Write(p []byte) (n int, err error) {
	// if !cw.wroteHeader {
	// 	cw.writeHeader(p)
	// }
	// if cw.res.req.Method == "HEAD" {
	// 	// Eat writes.
	// 	return len(p), nil
	// }
	// if cw.chunking {
	// 	_, err = fmt.Fprintf(cw.res.conn.bufw, "%x\r\n", len(p))
	// 	if err != nil {
	// 		cw.res.conn.rwc.Close()
	// 		return
	// 	}
	// }
	// n, err = cw.res.conn.bufw.Write(p)
	// if cw.chunking && err == nil {
	// 	_, err = cw.res.conn.bufw.Write(crlf)
	// }
	// if err != nil {
	// 	cw.res.conn.rwc.Close()
	// }
	// return
	return len(p), nil
}

// A response represents the server side of an HTTP response.
type response struct {
	conn        *conn
	req         *Request // request for this response
	reqBody     io.ReadCloser
	cancelCtx   context.CancelFunc // when ServeHTTP exits
	wroteHeader bool               // a non-1xx header has been (logically) written
	// wroteContinue    bool               // 100 Continue response was written
	// wants10KeepAlive bool               // HTTP/1.0 w/ Connection "keep-alive"
	// wantsClose       bool               // HTTP request has Connection "close"

	// // canWriteContinue is an atomic boolean that says whether or
	// // not a 100 Continue header can be written to the
	// // connection.
	// // writeContinueMu must be held while writing the header.
	// // These two fields together synchronize the body reader (the
	// // expectContinueReader, which wants to write 100 Continue)
	// // against the main writer.
	// canWriteContinue atomic.Bool
	// writeContinueMu  sync.Mutex

	w  *bufio.Writer // buffers output in chunks to chunkWriter
	cw chunkWriter

	// handlerHeader is the Header that Handlers get access to,
	// which may be retained and mutated even after WriteHeader.
	// handlerHeader is copied into cw.header at WriteHeader
	// time, and privately mutated thereafter.
	handlerHeader Header
	// calledHeader  bool // handler accessed handlerHeader via Header

	// written       int64 // number of bytes written in body
	contentLength int64 // explicitly-declared Content-Length; or -1
	status        int   // status code passed to WriteHeader

	// // close connection after this reply.  set on request and
	// // updated after response from handler if there's a
	// // "Connection: keep-alive" response header and a
	// // Content-Length.
	// closeAfterReply bool

	// // When fullDuplex is false (the default), we consume any remaining
	// // request body before starting to write a response.
	// fullDuplex bool

	// // requestBodyLimitHit is set by requestTooLarge when
	// // maxBytesReader hits its max size. It is checked in
	// // WriteHeader, to make sure we don't consume the
	// // remaining request body to try to advance to the next HTTP
	// // request. Instead, when this is set, we stop reading
	// // subsequent requests on this connection and stop reading
	// // input from it.
	// requestBodyLimitHit bool

	// // trailers are the headers to be sent after the handler
	// // finishes writing the body. This field is initialized from
	// // the Trailer response header when the response header is
	// // written.
	// trailers []string

	// handlerDone atomic.Bool // set true when the handler exits

	// // Buffers for Date, Content-Length, and status code
	// dateBuf   [len(TimeFormat)]byte
	// clenBuf   [10]byte
	// statusBuf [3]byte

	// closeNotifyCh is the channel returned by CloseNotify.
	// TODO(bradfitz): this is currently (for Go 1.8) always
	// non-nil. Make this lazily-created again as it used to be?
	closeNotifyCh  chan bool
	didCloseNotify atomic.Bool // atomic (only false->true winner should send)
}

func (srv *Server) newConn(rwc net.Conn) *conn {
	c := &conn{
		server: srv,
		rwc:    rwc,
	}
	return c
}

// connReader is the io.Reader wrapper used by *conn. It combines a
// selectively-activated io.LimitedReader (to bound request header
// read sizes) with support for selectively keeping an io.Reader.Read
// call blocked in a background goroutine to wait for activity and
// trigger a CloseNotifier channel.
type connReader struct {
	conn *conn

	mu      sync.Mutex // guards following
	hasByte bool
	byteBuf [1]byte
	cond    *sync.Cond
	inRead  bool
	// aborted bool  // set true before conn.rwc deadline is set to past
	remain int64 // bytes remaining
}

func (cr *connReader) lock() {
	cr.mu.Lock()
	if cr.cond == nil {
		cr.cond = sync.NewCond(&cr.mu)
	}
}

func (cr *connReader) unlock() { cr.mu.Unlock() }

func (cr *connReader) setReadLimit(remain int64) { cr.remain = remain }
func (cr *connReader) setInfiniteReadLimit()     { cr.remain = maxInt64 }
func (cr *connReader) hitReadLimit() bool        { return cr.remain <= 0 }

// handleReadError is called whenever a Read from the client returns a
// non-nil error.
//
// The provided non-nil err is almost always io.EOF or a "use of
// closed network connection". In any case, the error is not
// particularly interesting, except perhaps for debugging during
// development. Any error means the connection is dead and we should
// down its context.
//
// It may be called from multiple goroutines.
func (cr *connReader) handleReadError(_ error) {
	cr.conn.cancelCtx()
	cr.closeNotify()
}

func (cr *connReader) closeNotify() {
	res := cr.conn.curReq.Load()
	if res != nil && res.didCloseNotify.Swap(true) {
		res.closeNotifyCh <- true
	}
}

func (cr *connReader) Read(p []byte) (n int, err error) {
	cr.lock()
	if cr.inRead {
		cr.unlock()
		panic("invalid concurrent Body.Read call")
	}
	if cr.hitReadLimit() {
		cr.unlock()
		return 0, io.EOF
	}
	if len(p) == 0 {
		cr.unlock()
		return 0, nil
	}
	if int64(len(p)) > cr.remain {
		p = p[:cr.remain]
	}
	if cr.hasByte {
		p[0] = cr.byteBuf[0]
		cr.hasByte = false
		cr.unlock()
		return 1, nil
	}
	cr.inRead = true
	cr.unlock()
	n, err = cr.conn.rwc.Read(p)

	cr.lock()
	cr.inRead = false
	if err != nil {
		cr.handleReadError(err)
	}
	cr.remain -= int64(n)
	cr.unlock()

	cr.cond.Broadcast()
	return n, err
}

var (
	bufioReaderPool   sync.Pool
	bufioWriter2kPool sync.Pool
	bufioWriter4kPool sync.Pool
)

func bufioWriterPool(size int) *sync.Pool {
	switch size {
	case 2 << 10:
		return &bufioWriter2kPool
	case 4 << 10:
		return &bufioWriter4kPool
	}
	return nil
}

func newBufioReader(r io.Reader) *bufio.Reader {
	if v := bufioReaderPool.Get(); v != nil {
		br := v.(*bufio.Reader)
		br.Reset(r)
		return br
	}
	// Note: if this reader size is ever changed, update
	// TestHandleBodyClose's assumptions.
	return bufio.NewReader(r)
}

func newBufioWriterSize(w io.Writer, size int) *bufio.Writer {
	pool := bufioWriterPool(size)
	if pool != nil {
		if v := pool.Get(); v != nil {
			bw := v.(*bufio.Writer)
			bw.Reset(w)
			return bw
		}
	}
	return bufio.NewWriterSize(w, size)
}

var errTooLarge = errors.New("http: request too large")

// Read next request from connection.
func (c *conn) readRequst(ctx context.Context) (w *response, err error) {
	c.r.setInfiniteReadLimit() // Instead of setReadLimit(c.server.initialReadLimitSize())
	// if c.lastMethod == "POST" {
	// 	// RFC 7230 section 3 tolerance for old buggy clients.
	// 	peek, _ := c.bufr.Peek(4) // ReadRequst will get err below
	// 	c.bufr.Discard(numLeadingCRorLF(peek))
	// }
	req, err := readRequest(c.bufr)
	if err != nil {
		if c.r.hitReadLimit() {
			return nil, errTooLarge
		}
		return nil, err
	}

	c.lastMethod = req.Method
	c.r.setInfiniteReadLimit()

	ctx, cancelCtx := context.WithCancel(ctx)
	req.ctx = ctx
	// req.RemoteAddr = c.remoteAddr
	// req.TLS = c.tlsState
	// if body, ok := req.Body.(*body); ok {
	// 	body.doEarlyClose = true
	// }

	w = &response{
		conn:          c,
		cancelCtx:     cancelCtx,
		req:           req,
		reqBody:       req.Body,
		handlerHeader: make(Header),
		contentLength: -1,
		closeNotifyCh: make(chan bool, 1),

		// // We populate these ahead of time so we're not
		// // reading from req.Header after their Handler starts
		// // and maybe mutates it (Issue 14940)
		// wants10KeepAlive: req.wantsHttp10KeepAlive(),
		// wantsClose:       req.wantsClose(),
	}
	// if isH2Upgrade {
	// 	w.closeAfterReply = true
	// }
	w.cw.res = w
	w.w = newBufioWriterSize(&w.cw, bufferBeforeChunkingSize)
	return w, nil
}

func (w *response) WriteHeader(code int) {
	// TODO: Handle the case when 100 <= code <= 199.

	w.wroteHeader = true
	w.status = code
}

func (w *response) Write(data []byte) (n int, err error) {
	return w.write(len(data), data, "")
}

// either dataB or dataS is non-zero.
func (w *response) write(lenData int, dataB []byte, dataS string) (n int, err error) {
	if !w.wroteHeader {
		w.WriteHeader(StatusOK)
	}
	if lenData == 0 {
		return 0, nil
	}
	// if !w.bodyAllowed() {
	// 	return 0, ErrBodyNotAllowed
	// }

	// w.written += int64(lenData) // ignoring errors, for errorKludge
	// if w.contentLength != -1 && w.written > w.contentLength {
	// 	return 0, ErrContentLength
	// }
	if dataB != nil {
		return w.w.Write(dataB)
	} else {
		return w.w.WriteString(dataS)
	}
}

// Serve a new connection.
func (c *conn) serve(ctx context.Context) {
	// if ra := c.rwc.RemoteAddr(); ra != nil {
	// 	c.remoteAddr = ra.String()
	// }
	ctx = context.WithValue(ctx, LocalAddrContextKey, c.rwc.LocalAddr())

	// HTTP/1.x from here on.

	ctx, cancelCtx := context.WithCancel(ctx)
	c.cancelCtx = cancelCtx
	defer cancelCtx()

	c.r = &connReader{conn: c}
	c.bufr = newBufioReader(c.r)
	c.bufw = newBufioWriterSize(checkConnErrorWriter{c}, 4<<10)

	for {
		w, err := c.readRequst(ctx)
		// if c.r.remain != c.server.initialReadLimitSize() {
		// 	// If we read any bytes off the wire, we're active.
		// 	c.setState(c.rwc, StateActive, runHooks)
		// }
		if err != nil {
			// TODO: Implement error handling based on error type.
			const errorHeaders = "\r\nContent-Type: text/plain; charset=utf-8\r\nConnection: close\r\n\r\n"
			const publicErr = "400 Bad Request"
			fmt.Fprintf(c.rwc, "HTTP/1.1 "+publicErr+errorHeaders+publicErr)
			return
		}

		c.curReq.Store(w)

		// if requestBodyRemains(req.Body) {
		// 	registerOnHitEOF(req.Body, w.conn.r.startBackgroundRead)
		// } else {
		// 	w.conn.r.startBackgroundRead()
		// }

		serverHandler{c.server}.ServeHTTP(w, w.req)
		w.cancelCtx()
	}
}

// The HandlerFunc type is an adapter to allow the use of
// ordinary functions as HTTP handlers. If f is a function
// with the appropriate signature, HandlerFunc(f) is a
// Handler that calls f.
type HandlerFunc func(ResponseWriter, *Request)

func (f HandlerFunc) ServeHTTP(w ResponseWriter, r *Request) {
	f(w, r)
}

// ServeMux is an HTTP request multiplexer.
// It matches the URL of each incoming request against a list of registered
// patterns and calls the handler for the pattern that
// most closely matches the URL.
type ServeMux struct {
	mu sync.RWMutex
	m  map[string]muxEntry
	// es    []muxEntry // slice of entries sorted from longest to shortest.
	// hosts bool       // whether any patterns contain hostnames
}

type muxEntry struct {
	h       Handler
	pattern string
}

// DefaultServeMux is the default ServeMux used by Serve.
var DefaultServeMux = &defaultServeMux

var defaultServeMux ServeMux

func (mux *ServeMux) Handler(r *Request) (h Handler, pattern string) {
	path := r.URL.Path

	mux_entry := mux.m[path]
	return mux_entry.h, mux_entry.pattern
}

func (mux *ServeMux) ServeHTTP(w ResponseWriter, r *Request) {
	h, _ := mux.Handler(r)
	h.ServeHTTP(w, r)
}

func (mux *ServeMux) Handle(pattern string, handler Handler) {
	mux.mu.Lock()
	defer mux.mu.Unlock()

	if pattern == "" {
		panic("http: invalid pattern")
	}
	if handler == nil {
		panic("http: nil handler")
	}
	if _, exist := mux.m[pattern]; exist {
		panic("http: multiple registrations for " + pattern)
	}

	if mux.m == nil {
		mux.m = make(map[string]muxEntry)
	}
	e := muxEntry{h: handler, pattern: pattern}
	mux.m[pattern] = e
	// if pattern[len(pattern)-1] == '/' {
	// 	mux.es = appendSorted(mux.es, e)
	// }

	// if pattern[0] != '/' {
	// 	mux.hosts = true
	// }
}

func (mux *ServeMux) HandleFunc(pattern string, handler func(ResponseWriter, *Request)) {
	if handler == nil {
		panic("http: nil handler")
	}
	mux.Handle(pattern, HandlerFunc(handler))
}

func HandleFunc(pattern string, handler func(ResponseWriter, *Request)) {
	DefaultServeMux.HandleFunc(pattern, handler)
}

// A Server defines parameters for running an HTTP server.
// The zero value for Server is a valid configuration.
type Server struct {
	// Addr optionally specifies the TCP address for the server to listen on,
	// in the form "host:port". If empty, ":http" (port 80) is used.
	// The service names are defined in RFC 6335 and assigned by IANA.
	// See net.Dial for details of the address format.
	Addr string

	Handler Handler // handler to invoke, http.DefaultServeMux if nil

	// BaseContext optionally specifies a function that returns
	// the base context for incoming requests on this server.
	// The provided Listener is the specific Listener that's
	// about to start accepting requests.
	// If BaseContext is nil, the default is context.Background().
	// If non-nil, it must return a non-nil context.
	BaseContext func(net.Listener) context.Context

	// ConnContext optionally specifies a function that modifies
	// the context used for a new connection c. The provided ctx
	// is derived from the base context and has a ServerContextKey
	// value.
	ConnContext func(ctx context.Context, c net.Conn) context.Context

	nextProtoOnce sync.Once // guards setupHTTP2_* init
	nextProtoErr  error     // result of http2.ConfigureServer if used

	mu         sync.Mutex
	listeners  map[*net.Listener]struct{}
	onShutdown []func()

	listenerGroup sync.WaitGroup
}

// RegisterOnShutdown registers a function to call on Shutdown.
// This can be used to gracefully shutdown connections that have
// undergone ALPN protocol upgrade or that have been hijacked.
// This function should start protocol-specific graceful shutdown,
// but should not wait for shutdown to complete.
func (srv *Server) RegisterOnShutdown(f func()) {
	srv.mu.Lock()
	srv.onShutdown = append(srv.onShutdown, f)
	srv.mu.Unlock()
}

// ListenAndServe listens on the TCP network address srv.Addr and then
// calls Serve to handle requests on incoming connections.
// Accepted connections are configured to enable TCP keep-alives.
//
// If srv.Addr is blank, ":http" is used.
//
// ListenAndServe always returns a non-nil error. After Shutdown or Close,
// the returned error is ErrServerClosed.
func (srv *Server) ListenAndServe() error {
	// if srv.shuttingDown() {
	// 	return ErrServerClosed
	// }
	addr := srv.Addr
	if addr == "" {
		addr = ":http"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return srv.Serve(ln)
}

// ErrServerClosed is returned by the Server's Serve, ServeTLS, ListenAndServe,
// and ListenAndServeTLS methods after a call to Shutdown or Close.
var ErrServerClosed = errors.New("http: Server closed")

// Serve accepts incoming connections on the Listener l, creating a
// new service goroutine for each. The service goroutines read requests and
// then call srv.Handler to reply to them.
//
// HTTP/2 support is only enabled if the Listener returns *tls.Conn
// connections and they were configured with "h2" in the TLS
// Config.NextProtos.
//
// Serve always returns a non-nil error and closes l.
// After Shutdown or Close, the returned error is ErrServerClosed.
func (srv *Server) Serve(l net.Listener) error {
	origListener := l
	l = &onceCloseListener{Listener: l}
	defer l.Close()

	if err := srv.setupHTTP2_Serve(); err != nil {
		return err
	}

	if !srv.trackListener(&l, true) {
		return ErrServerClosed
	}

	baseCtx := context.Background()
	if srv.BaseContext != nil {
		baseCtx = srv.BaseContext(origListener)
		if baseCtx == nil {
			panic("BaseContext returened a nil context")
		}
	}

	// var tempDelay time.Duration // how long to sleep on accept failuer

	ctx := context.WithValue(baseCtx, ServerContextKey, srv)
	for {
		rw, _ := l.Accept()
		// TODO: handles the case when the err != nil.

		connCtx := ctx
		if cc := srv.ConnContext; cc != nil {
			connCtx = cc(connCtx, rw)
			if connCtx == nil {
				panic("ConnContext returned nil")
			}
		}
		// tempDelay = 0
		c := srv.newConn(rw)
		// // c.setState(c.rwc, StateNew, runHooks) // before Serve can return
		go c.serve(connCtx)
	}
}

func (s *Server) trackListener(ln *net.Listener, add bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listeners == nil {
		s.listeners = make(map[*net.Listener]struct{})
	}
	if add {
		// if s.shuttingDown() {
		// 	return false
		// }
		s.listeners[ln] = struct{}{}
		s.listenerGroup.Add(1)
	}
	return true
}

// serverHandler delegates to either the server's Handler or
// DefaultServeMux and also handles "OPTIONS *" requests.
type serverHandler struct {
	srv *Server
}

func (sh serverHandler) ServeHTTP(rw ResponseWriter, req *Request) {
	handler := sh.srv.Handler
	if handler == nil {
		handler = DefaultServeMux
	}

	handler.ServeHTTP(rw, req)
}

// ListenAndServe listens on the TCP network address addr and then calls
// Serve with handler to handle requests on incoming connections.
// Accepted connections are configured to enable TCP keep-alives.
//
// The handler is typically nil, in which case the DefaultServeMux is used.
//
// ListenAndServe always returns a non-nil error.
func ListenAndServe(addr string, handler Handler) error {
	server := &Server{Addr: addr, Handler: handler}
	return server.ListenAndServe()
}

// shouldConfigureHTTP2ForServe reports whether Server.Serve should configure
// automatic HTTP/2. (which sets up the srv.TLSNextProto map)
// Temporarily, it always returns true.
func (srv *Server) shouldConfigureHTTP2ForServe() bool {
	return true
}

// setupHTTP2_Serve is called from (*Server).Serve and conditionally
// configures HTTP/2 on srv using a more conservative policy than
// setupHTTP2_ServeTLS because Serve is called after tls.Listen,
// and may be called concurrently. See shouldConfigureHTTP2ForServe.
//
// The tests named TestTransportAutomaticHTTP2* and
// TestConcurrentServerServe in server_test.go demonstrate some
// of the supported use cases and motivations.
func (srv *Server) setupHTTP2_Serve() error {
	srv.nextProtoOnce.Do(srv.onceSetNextProtoDefaults_Serve)
	return srv.nextProtoErr
}

func (srv *Server) onceSetNextProtoDefaults_Serve() {
	if srv.shouldConfigureHTTP2ForServe() {
		srv.onceSetNextProtoDefaults()
	}
}

// onceSetNextProtoDefaults configures HTTP/2, if the user hasn't
// configured otherwise. (by setting srv.TLSNextProto non-nil)
// It must only be called via srv.nextProtoOnce (use srv.setupHTTP2_*).
func (srv *Server) onceSetNextProtoDefaults() {
	// if omitBundledHTTP2 {
	// 	return
	// }
	// if http2server.Value() == "0" {
	// 	http2server.IncNonDefault()
	// 	return
	// }
	conf := &http2Server{
		NewWriteScheduler: func() http2WriteScheduler { return http2NewPriorityWriteScheduler(nil) },
	}
	srv.nextProtoErr = http2ConfigureServer(srv, conf)
}

// onceCloseListener wraps a net.Listener, protecting it from
// multiple Close calls.
type onceCloseListener struct {
	net.Listener
	once     sync.Once
	closeErr error
}

func (oc *onceCloseListener) Close() error {
	oc.once.Do(oc.close)
	return oc.closeErr
}

func (oc *onceCloseListener) close() { oc.closeErr = oc.Listener.Close() }

// checkConnErrorWriter writes to c.rwc and records any write errors to c.werr.
// It only contains one field (and a pointer field at that), so it
// fits in an interface value without an extra allocation.
type checkConnErrorWriter struct {
	c *conn
}

func (w checkConnErrorWriter) Write(p []byte) (n int, err error) {
	n, err = w.c.rwc.Write(p)
	if err != nil && w.c.werr == nil {
		w.c.werr = err
		w.c.cancelCtx()
	}
	return
}
