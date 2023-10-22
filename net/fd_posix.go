package net

import (
	"internal/poll"
	"runtime"
)

// Network file descriptor.
type netFD struct {
	pfd poll.FD

	// immutable until Close
	family int
	sotype int
	// isConnected bool // handshake completed or use of association with peer
	net   string
	laddr Addr
	raddr Addr
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
