// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Tests that use both the client & server, in both HTTP/1 and HTTP/2 mode.

package http_test

import (
	"log"
	. "net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type testMode string

const (
	http1Mode = testMode("h1") // HTTP/1.1
	// https1Mode = testMode("https1") // HTTPS/1.1
	http2Mode = testMode("h2") // HTTP/2
)

type clientServerTest struct {
	t  testing.TB
	h2 bool
	h  Handler
	ts *httptest.Server
	tr *Transport
	c  *Client
}

func (t *clientServerTest) close() {
	t.tr.CloseIdleConnections()
	t.ts.Close()
}

// newClientServerTest creates and starts an httptest.Server.
//
// The mode parameter selects the implementation to test:
// HTTP/1, HTTP/2, etc. Tests using newClientServerTest should use
// the 'run' function, which will start a subtests for each tested mode.
//
// The vararg opts parameter can include functions to configure the
// test server or transport.
//
//	func(*httptest.Server) // run before starting the server
//	func(*http.Transport)
func newClientServerTest(t testing.TB, mode testMode, h Handler, opts ...any) *clientServerTest {
	// if mode == http2Mode {
	// 	CondSkipHTTP2(t)
	// }
	cst := &clientServerTest{
		t:  t,
		h2: mode == http2Mode,
		h:  h,
	}
	cst.ts = httptest.NewUnstartedServer(h)

	var transportFuncs []func(*Transport)
	for _, opt := range opts {
		switch opt := opt.(type) {
		case func(*Transport):
			transportFuncs = append(transportFuncs, opt)
		case func(*httptest.Server):
			opt(cst.ts)
		default:
			t.Fatalf("unhandled option type %T", opt)
		}
	}

	if cst.ts.Config.ErrorLog == nil {
		cst.ts.Config.ErrorLog = log.New(testLogWriter{t}, "", 0)
	}

	switch mode {
	case http1Mode:
		cst.ts.Start()
	// case https1Mode:
	// 	cst.ts.StartTLS()
	// case http2Mode:
	// 	ExportHttp2ConfigureServer(cst.ts.Config, nil)
	// 	cst.ts.TLS = cst.ts.Config.TLSConfig
	// 	cst.ts.StartTLS()
	default:
		t.Fatalf("unknown test mode %v", mode)
	}
	cst.c = cst.ts.Client()
	cst.tr = cst.c.Transport.(*Transport)
	// if mode == http2Mode {
	// 	if err := ExportHttp2ConfigureTransport(cst.tr); err != nil {
	// 		t.Fatal(err)
	// 	}
	// }
	for _, f := range transportFuncs {
		f(cst.tr)
	}
	t.Cleanup(func() {
		cst.close()
	})
	return cst
}

type testLogWriter struct {
	t testing.TB
}

func (w testLogWriter) Write(b []byte) (int, error) {
	w.t.Logf("server log: %v", strings.TrimSpace(string(b)))
	return len(b), nil
}
