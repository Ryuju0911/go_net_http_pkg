// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// HTTP client implementation. See RFC 7230 through 7235.
//
// This is the low-level Transport implementation of RoundTripper.
// The high-level interface is in client.go.

package http

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

type Transport struct{}

// roundTrip implements a RoundTripper over HTTP.
func (t *Transport) roundTrip(req *Request) (*Response, error) {
	return nil, nil
}
