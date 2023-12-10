// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package net

import (
	"context"
	"internal/bytealg"
)

// An addrList represents a list of network endpoint addresses.
type addrList []Addr

// isIPv4 reports whether addr contains an IPv4 address.
func isIPv4(addr Addr) bool {
	switch addr := addr.(type) {
	case *TCPAddr:
		return addr.IP.To4() != nil
	case *UDPAddr:
		return addr.IP.To4() != nil
	case *IPAddr:
		return addr.IP.To4() != nil
	}
	return false
}

// first returns the first address which satisfies strategy, or if
// none do, then the first address of any kind.
func (addrs addrList) first(strategy func(Addr) bool) Addr {
	for _, addr := range addrs {
		if strategy(addr) {
			return addr
		}
	}
	return addrs[0]
}

// SplitHostPort splits a network address of the form "host:port",
// "host%zone:port", "[host]:port" or "[host%zone]:port" into host or
// host%zone and port.
//
// A literal IPv6 address in hostport must be enclosed in square
// brackets, as in "[::1]:80", "[::1%lo0]:80".
//
// See func Dial for a description of the hostport parameter, and host
// and port results.
func SplitHostPort(hostport string) (host, port string, err error) {
	const (
		missingPort   = "missing port in address"
		tooManyColons = "too many colons in address"
	)
	addrErr := func(addr, why string) (host, port string, err error) {
		return "", "", &AddrError{Err: why, Addr: addr}
	}
	j, k := 0, 0

	// The port starts after the last colon.
	i := bytealg.LastIndexByteString(hostport, ':')
	if i < 0 {
		return addrErr(hostport, missingPort)
	}

	if hostport[0] == '[' {
		// Expect the first ']' just before the last ':'.
		end := bytealg.IndexByteString(hostport, ']')
		if end < 0 {
			return addrErr(hostport, "missing ']' in address")
		}
		switch end + 1 {
		case len(hostport):
			// There can't be a ':' behind the ']' now.
			return addrErr(hostport, missingPort)
		case i:
			// The expected result.
		default:
			// Either ']' isn't followed by a colon, or it is
			// followed by a colon that is not the last one.
			if hostport[end+1] == ':' {
				return addrErr(hostport, tooManyColons)
			}
			return addrErr(hostport, missingPort)
		}
		host = hostport[1:end]
		j, k = 1, end+1 // there can't be a '[' resp. ']' before these positions
	} else {
		host = hostport[:i]
		if bytealg.IndexByteString(host, ':') >= 0 {
			return addrErr(hostport, tooManyColons)
		}
	}
	if bytealg.IndexByteString(hostport[j:], '[') >= 0 {
		return addrErr(hostport, "unexpected '[' in address")
	}
	if bytealg.IndexByteString(hostport[k:], ']') >= 0 {
		return addrErr(hostport, "unexpected ']' in address")
	}

	port = hostport[i+1:]
	return host, port, nil
}

func JoinHostPort(host, port string) string {
	// We assume that host is a literal IPv6 address if host has
	// colons.
	if bytealg.IndexByteString(host, ':') >= 0 {
		return "[" + host + "]:" + port
	}
	return host + ":" + port
}

// internetAddrList resolves addr, which may be a literal IP
// address or a DNS name, and returns a list of internet protocol
// family addresses. The result contains at least one address when
// error is nil.
func (r *Resolver) internetAddrList(ctx context.Context, net, addr string) (addrList, error) {
	var (
		err        error
		host, port string
		portnum    int
	)
	switch net {
	case "tcp", "tcp4", "tcp6", "udp", "udp4", "udp6":
		if addr != "" {
			if host, port, err = SplitHostPort(addr); err != nil {
				return nil, err
			}
			if portnum, err = r.LookupPort(ctx, net, port); err != nil {
				return nil, err
			}
		}
	case "ip", "ip4", "ip6":
		if addr != "" {
			host = addr
		}
	default:
		return nil, UnknownNetworkError(net)
	}
	inetaddr := func(ip IPAddr) Addr {
		switch net {
		case "tcp", "tcp4", "tcp6":
			return &TCPAddr{IP: ip.IP, Port: portnum, Zone: ip.Zone}
		case "udp", "udp4", "udp6":
			return &UDPAddr{IP: ip.IP, Port: portnum, Zone: ip.Zone}
		case "ip", "ip4", "ip6":
			return &IPAddr{IP: ip.IP, Zone: ip.Zone}
		default:
			panic("unexpected network: " + net)
		}
	}
	if host == "" {
		return addrList{inetaddr(IPAddr{})}, nil
	}

	// TODO: Handle the case when host != "".
	return addrList{inetaddr(IPAddr{})}, nil
}
