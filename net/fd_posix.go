package net

import "go_net/internal/poll"

// Network file descriptor.
type netFD struct {
	pfd poll.FD

	// immutable until Close
	family int
	sotype int
	// isConnected bool // handshake completed or use of association with peer
	net string
	// laddr       Addr
	// raddr       Addr
}
