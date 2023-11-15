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
	// A leaf node holds a single pattern and the Handler it was registered
	// with.
	pattern *pattern
	handler Handler

	// An interior node maps parts of the incoming request to child nodes.
	// special children keys:
	//     "/"	trailing slash (resulting from {$})
	//	   ""   single wildcard
	//	   "*"  multi wildcard
	children   mapping[string, *routingNode]
	emptyChild *routingNode // optimization: child with key ""
}

func (root *routingNode) addPattern(p *pattern, h Handler) {
	// First level of tree is host.
	n := root.addChild(p.host)
	// Second level of tree is method.
	n = n.addChild(p.method)
	// Remaining levels are path.
	n.addSegments(p.segments, p, h)
}

// addSegments adds the given segments to the tree rooted at n.
// If there are no segments, then n is a leaf node that holds
// the given pattern and handler.
func (n *routingNode) addSegments(segs []segment, p *pattern, h Handler) {
	if len(segs) == 0 {
		n.set(p, h)
		return
	}
	seg := segs[0]
	if seg.multi {
		if len(segs) != 1 {
			panic("multi wildcard not last")
		}
		n.addChild("*").set(p, h)
	} else if seg.wild {
		n.addChild("").addSegments(segs[1:], p, h)
	} else {
		n.addChild(seg.s).addSegments(segs[1:], p, h)
	}
}

// set sets the pattern and handler for n, which
// must be a leaf node.
func (n *routingNode) set(p *pattern, h Handler) {
	if n.pattern != nil || n.handler != nil {
		panic("non-nil leaf fields")
	}
	n.pattern = p
	n.handler = h
}

// addChild adds a child node with the given key to n
// if one does not exist, and returns the child.
func (n *routingNode) addChild(key string) *routingNode {
	if key == "" {
		if n.emptyChild == nil {
			n.emptyChild = &routingNode{}
		}
		return n.emptyChild
	}
	if c := n.findChild(key); c != nil {
		return c
	}
	c := &routingNode{}
	n.children.add(key, c)
	return c
}

func (n *routingNode) findChild(key string) *routingNode {
	if key == "" {
		return n.emptyChild
	}
	r, _ := n.children.find(key)
	return r
}
