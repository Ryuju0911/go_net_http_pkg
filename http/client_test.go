// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Tests for client.go

package http_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	. "net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var robotsTxtHandler = HandlerFunc(func(w ResponseWriter, r *Request) {
	w.Header().Set("Last-Modified", "sometime")
	fmt.Fprintf(w, "User-agent: go\nDisallow: /something/")
})

// pedanticReadAll works like io.ReadAll but additionally
// verifies that r obeys the documented io.Reader contract.
func pedanticReadAll(r io.Reader) (b []byte, err error) {
	var bufa [64]byte
	buf := bufa[:]
	for {
		n, err := r.Read(buf)
		if n == 0 && err == nil {
			return nil, fmt.Errorf("Read: n=0 with err=nil")
		}
		b = append(b, buf[:n]...)
		if err == io.EOF {
			n, err := r.Read(buf)
			if n != 0 || err != io.EOF {
				return nil, fmt.Errorf("Read: n=%d err=%#v after EOF", n, err)
			}
			return b, nil
		}
		if err != nil {
			return b, err
		}
	}
}

func TestClient(t *testing.T) { run(t, testClient) }

func testClient(t *testing.T, mode testMode) {
	ts := newClientServerTest(t, mode, robotsTxtHandler).ts

	c := ts.Client()
	r, err := c.Get(ts.URL)
	var b []byte
	if err == nil {
		b, err = pedanticReadAll(r.Body)
		r.Body.Close()
	}
	if err != nil {
		t.Error(err)
	} else if s := string(b); !strings.HasPrefix(s, "User-agent:") {
		t.Errorf("Incorrect page body (did not begin with User-agent): %q", s)
	}
}

type recordingTransport struct {
	req *Request
}

func (t *recordingTransport) RoundTrip(req *Request) (resp *Response, err error) {
	t.req = req
	return nil, errors.New("dummy impl")
}

func TestGetRequestFormat(t *testing.T) {
	setParallel(t)
	defer afterTest(t)
	tr := &recordingTransport{}
	client := &Client{Transport: tr}
	url := "http://dummy.faketld/"
	client.Get(url) // Note: doesn't hit network
	if tr.req.Method != "GET" {
		t.Errorf("expected method %q; got %q", "GET", tr.req.Method)
	}
	if tr.req.URL.String() != url {
		t.Errorf("expected URL %q; got %q", url, tr.req.URL.String())
	}
	if tr.req.Header == nil {
		t.Errorf("expected non-nil request Header")
	}
}

