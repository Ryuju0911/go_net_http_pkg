// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Tests for transport.go.
//
// More tests are in clientserver_test.go (for things testing both client & server for both
// HTTP/1 and HTTP/2). This

package http_test

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	. "net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReuetRequest(t *testing.T) { run(t, testReuseRequest) }
func testReuseRequest(t *testing.T, mode testMode) {
	ts := newClientServerTest(t, mode, HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Write([]byte("{}"))
	})).ts

	c := ts.Client()
	req, _ := NewRequest("GET", ts.URL, nil)
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	err = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}

	res, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	err = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
}

func TestTransportMaxPerHostIdleConns(t *testing.T) {
	run(t, testTransportMaxPerHostIdleConns, []testMode{http1Mode})
}
func testTransportMaxPerHostIdleConns(t *testing.T, mode testMode) {
	stop := make(chan struct{}) // stop marks the exit of main Test goroutine
	defer close(stop)

	resch := make(chan string)
	gotReq := make(chan bool)
	ts := newClientServerTest(t, mode, HandlerFunc(func(w ResponseWriter, r *Request) {
		gotReq <- true
		var msg string
		select {
		case <-stop:
			return
		case msg = <-resch:
		}
		_, err := w.Write([]byte(msg))
		if err != nil {
			t.Errorf("Write: %v", err)
			return
		}
	})).ts

	c := ts.Client()
	tr := c.Transport.(*Transport)
	maxIdleConnsPerHost := 2
	tr.MaxIdleConnsPerHost = maxIdleConnsPerHost

	// Start 3 outstanding requests and wait for the server to get them.
	// Their responses will hang until we write to resch, though.
	donech := make(chan bool)
	doReq := func() {
		defer func() {
			select {
			case <-stop:
				return
			case donech <- t.Failed():
			}
		}()
		resp, err := c.Get(ts.URL)
		if err != nil {
			t.Error(err)
			return
		}
		if _, err := io.ReadAll(resp.Body); err != nil {
			t.Errorf("ReadAll: %v", err)
			return
		}
	}

	go doReq()
	<-gotReq
	go doReq()
	<-gotReq
	go doReq()
	<-gotReq

	if e, g := 0, len(tr.IdleConnKeysForTesting()); e != g {
		t.Fatalf("Before writes, expected %d idle conn cache keys; got %d", e, g)
	}

	resch <- "res1"
	<-donech
	keys := tr.IdleConnKeysForTesting()
	if e, g := 1, len(keys); e != g {
		t.Fatalf("after first response, expected %d idle conn cache keys; got %d", e, g)
	}
	addr := ts.Listener.Addr().String()
	cacheKey := "|http|" + addr
	if keys[0] != cacheKey {
		t.Fatalf("Expected idle cache key %q; got %q", cacheKey, keys[0])
	}
	if e, g := 1, tr.IdleConnCountForTesting("http", addr); e != g {
		t.Errorf("after first response, expected %d idle conns; got %d", e, g)
	}

	resch <- "res2"
	<-donech
	if g, w := tr.IdleConnCountForTesting("http", addr), 2; g != w {
		t.Errorf("after second response, idle conns = %d; want %d", g, w)
	}

	resch <- "res3"
	<-donech
	if g, w := tr.IdleConnCountForTesting("http", addr), maxIdleConnsPerHost; g != w {
		t.Errorf("after third response, idle conns = %d; want %d", g, w)
	}
}

var roundTripTests = []struct {
	accept       string
	expectAccept string
	compressed   bool
}{
	// Requests with no accept-encoding header use transparent compression
	{"", "gzip", false},
	// Requests with other accept-encoding should pass through unmodified
	{"foo", "foo", false},
	// Requests with accept-encoding == gzip should be passed through
	{"gzip", "gzip", true},
}

