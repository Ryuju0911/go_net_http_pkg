// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package net

// Addr represents a network end point address.
type Addr interface {
}

// listenerBackLog returns the length of the listen queue, which represents
// the queue to which pending connections are joined.
// If the length limit of this queue is small, the server will reject requests
// that exceed the limit, especially when multiple connection requests arrive simultaneously.
//
// Regardless of the operating system (Linux, macOS, or FreeBSD), the default value of this parameter is 128.
// Therefore, for the sake of simplicity, we always returns 128.
func listenerBacklog() int {
	return 128
}

// A Listener is a generic network listener for stream-oriented protocols.
//
// Multiple goroutines may invoke methods on a Listener simultaneously.
type Listener interface {
}

type AddrError struct {
	Err  string
	Addr string
}

func (e *AddrError) Error() string {
	if e == nil {
		return "<nil>"
	}
	s := e.Err
	if e.Addr != "" {
		s = "address " + e.Addr + ": " + s
	}
	return s
}