func TestClientRedirects(t *testing.T) { run(t, testClientRedirects) }
func testClientRedirects(t *testing.T, mode testMode) {
	var ts *httptest.Server
	ts = newClientServerTest(t, mode, HandlerFunc(func(w ResponseWriter, r *Request) {
		n, _ := strconv.Atoi(r.FormValue("n"))
		// Test Referer header. (7 is arbitrary position to test at)
		if n == 7 {
			if g, e := r.Referer(), ts.URL+"/?n=6"; e != g {
				t.Errorf("on request ?n=7, expected referer of %q; got %q", e, g)
			}
		}
		if n < 15 {
			Redirect(w, r, fmt.Sprintf("/?n=%d", n+1), StatusTemporaryRedirect)
			return
		}
		fmt.Fprintf(w, "n=%d", n)
	})).ts

	c := ts.Client()
	_, err := c.Get(ts.URL)
	if e, g := `Get "/?n=10": stopped after 10 redirects`, fmt.Sprintf("%v", err); e != g {
		t.Errorf("with default client Get, expected error %q, got %q", e, g)
	}

	// HEAD request should also have the ability to follow redirects.
	_, err = c.Head(ts.URL)
	if e, g := `Head "/?n=10": stopped after 10 redirects`, fmt.Sprintf("%v", err); e != g {
		t.Errorf("with default client Head, expected error %q, got %q", e, g)
	}

	// Do should also follow redirects.
	greq, _ := NewRequest("GET", ts.URL, nil)
	_, err = c.Do(greq)
	if e, g := `Get "/?n=10": stopped after 10 redirects`, fmt.Sprintf("%v", err); e != g {
		t.Errorf("with default client Do, expected error %q, got %q", e, g)
	}

	// Requests with an empty Method should also redirect (Issue 12705)
	greq.Method = ""
	_, err = c.Do(greq)
	if e, g := `Get "/?n=10": stopped after 10 redirects`, fmt.Sprintf("%v", err); e != g {
		t.Errorf("with default client Do and empty Method, expected error %q, got %q", e, g)
	}

	var checkErr error
	var lastVia []*Request
	var lastReq *Request
	c.CheckRedirect = func(req *Request, via []*Request) error {
		lastReq = req
		lastVia = via
		return checkErr
	}
	res, err := c.Get(ts.URL)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	res.Body.Close()
	finalURL := res.Request.URL.String()
	if e, g := "<nil>", fmt.Sprintf("%v", err); e != g {
		t.Errorf("with custom client, expected error %q, got %q", e, g)
	}
	if !strings.HasSuffix(finalURL, "/?n=15") {
		t.Errorf("expected final url to end in /?n=15; got url %q", finalURL)
	}
	if e, g := 15, len(lastVia); e != g {
		t.Errorf("expected lastVia to have contained %d elements; got %d", e, g)
	}

	// Test that Request.Cancel is propagated between requests (Issue 14053)
	creq, _ := NewRequest("HEAD", ts.URL, nil)
	cancel := make(chan struct{})
	creq.Cancel = cancel
	if _, err := c.Do(creq); err != nil {
		t.Fatal(err)
	}
	if lastReq == nil {
		t.Fatal("didn't see redirect")
	}
	if lastReq.Cancel != cancel {
		t.Errorf("expected lastReq to have the cancel channel set on the initial req")
	}

	checkErr = errors.New("no redirects allowed")
	res, err = c.Get(ts.URL)
	if urlError, ok := err.(*url.Error); !ok || urlError.Err != checkErr {
		t.Errorf("with redirects forbidden, expected a *url.Error with our 'no redirects allowed' error inside; got %#v (%q)", err, err)
	}
	if res == nil {
		t.Fatalf("Expected a non-nil Response on CheckRedirect failure (https://golang.org/issue/3795)")
	}
	res.Body.Close()
	if res.Header.Get("Location") == "" {
		t.Errorf("no Location header in Response")
	}
}

// Tests that Client redirects' contexts are derived from the original request's context.
func TestClientRedirectsContext(t *testing.T) { run(t, testClientRedirectsContext) }
func testClientRedirectsContext(t *testing.T, mode testMode) {
	ts := newClientServerTest(t, mode, HandlerFunc(func(w ResponseWriter, r *Request) {
		Redirect(w, r, "/", StatusTemporaryRedirect)
	})).ts

	ctx, cancel := context.WithCancel(context.Background())
	c := ts.Client()
	c.CheckRedirect = func(req *Request, via []*Request) error {
		cancel()
		select {
		case <-req.Context().Done():
			return nil
		case <-time.After(5 * time.Second):
			return errors.New("redirected request's context never expired after root request canceled")
		}
	}
	req, _ := NewRequestWithContext(ctx, "GET", ts.URL, nil)
	_, err := c.Do(req)
	ue, ok := err.(*url.Error)
	if !ok {
		t.Fatalf("got error %T; want *url.Error", err)
	}
	if ue.Err != context.Canceled {
		t.Errorf("url.Error.Err = %v; want %v", ue.Err, context.Canceled)
	}
}

type redirectTest struct {
	suffix       string
	want         int // response code
	redirectBody string
}

