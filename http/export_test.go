// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Bridge package to expose http internals to tests in the http_test
// package.

package http

import "sort"

func (t *Transport) IdleConnKeysForTesting() (keys []string) {
	keys = make([]string, 0)
	t.idleMu.Lock()
	defer t.idleMu.Unlock()
	for key := range t.idleConn {
		keys = append(keys, key.String())
	}
	sort.Strings(keys)
	return
}

func (t *Transport) IdleConnCountForTesting(scheme, addr string) int {
	t.idleMu.Lock()
	defer t.idleMu.Unlock()
	key := connectMethodKey{"", scheme, addr, false}
	cacheKey := key.String()
	for k, conns := range t.idleConn {
		if k.String() == cacheKey {
			return len(conns)
		}
	}
	return 0
}
