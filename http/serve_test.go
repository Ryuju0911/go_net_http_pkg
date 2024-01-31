// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// End-to-end serving tests

package http_test

import (
	"bufio"
	"fmt"
	"io"
	"net"
	. "net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"testing"
)

func testTCPConnectionCloses(t *testing.T, req string, h Handler) {
	setParallel(t)
	s := newClientServerTest(t, http1Mode, h).ts

	conn, err := net.Dial("tcp", s.Listener.Addr().String())
	if err != nil {
		t.Fatal("dial error:", err)
	}
	defer conn.Close()

	_, err = fmt.Fprint(conn, req)
	if err != nil {
		t.Fatal("print error:", err)
	}

	r := bufio.NewReader(conn)
	res, err := ReadResponse(r, &Request{Method: "GET"})
	if err != nil {
		t.Fatal("ReadResponse error:", err)
	}

	_, err = io.ReadAll(r)
	if err != nil {
		t.Fatal("read error:", err)
	}

	if !res.Close {
		t.Errorf("Response.Close = false; want true")
	}
}

func testTCPConnectionStaysOpen(t *testing.T, req string, handler Handler) {
	setParallel(t)
	ts := newClientServerTest(t, http1Mode, handler).ts
	conn, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	br := bufio.NewReader(conn)
	for i := 0; i < 2; i++ {
		if _, err := io.WriteString(conn, req); err != nil {
			t.Fatal(err)
		}
		res, err := ReadResponse(br, nil)
		if err != nil {
			t.Fatalf("res %d: %v", i+1, err)
		}
		if _, err := io.Copy(io.Discard, res.Body); err != nil {
			t.Fatalf("res %d body copy: %v", i+1, err)
		}
		res.Body.Close()
	}
}

// // TestServeHTTP10Close verifies that HTTP/1.0 requests won't be kept alive.
// func TestServeHTTP10Close(t *testing.T) {
// 	testTCPConnectionCloses(t, "GET / HTTP/1.0\r\n\r\n", HandlerFunc(func(w ResponseWriter, r *Request) {
// 		ServeFile(w, r, "testdata/file")
// 	}))
// }

// TestClientCanClose verifies that clients can also force a connection to close.
func TestClientCanClose(t *testing.T) {
	testTCPConnectionCloses(t, "GET / HTTP/1.1\r\nHost: foo\r\nConnection: close\r\n\r\n", HandlerFunc(func(w ResponseWriter, r *Request) {
		// Nothing.
	}))
}

// TestHandlersCanSetConnectionClose verifies that handlers can force a connection to close,
// even for HTTP/1.1 requests.
func TestHandlersCanSetConnectionClose11(t *testing.T) {
	testTCPConnectionCloses(t, "GET / HTTP/1.1\r\nHost: foo\r\n\r\n\r\n", HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Header().Set("Connection", "close")
	}))
}

func TestHandlersCanSetConnectionClose10(t *testing.T) {
	testTCPConnectionCloses(t, "GET / HTTP/1.0\r\nConnection: keep-alive\r\n\r\n", HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Header().Set("Connection", "close")
	}))
}

func send204(w ResponseWriter, r *Request) { w.WriteHeader(204) }
func send304(w ResponseWriter, r *Request) { w.WriteHeader(304) }

// Issue 15647: 204 responses can't have bodies, so HTTP/1.0 keep-alive conns should stay open.
func TestHTTP10KeepAlive204Response(t *testing.T) {
	testTCPConnectionStaysOpen(t, "GET / HTTP/1.0\r\nConnection: keep-alive\r\n\r\n", HandlerFunc(send204))
}

func TestHTTP11KeepAlive204Response(t *testing.T) {
	testTCPConnectionStaysOpen(t, "GET / HTTP/1.1\r\nHost: foo\r\n\r\n", HandlerFunc(send204))
}

func TestHTTP10KeepAlive304Response(t *testing.T) {
	testTCPConnectionStaysOpen(t,
		"GET / HTTP/1.0\r\nConnection: keep-alive\r\nIf-Modified-Since: Mon, 02 Jan 2006 15:04:05 GMT\r\n\r\n",
		HandlerFunc(send304))
}

type serverExpectTest struct {
	contentLength    int // of request body
	chunked          bool
	expectation      string // e.g. "100-continue"
	readBody         bool   // whether handler should read the body (if false, sends StatusUnauthorized)
	expectedResponse string // expected substring in first line of http response
}