func TestPostRedirects(t *testing.T) {
	postRedirectTests := []redirectTest{
		{"/", 200, "first"},
		{"/?code=301&next=302", 200, "c301"},
		{"/?code=302&next=302", 200, "c302"},
		{"/?code=303&next=301", 200, "c303wc301"}, // Issue 9348
		{"/?code=304", 304, "c304"},
		{"/?code=305", 305, "c305"},
		{"/?code=307&next=303,308,302", 200, "c307"},
		{"/?code=308&next=302,301", 200, "c308"},
		{"/?code=404", 404, "c404"},
	}

	wantSegments := []string{
		`POST / "first"`,
		`POST /?code=301&next=302 "c301"`,
		`GET /?code=302 ""`,
		`GET / ""`,
		`POST /?code=302&next=302 "c302"`,
		`GET /?code=302 ""`,
		`GET / ""`,
		`POST /?code=303&next=301 "c303wc301"`,
		`GET /?code=301 ""`,
		`GET / ""`,
		`POST /?code=304 "c304"`,
		`POST /?code=305 "c305"`,
		`POST /?code=307&next=303,308,302 "c307"`,
		`POST /?code=303&next=308,302 "c307"`,
		`GET /?code=308&next=302 ""`,
		`GET /?code=302 "c307"`,
		`GET / ""`,
		`POST /?code=308&next=302,301 "c308"`,
		`POST /?code=302&next=301 "c308"`,
		`GET /?code=301 ""`,
		`GET / ""`,
		`POST /?code=404 "c404"`,
	}
	want := strings.Join(wantSegments, "\n")
	run(t, func(t *testing.T, mode testMode) {
		testRedirectsByMethod(t, mode, "POST", postRedirectTests, want)
	})
}

func TestDeleteRedirects(t *testing.T) {
	deleteRedirectTests := []redirectTest{
		{"/", 200, "first"},
		{"/?code=301&next=302,308", 200, "c301"},
		{"/?code=302&next=302", 200, "c302"},
		{"/?code=303", 200, "c303"},
		{"/?code=307&next=301,308,303,302,304", 304, "c307"},
		{"/?code=308&next=307", 200, "c308"},
		{"/?code=404", 404, "c404"},
	}

	wantSegments := []string{
		`DELETE / "first"`,
		`DELETE /?code=301&next=302,308 "c301"`,
		`GET /?code=302&next=308 ""`,
		`GET /?code=308 ""`,
		`GET / "c301"`,
		`DELETE /?code=302&next=302 "c302"`,
		`GET /?code=302 ""`,
		`GET / ""`,
		`DELETE /?code=303 "c303"`,
		`GET / ""`,
		`DELETE /?code=307&next=301,308,303,302,304 "c307"`,
		`DELETE /?code=301&next=308,303,302,304 "c307"`,
		`GET /?code=308&next=303,302,304 ""`,
		`GET /?code=303&next=302,304 "c307"`,
		`GET /?code=302&next=304 ""`,
		`GET /?code=304 ""`,
		`DELETE /?code=308&next=307 "c308"`,
		`DELETE /?code=307 "c308"`,
		`DELETE / "c308"`,
		`DELETE /?code=404 "c404"`,
	}
	want := strings.Join(wantSegments, "\n")
	run(t, func(t *testing.T, mode testMode) {
		testRedirectsByMethod(t, mode, "DELETE", deleteRedirectTests, want)
	})
}

func testRedirectsByMethod(t *testing.T, mode testMode, method string, table []redirectTest, want string) {
	var log struct {
		sync.Mutex
		bytes.Buffer
	}
	var ts *httptest.Server
	ts = newClientServerTest(t, mode, HandlerFunc(func(w ResponseWriter, r *Request) {
		log.Lock()
		slurp, _ := io.ReadAll(r.Body)
		fmt.Fprintf(&log.Buffer, "%s %s %q", r.Method, r.RequestURI, slurp)
		if cl := r.Header.Get("Content-Length"); r.Method == "GET" && len(slurp) == 0 && (r.ContentLength != 0 || cl != "") {
			fmt.Fprintf(&log.Buffer, " (but with body=%T, content-length = %v, %q)", r.Body, r.ContentLength, cl)
		}
		log.WriteByte('\n')
		log.Unlock()
		urlQuery := r.URL.Query()
		if v := urlQuery.Get("code"); v != "" {
			location := ts.URL
			if final := urlQuery.Get("next"); final != "" {
				first, rest, _ := strings.Cut(final, ",")
				location = fmt.Sprintf("%s?code=%s", location, first)
				if rest != "" {
					location = fmt.Sprintf("%s&next=%s", location, rest)
				}
			}
			code, _ := strconv.Atoi(v)
			if code/100 == 3 {
				w.Header().Set("Location", location)
			}
			w.WriteHeader(code)
		}
	})).ts

	c := ts.Client()
	for _, tt := range table {
		content := tt.redirectBody
		req, _ := NewRequest(method, ts.URL+tt.suffix, strings.NewReader(content))
		req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(content)), nil }
		res, err := c.Do(req)

		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != tt.want {
			t.Errorf("POST %s: status code = %d; want %d", tt.suffix, res.StatusCode, tt.want)
		}
	}
	log.Lock()
	got := log.String()
	log.Unlock()

	got = strings.TrimSpace(got)
	want = strings.TrimSpace(want)

	if got != want {
		got, want, lines := removeCommonLines(got, want)
		t.Errorf("Log differs after %d common lines.\n\nGot:\n%s\n\nWant:\n%s\n", lines, got, want)
	}
}

