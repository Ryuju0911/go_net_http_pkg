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
	// switch runtime.GOOS {
	// case "aix", "windows", "openbsd", "js", "wasip1":
	// 	if mode == "dial" && raddr.isWildcard() {
	// 		raddr = raddr.toLocal(net)
	// 	}
	// }
	// family, ipv6only := favoriteAddrFamily(net, laddr, raddr, mode)

	// Temporarily hard coded.
	family, ipv6only := syscall.AF_INET6, false
	return socket(ctx, net, family, sotype, proto, ipv6only, laddr, raddr, ctrlCtxFn)
}

func ipToSockaddrInet6(ip IP, port int, zone string) (syscall.SockaddrInet6, error) {
	// In general, an IP wildcard address, which is either
	// "0.0.0.0" or "::", means the entire IP addressing
	// space. For some historical reason, it is used to
	// specify "any available address" on some operations
	// of IP node.
	//
	// When the IP node supports IPv4-mapped IPv6 address,
	// we allow a listener to listen to the wildcard
	// address of both IP addressing spaces by specifying
	// IPv6 wildcard address.
	if len(ip) == 0 || ip.Equal(IPv4zero) {
		ip = IPv6zero
	}
	// We accept any IPv6 address including IPv4-mapped
	// IPv6 address.
	ip6 := ip.To16()
	if ip6 == nil {
		return syscall.SockaddrInet6{}, &AddrError{Err: "non-IPv6 address", Addr: ip.String()}
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
