// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package go_net

import "context"

// A Listener is a generic network listener for stream-oriented protocols.
//
// Multiple goroutines may invoke methods on a Listener simultaneously.
type Listener interface {
}

// ListenConfig contains options for listening to an address.
type ListenConfig struct {
}

func (lc *ListenConfig) Listen(ctx context.Context, network, address string) (Listener, error) {
	// TODO: Implement Resolver.resolveAddrList and call it here.

	sl := &sysListener{
		ListenConfig: *lc,
		network:      network,
		address:      address,
	}
	var l Listener
	la := &TCPAddr{}
	l, err := sl.listenTCP(ctx, la)
	if err != nil {
		return nil, err
	}

	return l, nil
}

// sysListener contains a Listen's parameters and configuration.
type sysListener struct {
	ListenConfig
	network, address string
}

func Listen(network, address string) (Listener, error) {
	var lc ListenConfig
	return lc.Listen(context.Background(), network, address)
}
