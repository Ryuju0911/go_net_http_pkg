package net

import (
	"internal/poll"
	"runtime"
	"time"
)

// Network file descriptor.
type netFD struct {
	pfd poll.FD

	// immutable until Close
	family      int
	sotype      int
	isConnected bool // handshake completed or use of association with peer
	net         string
	laddr       Addr
	raddr       Addr
}

func (fd *netFD) setAddr(laddr, raddr Addr) {
	fd.laddr = laddr
	fd.raddr = raddr
	runtime.SetFinalizer(fd, (*netFD).Close)
}

func (fd *netFD) Close() error {
	runtime.SetFinalizer(fd, nil)
	return fd.pfd.Close()
}

func (fd *netFD) Read(p []byte) (n int, err error) {
	n, err = fd.pfd.Read(p)
	runtime.KeepAlive(fd)
	return n, wrapSyscallError(readSyscallName, err)
}

func (fd *netFD) Write(p []byte) (nn int, err error) {
	nn, err = fd.pfd.Write(p)
	runtime.KeepAlive(fd)
	return nn, wrapSyscallError(writeSyscallName, err)
}

func (fd *netFD) SetReadDeadline(t time.Time) error {
	return fd.pfd.SetReadDeadline(t)
}

func (fd *netFD) SetWriteDeadline(t time.Time) error {
	return fd.pfd.SetWriteDeadline(t)
}
