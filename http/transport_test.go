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
	"fmt"
	"io"
	. "net/http"
	"strings"
	"testing"
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
