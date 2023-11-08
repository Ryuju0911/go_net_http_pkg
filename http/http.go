// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package http

// maxInt64 is the effective "infinite" value for the Server and
// Transport's byte-limiting readers.
const maxInt64 = 1<<63 - 1

// contextKey is a value for use with context.WithValue. It's used as
// a pointer so it fits in an interface{} without allocation.
type contextKey struct {
	name string
}

// func isNotToken(r rune) bool {
// 	return !httpguts.IsTokenRune(r)
// }