func removeCommonLines(a, b string) (asuffix, bsuffix string, commonLines int) {
	for {
		nl := strings.IndexByte(a, '\n')
		if nl < 0 {
			return a, b, commonLines
		}
		line := a[:nl+1]
		if !strings.HasPrefix(b, line) {
			return a, b, commonLines
		}
		commonLines++
		a = a[len(line):]
		b = b[len(line):]
	}
}

func TestClientRedirectUseResponse(t *testing.T) { run(t, testClientRedirectUseResponse) }
func testClientRedirectUseResponse(t *testing.T, mode testMode) {
	const body = "Hello, world."
	var ts *httptest.Server
	ts = newClientServerTest(t, mode, HandlerFunc(func(w ResponseWriter, r *Request) {
		if strings.Contains(r.URL.Path, "/other") {
			io.WriteString(w, "wrong body")
		} else {
			w.Header().Set("Location", ts.URL+"/other")
			w.WriteHeader(StatusFound)
			io.WriteString(w, body)
		}
	})).ts

	c := ts.Client()
	c.CheckRedirect = func(req *Request, via []*Request) error {
		if req.Response == nil {
			t.Error("expected non-nil Request.Response")
		}
		return ErrUseLastResponse
	}
	res, err := c.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != StatusFound {
		t.Errorf("status = %d; want %d", res.StatusCode, StatusFound)
	}
	defer res.Body.Close()
	slurp, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(slurp) != body {
		t.Errorf("body = %q; want %q", slurp, body)
	}
}

// Issues 17773 and 49281: don't follow a 3xx if the response doesn't
// have a Location header.
func TestClientRedirectNoLocation(t *testing.T) { run(t, testClientRedirectNoLocation) }
func testClientRedirectNoLocation(t *testing.T, mode testMode) {
	for _, code := range []int{301, 308} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			setParallel(t)
			cst := newClientServerTest(t, mode, HandlerFunc(func(w ResponseWriter, r *Request) {
				w.Header().Set("Foo", "Bar")
				w.WriteHeader(code)
			}))
			res, err := cst.c.Get(cst.ts.URL)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != code {
				t.Errorf("status = %d; want %d", res.StatusCode, code)
			}
			if got := res.Header.Get("Foo"); got != "Bar" {
				t.Errorf("Foo header = %q; want Bar", got)
			}
		})
	}
}

// Don't follow a 307/308 if we can't resent the request body.
func TestClientRedirect308NoGetBody(t *testing.T) { run(t, testClientRedirect308NoGetBody) }
func testClientRedirect308NoGetBody(t *testing.T, mode testMode) {
	const fakeURL = "https://localhost:1234" // won't be hit
	ts := newClientServerTest(t, mode, HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Header().Set("Location", fakeURL)
		w.WriteHeader(308)
	})).ts
	req, err := NewRequest("POST", ts.URL, strings.NewReader("some body"))
	if err != nil {
		t.Fatal(err)
	}
	c := ts.Client()
	req.GetBody = nil // so it can't rewind.
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 308 {
		t.Errorf("status = %d; want %d", res.StatusCode, 308)
	}
	if got := res.Header.Get("Location"); got != fakeURL {
		t.Errorf("Location header = %q; want %q", got, fakeURL)
	}
}

