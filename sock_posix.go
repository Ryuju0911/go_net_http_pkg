// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix || windows

package go_net

import (
	"context"
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
	if fd, err = newFD(s, family, sotype, net); err != nil {
		// poll.CloseFunc(s)
		return nil, err
	}

	return fd, nil
}