// Test that the modification made to the Request by the RoundTripper is cleaned up
func TestRoundTripGzip(t *testing.T) { run(t, testRoundTripGzip) }
func testRoundTripGzip(t *testing.T, mode testMode) {
	const responseBody = "test response body"
	ts := newClientServerTest(t, mode, HandlerFunc(func(rw ResponseWriter, req *Request) {
		accept := req.Header.Get("Accept-Encoding")
		if expect := req.FormValue("expect_accept"); accept != expect {
			t.Errorf("in handler, test %v: Accept-Encoding = %q, want %q",
				req.FormValue("testnum"), accept, expect)
		}
		if accept == "gzip" {
			rw.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(rw)
			gz.Write([]byte(responseBody))
			gz.Close()
		} else {
			rw.Header().Set("Content-Encoding", accept)
			rw.Write([]byte(responseBody))
		}
	})).ts
	tr := ts.Client().Transport.(*Transport)

	for i, test := range roundTripTests {
		// Test basic request (no accept-encoding)
		req, _ := NewRequest("GET", fmt.Sprintf("%s/?testnum=%d&expect_accept=%s", ts.URL, i, test.expectAccept), nil)
		if test.accept != "" {
			req.Header.Set("Accept-Encoding", test.accept)
		}
		res, err := tr.RoundTrip(req)
		if err != nil {
			t.Errorf("%d. RounfTrip: %v", i, err)
			continue
		}
		var body []byte
		if test.compressed {
			var r *gzip.Reader
			r, err = gzip.NewReader(res.Body)
			if err != nil {
				t.Errorf("%d. gzip NewReader: %v", i, err)
				continue
			}
			body, err = io.ReadAll(r)
			res.Body.Close()
		} else {
			body, err = io.ReadAll(res.Body)
		}
		if err != nil {
			t.Errorf("%d. Error: %q", i, err)
			continue
		}
		if g, e := string(body), responseBody; g != e {
			t.Errorf("%d. body = %q; want %q", i, g, e)
		}
		if g, e := req.Header.Get("Accept-Encoding"), test.accept; g != e {
			t.Errorf("%d. Accept-Encoding = %q; want %q (it was mutated, in violation of RoundTrip contract)", i, g, e)
		}
		if g, e := res.Header.Get("Content-Encoding"), test.accept; g != e {
			t.Errorf("%d. Content-Encoding = %q; want %q", i, g, e)
		}
	}
}

func TestTransportGzip(t *testing.T) { run(t, testTransportGzip) }
func testTransportGzip(t *testing.T, mode testMode) {
	if mode == http2Mode {
		t.Skip("https://go.dev/issue/56020")
	}
	const testString = "The test string aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const nRandBytes = 1024 * 1024
	ts := newClientServerTest(t, mode, HandlerFunc(func(rw ResponseWriter, req *Request) {
		if req.Method == "HEAD" {
			if g := req.Header.Get("Accept-Encoding"); g != "" {
				t.Errorf("HEAD request sent with Accept-Encoding of %q; want none", g)
			}
			return
		}
		if g, e := req.Header.Get("Accept-Encoding"), "gzip"; g != e {
			t.Errorf("Accept-Encoding = %q, want %q", g, e)
		}
		rw.Header().Set("Content-Encoding", "gzip")

		var w io.Writer = rw
		var buf bytes.Buffer
		if req.FormValue("chunked") == "0" {
			w = &buf
			defer io.Copy(rw, &buf)
			defer func() {
				rw.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
			}()
		}
		gz := gzip.NewWriter(w)
		gz.Write([]byte(testString))
		if req.FormValue("body") == "large" {
			io.CopyN(gz, rand.Reader, nRandBytes)
		}
		gz.Close()
	})).ts
	c := ts.Client()

	for _, chunked := range []string{"1", "0"} {
		// First fetch something large, but only read some of it.
		res, err := c.Get(ts.URL + "/?body=large&chunked=" + chunked)
		if err != nil {
			t.Fatalf("large get: %v", err)
		}
		buf := make([]byte, len(testString))
		n, err := io.ReadFull(res.Body, buf)
		if err != nil {
			t.Fatalf("partial read of large response: size=%d, %v", n, err)
		}
		if e, g := testString, string(buf); e != g {
			t.Errorf("partial read got %q, expected %q", g, e)
		}
		res.Body.Close()
		// Read on the body, even though it's closed
		n, err = res.Body.Read(buf)
		if n != 0 || err == nil {
			t.Errorf("expected error post-closed large Read; got = %d, %v", n, err)
		}

		// Then something small.
		res, err = c.Get(ts.URL + "/?chunked=" + chunked)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if g, e := string(body), testString; g != e {
			t.Fatalf("body = %q; want %q", g, e)
		}
		if g, e := res.Header.Get("Content-Encoding"), ""; g != e {
			t.Fatalf("Content-Encoding = %q; want %q", g, e)
		}

		// Read on the body after it's been fully read:
		n, err = res.Body.Read(buf)
		if n != 0 || err == nil {
			t.Errorf("expected Read error after exhausted reads; got %d, %v", n, err)
		}
		res.Body.Close()
		n, err = res.Body.Read(buf)
		if n != 0 || err == nil {
			t.Errorf("expected Read error after Close; got %d, %v", n, err)
		}
	}
}

