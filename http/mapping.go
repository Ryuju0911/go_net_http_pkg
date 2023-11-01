// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package http

// A mapping is a collection of key-value pairs where the keys are unique.
// A zero mapping is empty and ready to use.
// A mapping tries to pick a representation that makes [mapping.find] most efficient.
type mapping[K comparable, V any] struct {
	s []entry[K, V] // for few pairs
	m map[K]V       // for many pairs
}

type entry[K comparable, V any] struct {
	key   K
	value V
}

func (h *mapping[K, V]) find(k K) (v V, found bool) {
	if h == nil {
		return v, false
	}
	if h.m != nil {
		v, found = h.m[k]
		return v, found
	}
	for _, e := range h.s {
		if e.key == k {
			return e.value, true
		}
	}
	return v, false
}
