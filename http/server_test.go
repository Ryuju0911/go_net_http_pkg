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

func TestListenAndServe(t *testing.T) {
	addr := "localhost:80"
	handler := new(testHandler)

	server := ListenAndServe(addr, handler)
	if server == nil {
		t.Fatal("failed in creating Server")
	}
}