func TestTransportProxy(t *testing.T) {
	defer afterTest(t)
	testCases := []struct{ siteMode, proxyMode testMode }{
		{http1Mode, http1Mode},
		{http1Mode, https1Mode},
		{https1Mode, http1Mode},
		{https1Mode, https1Mode},
	}
	for _, testCase := range testCases {
		siteMode := testCase.siteMode
		proxyMode := testCase.proxyMode
		t.Run(fmt.Sprintf("site=%v/proxy=%v", siteMode, proxyMode), func(t *testing.T) {
			siteCh := make(chan *Request, 1)
			h1 := HandlerFunc(func(w ResponseWriter, r *Request) {
				siteCh <- r
			})
			proxyCh := make(chan *Request, 1)
			h2 := HandlerFunc(func(w ResponseWriter, r *Request) {
				proxyCh <- r
				// Implement an entire CONNECT proxy
				if r.Method == "CONNECT" {
					hijacker, ok := w.(Hijacker)
					if !ok {
						t.Errorf("hijack not allowed")
						return
					}
					clientConn, _, err := hijacker.Hijack()
					if err != nil {
						t.Errorf("hijacking failed")
						return
					}
					res := &Response{
						StatusCode: StatusOK,
						Proto:      "HTTP/1.1",
						ProtoMajor: 1,
						ProtoMinor: 1,
						Header:     make(Header),
					}

					targetConn, err := net.Dial("tcp", r.URL.Host)
					if err != nil {
						t.Errorf("net.Dial(%q) failed: %v", r.URL.Host, err)
						return
					}

					if err := res.Write(clientConn); err != nil {
						t.Errorf("Writing 200 OK failed: %v", err)
						return
					}

					go io.Copy(targetConn, clientConn)
					go func() {
						io.Copy(clientConn, targetConn)
						targetConn.Close()
					}()
				}
			})
			ts := newClientServerTest(t, siteMode, h1).ts
			proxy := newClientServerTest(t, proxyMode, h2).ts

			pu, err := url.Parse(proxy.URL)
			if err != nil {
				t.Fatal(err)
			}

			// If neither server is HTTPS or both are, then c may be derived from either.
			// If only one server is HTTPS, c must be derived from that server in order
			// to ensure that it is configured to use the fake root CA from testcert.go.
			c := proxy.Client()
			if siteMode == https1Mode {
				c = ts.Client()
			}

			c.Transport.(*Transport).Proxy = ProxyURL(pu)
			if _, err := c.Head(ts.URL); err != nil {
				t.Error(err)
			}
			got := <-proxyCh
			c.Transport.(*Transport).CloseIdleConnections()
			ts.Close()
			if siteMode == https1Mode {
				// First message should be a CONNECT, asking for a socket to the real server,
				if got.Method != "CONNECT" {
					t.Errorf("Wrong method for secure proxying: %q", got.Method)
				}
				gotHost := got.URL.Host
				pu, err := url.Parse(ts.URL)
				if err != nil {
					t.Fatal("Invalid site URL")
				}
				if wantHost := pu.Host; gotHost != wantHost {
					t.Errorf("Got CONNECT host %q, want %q", gotHost, wantHost)
				}

				// The next message on the channel should be from the site's server.
				next := <-siteCh
				if next.Method != "HEAD" {
					t.Errorf("Wrong method at destination: %s", next.Method)
				}
				if nextURL := next.URL.String(); nextURL != "/" {
					t.Errorf("Wrong URL at destination: %s", nextURL)
				}
			} else {
				if got.Method != "HEAD" {
					t.Errorf("Wrong method for destination: %q", got.Method)
				}
				gotURL := got.URL.String()
				wantURL := ts.URL + "/"
				if gotURL != wantURL {
					t.Errorf("Got URL %q, want %q", gotURL, wantURL)
				}
			}
		})
	}
}

