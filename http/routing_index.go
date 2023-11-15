// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package http

// A routingIndex optimizes conflict detection by indexing patterns.
//
// The basic idea is to rule out patterns that cannot conflict with a given
// pattern because they have a different literal in a corresponding segment.
// See the comments in [routingIndex.possiblyConflictingPatterns] for more details.
type routingIndex struct {
	// map from a particular segment position and value to all registered patterns
	// with that value in that position.
	// For example, the key {1, "b"} would hold the patterns "/a/b" and "/a/b/c"
	// but not "/a", "b/a", "/a/c" or "/a/{x}".
	segments map[routingIndexKey][]*pattern
	// All patterns that end in a multi wildcard (including trailing slash).
	// We do not try to be clever about indexing multi patterns, because there
	// are unlikely to be many of them.
	multis []*pattern
}

type routingIndexKey struct {
	pos int    // 0-based segment position
	s   string // literal, or empty for wildcard
}

func (idx *routingIndex) addPattern(pat *pattern) {
	if pat.lastSegment().multi {
		idx.multis = append(idx.multis, pat)
	} else {
		if idx.segments == nil {
			idx.segments = map[routingIndexKey][]*pattern{}
		}
		for pos, seg := range pat.segments {
			key := routingIndexKey{pos: pos, s: ""}
			if !seg.wild {
				key.s = seg.s
			}
			idx.segments[key] = append(idx.segments[key], pat)
		}
	}
}
