// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package go_net

import (
	"context"
	"syscall"
)

func internetSocket(
	ctx context.Context,
	net string,
	laddr, raddr sockaddr,
	sotype,
	proto int,
	mode string,
	ctrlCtxFn func(context.Context, string, string, syscall.RawConn) error,
) (fd *netFD, err error) {
	return socket(ctx, net, syscall.AF_INET, sotype, proto, false, laddr, raddr, nil)
}
