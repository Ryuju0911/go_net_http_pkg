// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package net

import (
	"context"
	"syscall"
)

func sockaddrToTCP(sa syscall.Sockaddr) Addr {
	switch sa := sa.(type) {
	case *syscall.SockaddrInet6:
		return &TCPAddr{IP: sa.Addr[0:], Port: sa.Port}
	}
	return nil
}

func (a *TCPAddr) sockaddr(family int) (syscall.Sockaddr, error) {
	if a == nil {
		return nil, nil
	}
	return ipToSockaddr(family, a.IP, a.Port, a.Zone)
}

func (ln *TCPListener) ok() bool {
	return ln != nil && ln.fd != nil
}

func (ln *TCPListener) accept() (*TCPConn, error) {
	fd, err := ln.fd.accept()
	if err != nil {
		return nil, err
	}
	return newTCPConn(fd, ln.lc.KeepAlive, nil), nil
}

func (ln *TCPListener) close() error {
	return ln.fd.Close()
}

func (sl *sysListener) listenTCP(ctx context.Context, laddr *TCPAddr) (*TCPListener, error) {
	// TODO: Implement logic (create a context and sockets).
	fd, err := internetSocket(ctx, sl.network, laddr, nil, syscall.SOCK_STREAM, 0, "listen", nil)
	if err != nil {
		return nil, err
	}
	return &TCPListener{fd: fd, lc: sl.ListenConfig}, nil
}
