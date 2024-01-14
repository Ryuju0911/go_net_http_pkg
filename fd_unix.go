// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package net

import (
	"context"
	"internal/poll"
	"os"
	"runtime"
	"syscall"
)

const (
	readSyscallName     = "read"
	readFromSyscallName = "recvfrom"
	readMsgSyscallName  = "recvmsg"
	writeSyscallName    = "write"
	writeToSyscallName  = "sendto"
	writeMsgSyscallName = "sendmsg"
)

func newFD(sysfd, family, sotype int, net string) (*netFD, error) {
	ret := &netFD{
		pfd: poll.FD{
			Sysfd:         sysfd,
			IsStream:      sotype == syscall.SOCK_STREAM,
			ZeroReadIsEOF: sotype != syscall.SOCK_DGRAM && sotype != syscall.SOCK_RAW,
		},
		family: family,
		sotype: sotype,
		net:    net,
	}
	return ret, nil
}

func (fd *netFD) init() error {
	return fd.pfd.Init(fd.net, true)
}

func (fd *netFD) connect(ctx context.Context, la, ra syscall.Sockaddr) (rsa syscall.Sockaddr, ret error) {
	// Do not need to call fd.writeLock here,
	// because fd is not yet accessible to user,
	// so no concurrent operations are possible.
	switch err := connectFunc(fd.pfd.Sysfd, ra); err {
	case syscall.EINPROGRESS, syscall.EALREADY, syscall.EINTR:
	case nil, syscall.EISCONN:
		select {
		case <-ctx.Done():
			return nil, mapErr(ctx.Err())
		default:
		}
		if err := fd.pfd.Init(fd.net, true); err != nil {
			return nil, err
		}
		runtime.KeepAlive(fd)
		return nil, nil
	case syscall.EINVAL:
		// On Solaris and illumos we can see EINVAL if the socket has
		// already been accepted and closed by the server.  Treat this
		// as a successful connection--writes to the socket will see
		// EOF.  For details and a test case in C see
		// https://golang.org/issue/6828.
		if runtime.GOOS == "solaris" || runtime.GOOS == "illumos" {
			return nil, nil
		}
		fallthrough
	default:
		return nil, os.NewSyscallError("connect", err)
	}
	if err := fd.pfd.Init(fd.net, true); err != nil {
		return nil, err
	}
	if deadline, hasDeadline := ctx.Deadline(); hasDeadline {
		fd.pfd.SetWriteDeadline(deadline)
		defer fd.pfd.SetWriteDeadline(noDeadline)
	}

	// Start the "interrupter" goroutine, if this context might be canceled.
	//
	// The interrupter goroutine waits for the context to be done and
	// interrupts the dial (by altering the fd's write deadline, which
	// wakes up waitWrite).
	ctxDone := ctx.Done()
	if ctxDone != nil {
		// Wait for the interrupter goroutine to exit before returning
		// from connect.
		done := make(chan struct{})
		interruptRes := make(chan error)
		defer func() {
			close(done)
			if ctxErr := <-interruptRes; ctxErr != nil && ret == nil {
				ret = mapErr(ctxErr)
				fd.Close() // prevent a leak
			}
		}()
		go func() {
			select {
			case <-ctxDone:
				fd.pfd.SetWriteDeadline(aLongTimeAgo)
				// testHookCancelDial()
				interruptRes <- ctx.Err()
			case <-done:
				interruptRes <- nil
			}
		}()
	}

	for {
		if err := fd.pfd.WaitWrite(); err != nil {
			select {
			case <-ctxDone:
				return nil, mapErr(ctx.Err())
			default:
			}
			return nil, err
		}
		nerr, err := getsockoptIntFunc(fd.pfd.Sysfd, syscall.SOL_SOCKET, syscall.SO_ERROR)
		if err != nil {
			return nil, os.NewSyscallError("getsockopt", err)
		}
		switch err := syscall.Errno(nerr); err {
		case syscall.EINPROGRESS, syscall.EALREADY, syscall.EINTR:
		case syscall.EISCONN:
			return nil, nil
		case syscall.Errno(0):
			// The runtime poller can wake us up spuriously;
			// see issues 14548 and 19289. Check that we are
			// really connected; if not, wait again.
			if rsa, err := syscall.Getpeername(fd.pfd.Sysfd); err == nil {
				return rsa, nil
			}
		default:
			return nil, os.NewSyscallError("connect", err)
		}
		runtime.KeepAlive(fd)
	}
}

func (fd *netFD) accept() (netfd *netFD, err error) {
	d, rsa, errcall, err := fd.pfd.Accept()
	if err != nil {
		if errcall != "" {
			err = wrapSyscallError(errcall, err)
		}
		return nil, err
	}

	if netfd, err = newFD(d, fd.family, fd.sotype, fd.net); err != nil {
		poll.CloseFunc(d)
		return nil, err
	}
	if err = netfd.init(); err != nil {
		return nil, err
	}
	lsa, _ := syscall.Getsockname(netfd.pfd.Sysfd)
	netfd.setAddr(netfd.addrFunc()(lsa), netfd.addrFunc()(rsa))
	return netfd, nil
}