func TestOnProxyConnectResponse(t *testing.T) {

	var tcases = []struct {
		proxyStatusCode int
		err             error
	}{
		{
			StatusOK,
			nil,
		},
		{
			StatusForbidden,
			errors.New("403"),
		},
	}
	for _, tcase := range tcases {
		h1 := HandlerFunc(func(w ResponseWriter, r *Request) {

		})

		h2 := HandlerFunc(func(w ResponseWriter, r *Request) {
			// Implement an entire CONNECT proxy
			if r.Method == "CONNECT" {
				if tcase.proxyStatusCode != StatusOK {
					w.WriteHeader(tcase.proxyStatusCode)
					return
				}
				hijacker, ok := w.(Hijacker)
				if !ok {
					t.Errorf("hijack not allowed")
					return
				}
				clientConn, _, err := hijacker.Hijack()
				if err != nil {
					t.Errorf("hijacking failed")
					return
				}
				res := &Response{
					StatusCode: StatusOK,
					Proto:      "HTTP/1.1",
					ProtoMajor: 1,
					ProtoMinor: 1,
					Header:     make(Header),
				}

				targetConn, err := net.Dial("tcp", r.URL.Host)
				if err != nil {
					t.Errorf("net.Dial(%q) failed: %v", r.URL.Host, err)
					return
				}

				if err := res.Write(clientConn); err != nil {
					t.Errorf("Writing 200 OK failed: %v", err)
					return
				}

				go io.Copy(targetConn, clientConn)
				go func() {
					io.Copy(clientConn, targetConn)
					targetConn.Close()
				}()
			}
		})
		ts := newClientServerTest(t, https1Mode, h1).ts
		proxy := newClientServerTest(t, https1Mode, h2).ts

		pu, err := url.Parse(proxy.URL)
		if err != nil {
			t.Fatal(err)
		}

		c := proxy.Client()

		var (
			dials  atomic.Int32
			closes atomic.Int32
		)
		c.Transport.(*Transport).DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := net.Dial(network, addr)
			if err != nil {
				return nil, err
			}
			dials.Add(1)
			return noteCloseConn{
				Conn: conn,
				closeFunc: func() {
					closes.Add(1)
				},
			}, nil
		}

		c.Transport.(*Transport).Proxy = ProxyURL(pu)
		c.Transport.(*Transport).OnProxyConnectResponse = func(ctx context.Context, proxyURL *url.URL, connectReq *Request, connectRes *Response) error {
			if proxyURL.String() != pu.String() {
				t.Errorf("proxy url got %s, want %s", proxyURL, pu)
			}

			if "https://"+connectReq.URL.String() != ts.URL {
				t.Errorf("connect url got %s, want %s", connectReq.URL, ts.URL)
			}
			return tcase.err
		}
		wantCloses := int32(0)
		if _, err := c.Head(ts.URL); err != nil {
			wantCloses = 1
			if tcase.err != nil && !strings.Contains(err.Error(), tcase.err.Error()) {
				t.Errorf("got %v, want %v", err, tcase.err)
			}
		} else {
			if tcase.err != nil {
				t.Errorf("got %v, want nil", err)
			}
		}
		if got, want := dials.Load(), int32(1); got != want {
			t.Errorf("got %v dials, want %v", got, want)
		}
		// #64804: If OnProxyConnectResponse returns an error, we should close the conn.
		if got, want := closes.Load(), wantCloses; got != want {
			t.Errorf("got %v closes, want %v", got, want)
		}
	}
}

