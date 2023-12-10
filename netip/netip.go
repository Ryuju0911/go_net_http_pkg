// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package netip defines an IP address type that's a small value type.
// Building on that [Addr] type, the package also defines [AddrPort] (an
// IP address and a port) and [Prefix] (an IP address and a bit length
// prefix).
//
// Compared to the [net.IP] type, [Addr] type takes less memory, is immutable,
// and is comparable (supports == and being a map key).
package netip

import (
	"internal/intern"
)

// Sizes: (64-bit)
//   net.IP:     24 byte slice header + {4, 16} = 28 to 40 bytes
//   net.IPAddr: 40 byte slice header + {4, 16} = 44 to 56 bytes + zone length
//   netip.Addr: 24 bytes (zone is per-name singleton, shared across all users)

// Addr represents an IPv4 or IPv6 address (with or without a scoped
// addressing zone), similar to [net.IP] or [net.IPAddr].
//
// Unlike [net.IP] or [net.IPAddr], Addr is a comparable value
// type (it supports == and can be a map key) and is immutable.
//
// The zero Addr is not a valid IP address.
// Addr{} is distinct from both 0.0.0.0 and ::.
type Addr struct {
	// addr is the hi and lo bits of an IPv6 address. If z==z4,
	// hi and lo contain the IPv4-mapped IPv6 address.
	//
	// hi and lo are constructed by interpreting a 16-byte IPv6
	// address as a big-endian 128-bit number. The most significant
	// bits of that number go into hi, the rest into lo.
	//
	// For example, 0011:2233:4455:6677:8899:aabb:ccdd:eeff is stored as:
	//  addr.hi = 0x0011223344556677
	//  addr.lo = 0x8899aabbccddeeff
	//
	// We store IPs like this, rather than as [16]byte, because it
	// turns most operations on IPs into arithmetic and bit-twiddling
	// operations on 64-bit registers, which is much faster than
	// bytewise processing.
	addr uint128

	// z is a combination of the address family and the IPv6 zone.
	//
	// nil means invalid IP address (for a zero Addr).
	// z4 means an IPv4 address.
	// z6noz means an IPv6 address without a zone.
	//
	// Otherwise it's the interned zone name string.
	z *intern.Value
}

// z0, z4, and z6noz are sentinel Addr.z values.
// See the Addr type's field docs.
var (
	z0    = (*intern.Value)(nil)
	z4    = new(intern.Value)
	z6noz = new(intern.Value)
)

// AddrFrom4 returns the address of the IPv4 address given by the bytes in addr.
func AddrFrom4(addr [4]byte) Addr {
	return Addr{
		addr: uint128{0, 0xffff00000000 | uint64(addr[0])<<24 | uint64(addr[1])<<16 | uint64(addr[2])<<8 | uint64(addr[3])},
		z:    z4,
	}
}

// AddrFrom16 returns the IPv6 address given by the bytes in addr.
// An IPv4-mapped IPv6 address is left as an IPv6 address.
// (Use Unmap to convert them if needed.)
func AddrFrom16(addr [16]byte) Addr {
	return Addr{
		addr: uint128{
			beUint64(addr[:8]),
			beUint64(addr[8:]),
		},
		z: z6noz,
	}
}

// v4 returns the i'th byte of ip. If ip is not an IPv4, v4 returns
// unspecified garbage.
func (ip Addr) v4(i uint8) uint8 {
	return uint8(ip.addr.lo >> ((3 - i) * 8))
}

// v6u16 returns the i'th 16-bit word of ip. If ip is an IPv4 address,
// this accesses the IPv4-mapped IPv6 address form of the IP.
func (ip Addr) v6u16(i uint8) uint16 {
	return uint16(*(ip.addr.halves()[(i/4)%2]) >> ((3 - i%4) * 16))
}

// Zone returns ip's IPv6 scoped addressing zone, if any.
func (ip Addr) Zone() string {
	if ip.z == nil {
		return ""
	}
	zone, _ := ip.z.Get().(string)
	return zone
}

// Is4 reports whether ip is an IPv4 address.
//
// It returns false for IPv4-mapped IPv6 addresses. See Addr.Unmap.
func (ip Addr) Is4() bool {
	return ip.z == z4
}

// Is4In6 reports whether ip is an IPv4-mapped IPv6 address.
func (ip Addr) Is4In6() bool {
	return ip.Is6() && ip.addr.hi == 0 && ip.addr.lo>>32 == 0xffff
}

// Is6 reports whether ip is an IPv6 address, including IPv4-mapped
// IPv6 addresses.
func (ip Addr) Is6() bool {
	return ip.z != z0 && ip.z != z4
}

