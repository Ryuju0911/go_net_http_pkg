// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package net

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
	// if (runtime.GOOS == "aix" || runtime.GOOS == "windows" || runtime.GOOS == "openbsd") && mode == "dial" && raddr.isWildcard() {
	// 	raddr = raddr.toLocal(net)
	// }
	// family, ipv6only := favoriteAddrFamily(net, laddr, raddr, mode)

	// Temporarily hard coded.
	family, ipv6only := syscall.AF_INET6, false
	return socket(ctx, net, family, sotype, proto, ipv6only, laddr, raddr, nil)
}
