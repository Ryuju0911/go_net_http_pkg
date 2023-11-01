// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file implements a decision tree for fast matching of requests to
// patterns.
//
// The root of the tree branches on the host of the request.
// The next level branches on the method.
// The remaining levels branch on consecutive segments of the path.
//
// The "more specific wins" precedence rule can result in backtracking.
// For example, given the patterns
//     /a/b/z
//     /a/{x}/c
// we will first try to match the path "/a/b/c" with /a/b/z, and
// when that fails we will try against /a/{x}/c.

package http

// A routingNode is a node in the decision tree.
// The same struct is used for leaf and interior nodes.
type routingNode struct {
	// An interior node maps parts of the incoming request to child nodes.
	// special children keys:
	//     "/"	trailing slash (resulting from {$})
	//	   ""   single wildcard
	//	   "*"  multi wildcard
	children   mapping[string, *routingNode]
	emptyChild *routingNode // optimization: child with key ""
}

func (n *routingNode) findChild(key string) *routingNode {
	if key == "" {
		return n.emptyChild
	}
	r, _ := n.children.find(key)
	return r
}

func (root *routingNode) match(host, method, path string) (*routingNode, []string) {
	if host != "" {
		// There is a host. If there is a pattern that specifies that host and it
		// matches, we are done. If the pattern doesn't match, fall through to
		// try patterns with no host.
		if l, m := root.findChild(host).matchMethodAndPath(method, path); l != nil {
			return l, m
		}
	}
	return root.emptyChild.matchMethodAndPath(method, path)
}

func (n *routingNode) matchMethodAndPath(method, path string) (*routingNode, []string) {
	if n == nil {
		return nil, nil
	}
	if l, m := n.findChild(method).matchPath(path, nil); l != nil {
		// Exact match of method name.
		return l, m
	}
	if method == "HEAD" {
		// GET matches HEAD too.
		if l, m := n.findChild("GET").matchPath(path, nil); l != nil {
			return l, m
		}
	}
	// No exact match; try patterns with no method.
	return n.emptyChild.matchPath(path, nil)
}

func (n *routingNode) matchPath(path string, matches []string) (*routingNode, []string) {
	if n == nil {
		return nil, nil
	}
}