// Issue 26161: the HTTP client must treat 101 responses
// as the final response.
func TestTransportTreat101Terminal(t *testing.T) {
	run(t, testTransportTreat101Terminal, []testMode{http1Mode})
}
func testTransportTreat101Terminal(t *testing.T, mode testMode) {
	cst := newClientServerTest(t, mode, HandlerFunc(func(w ResponseWriter, r *Request) {
		conn, buf, _ := w.(Hijacker).Hijack()
		buf.Write([]byte("HTTP/1.1 101 Switching Protocols\r\n\r\n"))
		buf.Write([]byte("HTTP/1.1 204 No Content\r\n\r\n"))
		buf.Flush()
		conn.Close()
	}))
	res, err := cst.c.Get(cst.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != StatusSwitchingProtocols {
		t.Errorf("StatusCode = %v; want 101 Switching Protocols", res.StatusCode)
	}
}

func TestTransportTLSHandshakeTimeout(t *testing.T) {
	defer afterTest(t)
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	ln := newLocalListener(t)
	defer ln.Close()
	testdonec := make(chan struct{})
	defer close(testdonec)

	go func() {
		c, err := ln.Accept()
		if err != nil {
			t.Error(err)
			return
		}
		<-testdonec
		c.Close()
	}()

	tr := &Transport{
		Dial: func(_, _ string) (net.Conn, error) {
			return net.Dial("tcp", ln.Addr().String())
		},
		TLSHandshakeTimeout: 250 * time.Millisecond,
	}
	cl := &Client{Transport: tr}
	_, err := cl.Get("https://dummy.tld/")
	if err == nil {
		t.Error("expected error")
		return
	}
	ue, ok := err.(*url.Error)
	if !ok {
		t.Errorf("expected url.Error; got %#v", err)
		return
	}
	ne, ok := ue.Err.(net.Error)
	if !ok {
		t.Errorf("expected net.Error; got %#v", err)
		return
	}
	if !ne.Timeout() {
		t.Errorf("expected timeout error; got %v", err)
	}
	if !strings.Contains(err.Error(), "handshake timeout") {
		t.Errorf("expected 'handshake timeout' in error; got %v", err)
	}
}

// Trying to repro golang.org/issue/3514
func TestTLSServerClosesConnection(t *testing.T) {
	run(t, testTLSServerClosesConnection, []testMode{https1Mode})
}
func testTLSServerClosesConnection(t *testing.T, mode testMode) {
	closedc := make(chan bool, 1)
	ts := newClientServerTest(t, mode, HandlerFunc(func(w ResponseWriter, r *Request) {
		if strings.Contains(r.URL.Path, "keep-alive-then-die") {
			conn, _, _ := w.(Hijacker).Hijack()
			conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 3\r\n\r\nfoo"))
			conn.Close()
			closedc <- true
			return
		}
		fmt.Fprintf(w, "hello")
	})).ts

	c := ts.Client()
	tr := c.Transport.(*Transport)

	var nSuccess = 0
	var errs []error
	const trials = 20
	for i := 0; i < trials; i++ {
		tr.CloseIdleConnections()
		res, err := c.Get(ts.URL + "/keep-alive-then-die")
		if err != nil {
			t.Fatal(err)
		}
		<-closedc
		slurp, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(slurp) != "foo" {
			t.Errorf("Got %q, want foo", slurp)
		}

		// Now try again and see if we successfully
		// pick a new connection.
		res, err = c.Get(ts.URL + "/")
		if err != nil {
			errs = append(errs, err)
			continue
		}
		slurp, err = io.ReadAll(res.Body)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		nSuccess++
	}
	if nSuccess > 0 {
		t.Logf("successes = %d of %d", nSuccess, trials)
	} else {
		t.Errorf("All runs failed:")
	}
	for _, err := range errs {
		t.Logf("  err: %v", err)
	}
}

// logWritesConn is a net.Conn that logs each Write call to writes
// and then proxies to w.
// It proxies Read calls to a reader it receives from rch.
type logWritesConn struct {
	net.Conn // nil. crash on use.

	w io.Writer

	rch <-chan io.Reader
	r   io.Reader // nil until received by rch

	mu     sync.Mutex
	writes []string
}

func (c *logWritesConn) Write(p []byte) (n int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writes = append(c.writes, string(p))
	return c.w.Write(p)
}

func (c *logWritesConn) Read(p []byte) (n int, err error) {
	if c.r == nil {
		c.r = <-c.rch
	}
	return c.r.Read(p)
}

func (c *logWritesConn) Close() error { return nil }

// Issue 6574
func TestTransportFlushesBodyChunks(t *testing.T) {
	defer afterTest(t)
	resBody := make(chan io.Reader, 1)
	connr, connw := io.Pipe() // connection pipe pair
	lw := &logWritesConn{
		rch: resBody,
		w:   connw,
	}
	tr := &Transport{
		Dial: func(network, addr string) (net.Conn, error) {
			return lw, nil
		},
	}
	bodyr, bodyw := io.Pipe() // body pipe pair
	go func() {
		defer bodyw.Close()
		for i := 0; i < 3; i++ {
			fmt.Fprintf(bodyw, "num%d\n", i)
		}
	}()
	resc := make(chan *Response)
	go func() {
		req, _ := NewRequest("POST", "http://localhost:8080", bodyr)
		req.Header.Set("User-Agent", "x") // known value for test
		res, err := tr.RoundTrip(req)
		if err != nil {
			t.Errorf("RoundTrip: %v", err)
			close(resc)
			return
		}
		resc <- res

	}()
	// Fully consume the request before checking the Write log vs. want.
	req, err := ReadRequest(bufio.NewReader(connr))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, req.Body)

	// Unblock the transport's roundTrip goroutine.
	resBody <- strings.NewReader("HTTP/1.1 204 No Content\r\nConnection: close\r\n\r\n")
	res, ok := <-resc
	if !ok {
		return
	}
	defer res.Body.Close()

	want := []string{
		"POST / HTTP/1.1\r\nHost: localhost:8080\r\nUser-Agent: x\r\nTransfer-Encoding: chunked\r\nAccept-Encoding: gzip\r\n\r\n",
		"5\r\nnum0\n\r\n",
		"5\r\nnum1\n\r\n",
		"5\r\nnum2\n\r\n",
		"0\r\n\r\n",
	}
	if !reflect.DeepEqual(lw.writes, want) {
		t.Errorf("Writes differed.\n Got: %q\nWant: %q\n", lw.writes, want)
	}
}

