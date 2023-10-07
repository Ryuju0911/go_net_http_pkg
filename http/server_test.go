// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Server unit tests

package http

import (
	"testing"
)

type testHandler struct {
}

func (h *testHandler) ServeHTTP(w ResponseWriter, r *Request) {}

func TestHandleFunc(t *testing.T) {
	pattern := "/test"
	handler := func(w ResponseWriter, r *Request) {}

	HandleFunc(pattern, handler)
	if _, ok := DefaultServeMux.m[pattern]; !ok {
		t.Errorf("The specified URL: %s is not registered.", pattern)
	}
}

func TestListenAndServe(t *testing.T) {
	addr := "localhost:80"
	handler := new(testHandler)

	err := ListenAndServe(addr, handler)
	if err != nil {
		t.Fatal(err)
	}
}
