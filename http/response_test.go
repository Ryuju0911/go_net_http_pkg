// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package http

func dummyReq(method string) *Request {
	return &Request{Method: method}
}

func dummyReq11(method string) *Request {
	return &Request{Method: method, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1}
}
