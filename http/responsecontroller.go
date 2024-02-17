// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package http

import (
	"bufio"
	"fmt"
	"net"
)

// A ResponseController is used by an HTTP handler to control the response.
//
// A ResponseController may not be used after the [Handler.ServeHTTP] method has returned.
type ResponseController struct {
	rw ResponseWriter
}

// NewResponseController creates a [ResponseController] for a request.
//
// The ResponseWriter should be the original value passed to the [Handler.ServeHTTP] method,
// or have an Unwrap method returning the original ResponseWriter.
//
// If the ResponseWriter implements any of the following methods, the ResponseController
// will call them as appropriate:
//
//	Flush()
//	FlushError() error // alternative Flush returning an error
//	Hijack() (net.Conn, *bufio.ReadWriter, error)
//	SetReadDeadline(deadline time.Time) error
//	SetWriteDeadline(deadline time.Time) error
//	EnableFullDuplex() error
//
// If the ResponseWriter does not support a method, ResponseController returns
// an error matching [ErrNotSupported].
func NewResponseController(rw ResponseWriter) *ResponseController {
	return &ResponseController{rw}
}

type rwUnwrapper interface {
	Unwrap() ResponseWriter
}

// Flush flushes buffered data to the client.
func (c *ResponseController) Flush() error {
	rw := c.rw
	for {
		switch t := rw.(type) {
		case interface{ FlushError() error }:
			return t.FlushError()
		case Flusher:
			t.Flush()
			return nil
		case rwUnwrapper:
			rw = t.Unwrap()
		default:
			return errNotSupported()
		}
	}
}

// Hijack lets the caller take over the connection.
// See the Hijacker interface for details.
func (c *ResponseController) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	rw := c.rw
	for {
		switch t := rw.(type) {
		case Hijacker:
			return t.Hijack()
		case rwUnwrapper:
			rw = t.Unwrap()
		default:
			return nil, nil, errNotSupported()
		}
	}
}

// errNotSupported returns an error that Is ErrNotSupported,
// but is not == to it.
func errNotSupported() error {
	return fmt.Errorf("%w", ErrNotSupported)
}
