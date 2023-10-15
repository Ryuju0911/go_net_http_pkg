// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix || windows

package net

import (
	"context"
	"go_net/internal/poll"
	"syscall"
)

// socket returns a network file descriptor that is ready for
// asynchronous I/O using the network poller.
func socket(
	ctx context.Context,
	net string,
	family, sotype, proto int,
	ipv6only bool,
	laddr, raddr sockaddr,
	ctrlCtxFn func(context.Context, string, string, syscall.RawConn) error,
) (fd *netFD, err error) {
	s, err := sysSocket(family, sotype, proto)
	if err != nil {
		return nil, err
	}
	if err = setDefaultSockopts(s, family, sotype, ipv6only); err != nil {
		poll.CloseFunc(s)
		return nil, err
	}
	if fd, err = newFD(s, family, sotype, net); err != nil {
		poll.CloseFunc(s)
		return nil, err
	}

	if laddr != nil && raddr == nil {
		switch sotype {
		case syscall.SOCK_STREAM:
			if err := fd.listenStream(ctx, laddr, listenerBacklog(), ctrlCtxFn); err != nil {
				// fd.Close()
				return nil, err
			}
			return fd, nil
		}
		// TODO: Implement the cases when syscall.SOCK_SEQPACKET or syscall.SOCK_DGRAM.
	}
	// TODO: Implement netFD.dial and call it here.
	return fd, nil
}

func (fd *netFD) listenStream(
	ctx context.Context,
	laddr sockaddr,
	backlog int,
	ctrlCtxFn func(context.Context, string, string, syscall.RawConn) error,
) error {
	var err error
	return err
}
