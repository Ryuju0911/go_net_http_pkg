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
	// TODO: Implement logic.
	var l Listener
	return l, nil
}

func Listen(network, address string) (Listener, error) {
	var lc ListenConfig
	return lc.Listen(context.Background(), network, address)
}
