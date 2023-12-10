// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package netip

// uint128 represents a uint128 using two uint64s.
//
// When the methods below mention a bit number, bit 0 is the most
// significant bit (in hi) and bit 127 is the lowest (lo&1).
type uint128 struct {
	hi uint64
	lo uint64
}

// halves returns the two uint64 halves of the uint128.
//
// Logically, think of it as returning two uint64s.
// It only returns pointers for inlining reasons on 32-bit platforms.
func (u *uint128) halves() [2]*uint64 {
	return [2]*uint64{&u.hi, &u.lo}
}
