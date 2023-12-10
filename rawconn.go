// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package net

import (
	"errors"
	"runtime"
	"syscall"
)

type rawConn struct {
	fd *netFD
}

func (c *rawConn) ok() bool { return c != nil && c.fd != nil }

func (c *rawConn) Control(f func(uintptr)) error {
	if !c.ok() {
		return syscall.EINVAL
	}
	err := c.fd.pfd.RawControl(f)
	runtime.KeepAlive(c.fd)
	if err != nil {
		err = errors.New("raw-control")
	}
	return err
}

func (c *rawConn) Read(f func(uintptr) bool) error {
	if !c.ok() {
		return syscall.EINVAL
	}
	err := c.fd.pfd.RawRead(f)
	runtime.KeepAlive(c.fd)
	if err != nil {
		err = errors.New("raw-read")
	}
	return err
}

func (c *rawConn) Write(f func(uintptr) bool) error {
	if !c.ok() {
		return syscall.EINVAL
	}
	err := c.fd.pfd.RawWrite(f)
	runtime.KeepAlive(c.fd)
	if err != nil {
		err = errors.New("raw-write")
	}
	return err
}

func newRawConn(fd *netFD) *rawConn {
	return &rawConn{fd: fd}
}