func expectTest(contentLength int, expectation string, readBody bool, expectedResponse string) serverExpectTest {
	return serverExpectTest{
		contentLength:    contentLength,
		expectation:      expectation,
		readBody:         readBody,
		expectedResponse: expectedResponse,
	}
}

var serverExpectTests = []serverExpectTest{
	// Normal 100-continues, case-insensitive.
	expectTest(100, "100-continue", true, "100 Continue"),
	expectTest(100, "100-cOntInUE", true, "100 Continue"),

	// No 100-continue.
	expectTest(100, "", true, "200 OK"),

	// 100-continue but requesting client to deny us,
	// so it never reads the body.
	expectTest(100, "100-continue", false, "401 Unauthorized"),
	// Likewise without 100-continue:
	expectTest(100, "", false, "401 Unauthorized"),

	// Non-standard expectations are failures
	expectTest(0, "a-pony", false, "417 Expectation Failed"),

	// Expect-100 requested but no body (is apparently okay: Issue 7625)
	expectTest(0, "100-continue", true, "200 OK"),
	// Expect-100 requested but handler doesn't read the body
	expectTest(0, "100-continue", false, "401 Unauthorized"),
	// Expect-100 continue with no body, but a chunked body.
	{
		expectation:      "100-continue",
		readBody:         true,
		chunked:          true,
		expectedResponse: "100 Continue",
	},
}

// Tests that the server responds to the "Expect" request header
// correctly.
func TestServerExpect(t *testing.T) { run(t, testServerExpect, []testMode{http1Mode}) }
func testServerExpect(t *testing.T, mode testMode) {
	ts := newClientServerTest(t, mode, HandlerFunc(func(w ResponseWriter, r *Request) {
		// Note using r.FormValue("readbody") because for POST
		// requests that would read from r.Body, which we only
		// conditionally want to do.
		if strings.Contains(r.URL.RawQuery, "readbody=true") {
			io.ReadAll(r.Body)
			w.Write([]byte("Hi"))
		} else {
			w.WriteHeader(StatusUnauthorized)
		}
	})).ts

	runTest := func(test serverExpectTest) {
		conn, err := net.Dial("tcp", ts.Listener.Addr().String())
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		defer conn.Close()

		// Only send the body immediately if we're acting like an HTTP client
		// that doesn't send 100-continue expectations.
		writeBody := test.contentLength != 0 && strings.ToLower(test.expectation) != "100-continue"

		wg := sync.WaitGroup{}
		wg.Add(1)
		defer wg.Wait()

		go func() {
			defer wg.Done()

			contentLen := fmt.Sprintf("Content-Length: %d", test.contentLength)
			if test.chunked {
				contentLen = "Transfer-Encoding: chunked"
			}
			_, err := fmt.Fprintf(conn, "POST /?readbody=%v HTTP/1.1\r\n"+
				"Connection: close\r\n"+
				"%s\r\n"+
				"Expect: %s\r\nHost: foo\r\n\r\n",
				test.readBody, contentLen, test.expectation)
			if err != nil {
				t.Errorf("On test %#v, error writing request headers: %v", test, err)
				return
			}
			if writeBody {
				var targ io.WriteCloser = struct {
					io.Writer
					io.Closer
				}{
					conn,
					io.NopCloser(nil),
				}
				if test.chunked {
					targ = httputil.NewChunkedWriter(conn)
				}
				body := strings.Repeat("A", test.contentLength)
				_, err = fmt.Fprint(targ, body)
				if err == nil {
					err = targ.Close()
				}
				if err != nil {
					if !test.readBody {
						// Server likely already hung up on us.
						// See larger comment below.
						t.Logf("On test %#v, acceptable error writing request body: %v", test, err)
						return
					}
					t.Errorf("On test %#v, error writing request body: %v", test, err)
				}
			}
		}()
		bufr := bufio.NewReader(conn)
		line, err := bufr.ReadString('\n')
		if err != nil {
			if writeBody && !test.readBody {
				// This is an acceptable failure due to a possible TCP race:
				// We were still writing data and the server hung up on us. A TCP
				// implementation may send a RST if our request body data was known
				// to be lost, which may trigger our reads to fail.
				// See RFC 1122 page 88.
				t.Logf("On test %#v, acceptable error from ReadString: %v", test, err)
				return
			}
			t.Fatalf("On test %#v, ReadString: %v", test, err)
		}
		if !strings.Contains(line, test.expectedResponse) {
			t.Errorf("On test %#v, got first line = %q; want %q", test, line, test.expectedResponse)
		}
	}

	for _, test := range serverExpectTests {
		runTest(test)
	}
}