// Unmap returns ip with any IPv4-mapped IPv6 address prefix removed.
//
// That is, if ip is an IPv6 address wrapping an IPv4 address, it
// returns the wrapped IPv4 address. Otherwise it returns ip unmodified.
func (ip Addr) Unmap() Addr {
	if ip.Is4In6() {
		ip.z = z4
	}
	return ip
}

// String returns the string form of the IP address ip.
// It returns one of 5 forms:
//
//   - "invalid IP", if ip is the zero Addr
//   - IPv4 dotted decimal ("192.0.2.1")
//   - IPv6 ("2001:db8::1")
//   - "::ffff:1.2.3.4" (if Is4In6)
//   - IPv6 with zone ("fe80:db8::1%eth0")
//
// Note that unlike package net's IP.String method,
// IPv4-mapped IPv6 addresses format with a "::ffff:"
// prefix before the dotted quad.
func (ip Addr) String() string {
	switch ip.z {
	case z0:
		return "invalid IP"
	case z4:
		return ip.string4()
	default:
		if ip.Is4In6() {
			if z := ip.Zone(); z != "" {
				return "::ffff:" + ip.Unmap().string4() + "%" + z
			} else {
				return "::ffff:" + ip.Unmap().string4()
			}
		}
		return ip.string6()
	}
}

// digits is a string of the hex digits from 0 to f. It's used in
// appendDecimal and appendHex to format IP addresses.
const digits = "0123456789abcdef"

// appendDecimal appends the decimal string representation of x to b.
func appendDecimal(b []byte, x uint8) []byte {
	// Using this function rather than strconv.AppendUint makes IPv4
	// string building 2x faster.

	if x >= 100 {
		b = append(b, digits[x/100])
	}
	if x >= 10 {
		b = append(b, digits[x/10%10])
	}
	return append(b, digits[x%10])
}

// appendHex appends the hex string representation of x to b.
func appendHex(b []byte, x uint16) []byte {
	// Using this function rather than strconv.AppendUint makes IPv6
	// string building 2x faster.

	if x >= 0x1000 {
		b = append(b, digits[x>>12])
	}
	if x >= 0x100 {
		b = append(b, digits[x>>8&0xf])
	}
	if x >= 0x10 {
		b = append(b, digits[x>>4&0xf])
	}
	return append(b, digits[x&0xf])
}

func (ip Addr) string4() string {
	const max = len("255.255.255.255")
	ret := make([]byte, 0, max)
	ret = ip.appendTo4(ret)
	return string(ret)
}

func (ip Addr) appendTo4(ret []byte) []byte {
	ret = appendDecimal(ret, ip.v4(0))
	ret = append(ret, '.')
	ret = appendDecimal(ret, ip.v4(1))
	ret = append(ret, '.')
	ret = appendDecimal(ret, ip.v4(2))
	ret = append(ret, '.')
	ret = appendDecimal(ret, ip.v4(3))
	return ret
}

// string6 formats ip in IPv6 textual representation. It follows the
// guidelines in section 4 of RFC 5952
// (https://tools.ietf.org/html/rfc5952#section-4): no unnecessary
// zeros, use :: to elide the longest run of zeros, and don't use ::
// to compact a single zero field.
func (ip Addr) string6() string {
	// Use a zone with a "plausibly long" name, so that most zone-ful
	// IP addresses won't require additional allocation.
	//
	// The compiler does a cool optimization here, where ret ends up
	// stack-allocated and so the only allocation this function does
	// is to construct the returned string. As such, it's okay to be a
	// bit greedy here, size-wise.
	const max = len("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff%enp5s0")
	ret := make([]byte, 0, max)
	ret = ip.appendTo6(ret)
	return string(ret)
}

func (ip Addr) appendTo6(ret []byte) []byte {
	zeroStart, zeroEnd := uint8(255), uint8(255)
	for i := uint8(0); i < 8; i++ {
		j := i
		for j < 8 && ip.v6u16(j) == 0 {
			j++
		}
		if l := j - i; l >= 2 && l > zeroEnd-zeroStart {
			zeroStart, zeroEnd = i, j
		}
	}

	for i := uint8(0); i < 8; i++ {
		if i == zeroStart {
			ret = append(ret, ':', ':')
			i = zeroEnd
			if i >= 8 {
				break
			}
		} else if i > 0 {
			ret = append(ret, ':')
		}

		ret = appendHex(ret, ip.v6u16(i))
	}

	if ip.z != z6noz {
		ret = append(ret, '%')
		ret = append(ret, ip.Zone()...)
	}
	return ret
}
