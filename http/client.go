// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// HTTP client. See RFC 7230 through 7235.
//
// This is the high-level Client interface.
// The low-level implementation is in transport.go.

package http

import (
	"errors"
	"net/http/internal/ascii"
	"net/url"
	"time"
)

type Client struct {
	// Transport specifies the mechanism by which individual
	// HTTP requests are made.
	// If nil, DefaultTransport is used.
	Transport RoundTripper

	// Jar specifies the cookie jar.
	//
	// The Jar is used to insert relevant cookies into every
	// outbound Request and is updated with the cookie values
	// of every inbound Response. The Jar is consulted for every
	// redirect that the Client follows.
	//
	// If Jar is nil, cookies are only sent if they are explicitly
	// set on the Request.
	Jar CookieJar

	// Timeout specifies a time limit for requests made by this
	// Client. The timeout includes connection time, any
	// redirects, and reading the response body. The timer remains
	// running after Get, Head, Post, or Do return and will
	// interrupt reading of the Response.Body.
	//
	// A Timeout of zero means no timeout.
	//
	// The Client cancels requests to the underlying Transport
	// as if the Request's Context ended.
	//
	// For compatibility, the Client will also use the deprecated
	// CancelRequest method on Transport if found. New
	// RoundTripper implementations should use the Request's Context
	// for cancellation instead of implementing CancelRequest.
	Timeout time.Duration
}

// DefaultClient is the default Client and is used by Get, Head, and Post.
var DefaultClient = Client{}

// RoundTripper is an interface representing the ability to execute a
// single HTTP transaction, obtaining the Response for a given Request.
//
// A RoundTripper must be safe for concurrent use by multiple
// goroutines.
type RoundTripper interface {
	// RoundTrip executes a single HTTP transaction, returning
	// a Response for the provided Request.
	//
	// RoundTrip should not attempt to interpret the response. In
	// particular, RoundTrip must return err == nil if it obtained
	// a response, regardless of the response's HTTP status code.
	// A non-nil err should be reserved for failure to obtain a
	// response. Similarly, RoundTrip should not attempt to
	// handle higher-level protocol details such as redirects,
	// authentication, or cookies.
	//
	// RoundTrip should not modify the request, except for
	// consuming and closing the Request's Body. RoundTrip may
	// read fields of the request in a separate goroutine. Callers
	// should not mutate or reuse the request until the Response's
	// Body has been closed.
	//
	// RoundTrip must always close the body, including on errors,
	// but depending on the implementation may do so in a separate
	// goroutine even after RoundTrip returns. This means that
	// callers wanting to reuse the body for subsequent requests
	// must arrange to wait for the Close call before doing so.
	//
	// The Request's URL and Header fields must be initialized.
	RoundTrip(*Request) (*Response, error)
}

// didTimeout is non-nil only if err != nil.
func (c *Client) send(req *Request, deadline time.Time) (resp *Response, didTimeout func() bool, err error) {
	if c.Jar != nil {
		for _, cookie := range c.Jar.Cookies(req.URL) {
			req.AddCookie(cookie)
		}
	}
	resp, didTimeout, err = send(req, c.transport(), deadline)
	return
}

func (c *Client) deadline() time.Time {
	if c.Timeout > 0 {
		return time.Now().Add(c.Timeout)
	}
	return time.Time{}
}

func (c *Client) transport() RoundTripper {
	if c.Transport != nil {
		return c.Transport
	}
	return DefaultTransport
}

// send issues an HTTP request.
// Caller should close resp.Body when done reading from it.
func send(ireq *Request, rt RoundTripper, deadline time.Time) (resp *Response, didTimeout func() bool, err error) {
	req := ireq // req is either the original request, or a modified fork

	if rt == nil {
		req.closeBody()
		return nil, alwaysFalse, errors.New("http: no Client.Transport or DefaultTransport")
	}

	if req.URL == nil {
		req.closeBody()
		return nil, alwaysFalse, errors.New("http: nil Request.URL")
	}

	if req.RequestURI != "" {
		req.closeBody()
		return nil, alwaysFalse, errors.New("http: Request.RequestURI can't be set in client requests")
	}

	// forkReq forks req into a shallow clone of ireq the first
	// time it's called.
	forkReq := func() {
		if ireq == req {
			req = new(Request)
			*req = *ireq // shallow clone
		}
	}

	// TODO: Check authentication

	if !deadline.IsZero() {
		forkReq()
	}
	// stopTimer, didTimeout := setRequestCancel(req, rt, deadline)

	resp, err = rt.RoundTrip(req)
	return
}