func newLocalListener(t *testing.T) net.Listener {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		ln, err = net.Listen("tcp6", "[::1]:0")
	}
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

func TestTransportResponseBodyWritableOnProtocolSwitch(t *testing.T) {
	run(t, testTransportResponseBodyWritableOnProtocolSwitch, []testMode{http1Mode})
}
func testTransportResponseBodyWritableOnProtocolSwitch(t *testing.T, mode testMode) {
	done := make(chan struct{})
	defer close(done)
	cst := newClientServerTest(t, mode, HandlerFunc(func(w ResponseWriter, r *Request) {
		conn, _, err := w.(Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		io.WriteString(conn, "HTTP/1.1 101 Switching Protocols Hi\r\nConnection: upgRADe\r\nUpgrade: foo\r\n\r\nSome buffered data\n")
		bs := bufio.NewScanner(conn)
		bs.Scan()
		fmt.Fprintf(conn, "%s\n", strings.ToUpper(bs.Text()))
		<-done
	}))

	req, _ := NewRequest("GET", cst.ts.URL, nil)
	req.Header.Set("Upgrade", "foo")
	req.Header.Set("Connection", "upgrade")
	res, err := cst.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 101 {
		t.Fatalf("expected 101 switching protocols; got %v, %v", res.Status, res.Header)
	}
	rwc, ok := res.Body.(io.ReadWriteCloser)
	if !ok {
		t.Fatalf("expected a ReadWriteCloser; got a %T", res.Body)
	}
	defer rwc.Close()
	bs := bufio.NewScanner(rwc)
	if !bs.Scan() {
		t.Fatalf("expected readable input")
	}
	if got, want := bs.Text(), "Some buffered data"; got != want {
		t.Errorf("read %q; want %q", got, want)
	}
	io.WriteString(rwc, "echo\n")
	if !bs.Scan() {
		t.Fatalf("expected another line")
	}
	if got, want := bs.Text(), "ECHO"; got != want {
		t.Errorf("read %q; want %q", got, want)
	}
}
