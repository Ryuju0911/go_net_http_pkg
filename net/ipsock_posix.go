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

func ipToSockaddrInet6(ip IP, port int, zone string) (syscall.SockaddrInet6, error) {
	// TODO: Implement logic
	ip6 := ip.To16()
	if ip6 == nil {
		// TODO: Implement ip.String and call it here.
		return syscall.SockaddrInet6{}, &AddrError{Err: "non-IPv6 address", Addr: ""}
	}
	sa := syscall.SockaddrInet6{Port: port}
	copy(sa.Addr[:], ip6)
	return sa, nil
}

func ipToSockaddr(family int, ip IP, port int, zone string) (syscall.Sockaddr, error) {
	switch family {
	case syscall.AF_INET6:
		sa, err := ipToSockaddrInet6(ip, port, zone)
		if err != nil {
			return nil, err
		}
		return &sa, nil
	}
	return nil, nil
}