// Get issues a GET to the specified URL. If the response is one of
// the following redirect codes, Get follows the redirect, up to a
// maximum of 10 redirects:
//
//	301 (Moved Permanently)
//	302 (Found)
//	303 (See Other)
//	307 (Temporary Redirect)
//	308 (Permanent Redirect)
//
// An error is returned if there were too many redirects or if there
// was an HTTP protocol error. A non-2xx response doesn't cause an
// error. Any returned error will be of type *url.Error. The url.Error
// value's Timeout method will report true if the request timed out.
//
// When err is nil, resp always contains a non-nil resp.Body.
// Caller should close resp.Body when done reading from it.
//
// Get is a wrapper around DefaultClient.Get.
//
// To make a request with custom headers, use NewRequest and
// DefaultClient.Do.
//
// To make a request with a specified context.Context, use NewRequestWithContext
// and DefaultClient.Do.
func Get(url string) (resp *Response, err error) {
	return DefaultClient.Get(url)
}

// Get issues a GET to the specified URL. If the response is one of
// the following redirect codes, Get follows the redirect, up to a
// maximum of 10 redirects:
//
//	301 (Moved Permanently)
//	302 (Found)
//	303 (See Other)
//	307 (Temporary Redirect)
//	308 (Permanent Redirect)
//
// An error is returned if there were too many redirects or if there
// was an HTTP protocol error. A non-2xx response doesn't cause an
// error. Any returned error will be of type *url.Error. The url.Error
// value's Timeout method will report true if the request timed out.
//
// When err is nil, resp always contains a non-nil resp.Body.
// Caller should close resp.Body when done reading from it.
//
// Get is a wrapper around DefaultClient.Get.
//
// To make a request with custom headers, use NewRequest and
// DefaultClient.Do.
//
// To make a request with a specified context.Context, use NewRequestWithContext
// and DefaultClient.Do.
func (c *Client) Get(url string) (resp *Response, err error) {
	req, err := NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

func alwaysFalse() bool { return false }

func urlErrorOp(method string) string {
	if method == "" {
		return "Get"
	}
	if lowerMethod, ok := ascii.ToLower(method); ok {
		return method[:1] + lowerMethod[1:]
	}
	return method
}

// Do sends an HTTP request and returns an HTTP response, following
// policy (such as redirects, cookies, auth) as configured on the
// client.
//
// An error is returned if caused by client policy (such as
// CheckRedirect), or failure to speak HTTP (such as a network
// connectivity problem). A non-2xx status code doesn't cause an
// error.
//
// If the returned error is nil, the Response will contain a non-nil
// Body which the user is expected to close. If the Body is not both
// read to EOF and closed, the Client's underlying RoundTripper
// (typically Transport) may not be able to re-use a persistent TCP
// connection to the server for a subsequent "keep-alive" request.
//
// The request Body, if non-nil, will be closed by the underlying
// Transport, even on errors. The Body may be closed asynchronously after
// Do returns.
//
// On error, any Response can be ignored. A non-nil Response with a
// non-nil error only occurs when CheckRedirect fails, and even then
// the returned Response.Body is already closed.
//
// Generally Get, Post, or PostForm will be used instead of Do.
//
// If the server replies with a redirect, the Client first uses the
// CheckRedirect function to determine whether the redirect should be
// followed. If permitted, a 301, 302, or 303 redirect causes
// subsequent requests to use HTTP method GET
// (or HEAD if the original request was HEAD), with no body.
// A 307 or 308 redirect preserves the original HTTP method and body,
// provided that the Request.GetBody function is defined.
// The NewRequest function automatically sets GetBody for common
// standard library body types.
//
// Any returned error will be of type *url.Error. The url.Error
// value's Timeout method will report true if the request timed out.
func (c *Client) Do(req *Request) (*Response, error) {
	return c.do(req)
}

func (c *Client) do(req *Request) (retres *Response, reterr error) {
	if req.URL == nil {
		req.closeBody()
		return nil, &url.Error{
			Op:  urlErrorOp(req.Method),
			Err: errors.New("http: nil Request.URL"),
		}
	}

	var (
		deadline = c.deadline()
		reqs     []*Request
		resp     *Response
		// copyHeaders   = c.makeHeadersCopier(req)
		// reqBodyClosed = false // have we closed the current req.Body?

		// // Redirect behavior:
		// redirectMethod string
		// includeBody    bool
	)
	for {
		// For all but the first request, create the next
		// request hop and replace req.
		if len(reqs) > 0 {
			loc := resp.Header.Get("Location")
			if loc == "" {
				// While most 3xx responses include a Location, it is not
				// required and 3xx responses without a Location have been
				// observed in the wild. See issues #17773 and #49281.
				return resp, nil
			}
			// TODO: Handles the case when loc != nil.
		}
		reqs = append(reqs, req)
		var err error
		// var didTimeout func() bool
		if resp, _, err = c.send(req, deadline); err != nil {
			// TOOD: Implement logic
			return nil, err
		}
	}
}