func TestEmptyPasswordAuth(t *testing.T) { run(t, testEmptyPasswordAuth) }
func testEmptyPasswordAuth(t *testing.T, mode testMode) {
	gopher := "gopher"
	ts := newClientServerTest(t, mode, HandlerFunc(func(w ResponseWriter, r *Request) {
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Basic") {
			encoded := auth[6:]
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				t.Fatal(err)
			}
			expected := gopher + ":"
			s := string(decoded)
			if expected != s {
				t.Errorf("Invalid Authorization header. Got %q, wanted %q", s, expected)
			}
		} else {
			t.Errorf("Invalid auth %q", auth)
		}
	})).ts
	defer ts.Close()
	req, err := NewRequest("GET", ts.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.URL.User = url.User(gopher)
	c := ts.Client()
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
}

func TestBasicAuth(t *testing.T) {
	defer afterTest(t)
	tr := &recordingTransport{}
	client := &Client{Transport: tr}

	url := "http://My%20User:My%20Pass@dummy.faketld/"
	expected := "My User:My Pass"
	client.Get(url)

	if tr.req.Method != "GET" {
		t.Errorf("got method %q, want %q", tr.req.Method, "GET")
	}
	if tr.req.URL.String() != url {
		t.Errorf("got URL %q, want %q", tr.req.URL.String(), url)
	}
	if tr.req.Header == nil {
		t.Fatalf("expected non-nil request Header")
	}
	auth := tr.req.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Basic ") {
		encoded := auth[6:]
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		s := string(decoded)
		if expected != s {
			t.Errorf("Invalid Authorization header. Got %q, wanted %q", s, expected)
		}
	} else {
		t.Errorf("Invalid auth %q", auth)
	}
}

func TestBasicAuthHeadersPreserved(t *testing.T) {
	defer afterTest(t)
	tr := &recordingTransport{}
	client := &Client{Transport: tr}

	// If Authorization header is provided, username in URL should not override it
	url := "http://My%20User@dummy.faketld/"
	req, err := NewRequest("GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth("My User", "My Pass")
	expected := "My User:My Pass"
	client.Do(req)

	if tr.req.Method != "GET" {
		t.Errorf("got method %q, want %q", tr.req.Method, "GET")
	}
	if tr.req.URL.String() != url {
		t.Errorf("got URL %q, want %q", tr.req.URL.String(), url)
	}
	if tr.req.Header == nil {
		t.Fatalf("expected non-nil request Header")
	}
	auth := tr.req.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Basic ") {
		encoded := auth[6:]
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		s := string(decoded)
		if expected != s {
			t.Errorf("Invalid Authorization header. Got %q, wanted %q", s, expected)
		}
	} else {
		t.Errorf("Invalid auth %q", auth)
	}
}

func TestStripPasswordFromError(t *testing.T) {
	client := &Client{Transport: &recordingTransport{}}
	testCases := []struct {
		desc string
		in   string
		out  string
	}{
		{
			desc: "Strip password from error message",
			in:   "http://user:password@dummy.faketld/",
			out:  `Get "http://user:***@dummy.faketld/": dummy impl`,
		},
		{
			desc: "Don't Strip password from domain name",
			in:   "http://user:password@password.faketld/",
			out:  `Get "http://user:***@password.faketld/": dummy impl`,
		},
		{
			desc: "Don't Strip password from path",
			in:   "http://user:password@dummy.faketld/password",
			out:  `Get "http://user:***@dummy.faketld/password": dummy impl`,
		},
		{
			desc: "Strip escaped password",
			in:   "http://user:pa%2Fssword@dummy.faketld/",
			out:  `Get "http://user:***@dummy.faketld/": dummy impl`,
		},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			_, err := client.Get(tC.in)
			if err.Error() != tC.out {
				t.Errorf("Unexpected output for %q: expected %q, actual %q",
					tC.in, tC.out, err.Error())
			}
		})
	}
}
