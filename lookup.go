// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package net

import (
	"context"
)

// protocols contains minimal mappings between internet protocol
// names and numbers for platforms that don't have a complete list of
// protocol numbers.
//
// See https://www.iana.org/assignments/protocol-numbers
//
// On Unix, this map is augmented by readProtocols via lookupProtocol.
var protocols = map[string]int{
	"icmp":      1,
	"igmp":      2,
	"tcp":       6,
	"udp":       17,
	"ipv6-icmp": 58,
}

// services contains minimal mappings between services names and port
// numbers for platforms that don't have a complete list of port numbers.
//
// See https://www.iana.org/assignments/service-names-port-numbers
//
// On Unix, this map is augmented by readServices via goLookupPort.
var services = map[string]map[string]int{
	"udp": {
		"domain": 53,
	},
	"tcp": {
		"ftp":         21,
		"ftps":        990,
		"gopher":      70, // ʕ◔ϖ◔ʔ
		"http":        80,
		"https":       443,
		"imap2":       143,
		"imap3":       220,
		"imaps":       993,
		"pop3":        110,
		"pop3s":       995,
		"smtp":        25,
		"submissions": 465,
		"ssh":         22,
		"telnet":      23,
	},
}

const maxProtoLength = len("RSVP-E2E-IGNORE") + 10 // with room to grow

func lookupProtocolMap(name string) (int, error) {
	var lowerProtocol [maxProtoLength]byte
	n := copy(lowerProtocol[:], name)
	lowerASCIIBytes(lowerProtocol[:n])
	proto, found := protocols[string(lowerProtocol[:n])]
	if !found || n != len(name) {
		return 0, &AddrError{Err: "unknown IP protocol specified", Addr: name}
	}
	return proto, nil
}

// maxPortBufSize is the longest reasonable name of a service
// (non-numeric port).
// Currently the longest known IANA-unregistered name is
// "mobility-header", so we use that length, plus some slop in case
// something longer is added in the future.
const maxPortBufSize = len("mobility-header") + 10

func lookupPortMap(network, service string) (port int, error error) {
	switch network {
	case "ip": // no hints
		if p, err := lookupPortMapWithNetwork("tcp", "ip", service); err == nil {
			return p, nil
		}
		return lookupPortMapWithNetwork("udp", "ip", service)
	case "tcp", "tcp4", "tcp6":
		return lookupPortMapWithNetwork("tcp", "tcp", service)
	case "udp", "udp4", "udp6":
		return lookupPortMapWithNetwork("udp", "udp", service)
	}
	return 0, &DNSError{Err: "unknown network", Name: network + "/" + service}
}

func lookupPortMapWithNetwork(network, errNetwork, service string) (port int, error error) {
	if m, ok := services[network]; ok {
		var lowerService [maxPortBufSize]byte
		n := copy(lowerService[:], service)
		lowerASCIIBytes(lowerService[:n])
		if port, ok := m[string(lowerService[:n])]; ok && n == len(service) {
			return port, nil
		}
		return 0, &DNSError{Err: "unknown port", Name: errNetwork + "/" + service, IsNotFound: true}
	}
	return 0, &DNSError{Err: "unknown network", Name: errNetwork + "/" + service}
}

// DefaultResolver is the resolver used by the package-level Lookup
// functions and by Dialers without a specified Resolver.
var DefaultResolver = &Resolver{}

// A Resolver looks up names and numbers.
//
// A nil *Resolver is equivalent to a zero Resolver.
type Resolver struct {
}

// LookupPort looks up the port for the given network and service.
//
// The network must be one of "tcp", "tcp4", "tcp6", "udp", "udp4", "udp6" or "ip".
func (r *Resolver) LookupPort(ctx context.Context, network, service string) (port int, err error) {
	port, needsLookup := parsePort(service)
	if needsLookup {
		switch network {
		case "tcp", "tcp4", "tcp6", "udp", "udp4", "udp6", "ip":
		case "": // a hint wildcard for Go 1.0 undocumented behavior
			network = "ip"
		default:
			return 0, &AddrError{Err: "unknown network", Addr: network}
		}
		port, err = r.lookupPort(ctx, network, service)
		if err != nil {
			return 0, err
		}
	}
	if 0 > port || port > 65535 {
		return 0, &AddrError{Err: "invalid port", Addr: service}
	}
	return port, nil
}
