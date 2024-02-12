// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package http

// cloneOrMakeHeader invokes Header.Clone but if the
// result is nil, it'll instead make and return a non-nil Header.
func cloneOrMakeHeader(hdr Header) Header {
	clone := hdr.Clone()
	if clone == nil {
		clone = make(Header)
	}
	return clone
}
