// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package httptrace provides mechanisms to trace the events within
// HTTP client requests.
package httptrace

import "context"

// unique type to prevent assignment.
type clientEventContextKey struct{}

// ContextClientTrace returns the [ClientTrace] associated with the
// provided context. If none, it returns nil.
func ContextClientTrace(ctx context.Context) *ClientTrace {
	trace, _ := ctx.Value(clientEventContextKey{}).(*ClientTrace)
	return trace
}

// ClientTrace is a set of hooks to run at various stages of an outgoing
// HTTP request. Any particular hook may be nil. Functions may be
// called concurrently from different goroutines and some may be called
// after the request has completed or failed.
//
// ClientTrace currently traces a single HTTP request & response
// during a single round trip and has no hooks that span a series
// of redirected requests.
//
// See https://blog.golang.org/http-tracing for more.
type ClientTrace struct {
	// // GetConn is called before a connection is created or
	// // retrieved from an idle pool. The hostPort is the
	// // "host:port" of the target or proxy. GetConn is called even
	// // if there's already an idle cached connection available.
	// GetConn func(hostPort string)

	// // GotConn is called after a successful connection is
	// // obtained. There is no hook for failure to obtain a
	// // connection; instead, use the error from
	// // Transport.RoundTrip.
	// GotConn func(GotConnInfo)

	// // PutIdleConn is called when the connection is returned to
	// // the idle pool. If err is nil, the connection was
	// // successfully returned to the idle pool. If err is non-nil,
	// // it describes why not. PutIdleConn is not called if
	// // connection reuse is disabled via Transport.DisableKeepAlives.
	// // PutIdleConn is called before the caller's Response.Body.Close
	// // call returns.
	// // For HTTP/2, this hook is not currently used.
	// PutIdleConn func(err error)

	// // GotFirstResponseByte is called when the first byte of the response
	// // headers is available.
	// GotFirstResponseByte func()

	// // Got100Continue is called if the server replies with a "100
	// // Continue" response.
	// Got100Continue func()

	// // Got1xxResponse is called for each 1xx informational response header
	// // returned before the final non-1xx response. Got1xxResponse is called
	// // for "100 Continue" responses, even if Got100Continue is also defined.
	// // If it returns an error, the client request is aborted with that error value.
	// Got1xxResponse func(code int, header textproto.MIMEHeader) error

	// // DNSStart is called when a DNS lookup begins.
	// DNSStart func(DNSStartInfo)

	// // DNSDone is called when a DNS lookup ends.
	// DNSDone func(DNSDoneInfo)

	// // ConnectStart is called when a new connection's Dial begins.
	// // If net.Dialer.DualStack (IPv6 "Happy Eyeballs") support is
	// // enabled, this may be called multiple times.
	// ConnectStart func(network, addr string)

	// // ConnectDone is called when a new connection's Dial
	// // completes. The provided err indicates whether the
	// // connection completed successfully.
	// // If net.Dialer.DualStack ("Happy Eyeballs") support is
	// // enabled, this may be called multiple times.
	// ConnectDone func(network, addr string, err error)

	// // TLSHandshakeStart is called when the TLS handshake is started. When
	// // connecting to an HTTPS site via an HTTP proxy, the handshake happens
	// // after the CONNECT request is processed by the proxy.
	// TLSHandshakeStart func()

	// // TLSHandshakeDone is called after the TLS handshake with either the
	// // successful handshake's connection state, or a non-nil error on handshake
	// // failure.
	// TLSHandshakeDone func(tls.ConnectionState, error)

	// WroteHeaderField is called after the Transport has written
	// each request header. At the time of this call the values
	// might be buffered and not yet written to the network.
	WroteHeaderField func(key string, value []string)

	// // WroteHeaders is called after the Transport has written
	// // all request headers.
	// WroteHeaders func()

	// // Wait100Continue is called if the Request specified
	// // "Expect: 100-continue" and the Transport has written the
	// // request headers but is waiting for "100 Continue" from the
	// // server before writing the request body.
	// Wait100Continue func()

	// // WroteRequest is called with the result of writing the
	// // request and any body. It may be called multiple times
	// // in the case of retried requests.
	// WroteRequest func(WroteRequestInfo)
}
