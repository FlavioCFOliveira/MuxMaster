package muxmaster

import (
	"net/http"
	"strings"
)

type nodeType uint8

const (
	static   nodeType = iota
	root
	param
	wildcard
)

// node is a single node in the radix (compressed prefix) tree.
type node struct {
	path      string       // compressed path segment (edge label)
	indices   string       // first byte of each child's path, for O(1) dispatch
	wildChild bool         // one of the children is a wildcard node
	nType     nodeType
	priority  uint32
	children  []*node
	handler   http.Handler
}

// addRoute inserts path → handler into the tree.
// Not concurrency-safe; protected by Mux.mu at the call site.
func (n *node) addRoute(path string, handler http.Handler) {
	fullPath := path
	n.priority++

	if n.path == "" && n.indices == "" {
		n.insertChild(path, fullPath, handler)
		n.nType = root
		return
	}

walk:
	for {
		i := longestCommonPrefix(path, n.path)

		// Split the current node when the common prefix is shorter than n.path.
		if i < len(n.path) {
			child := &node{
				path:      n.path[i:],
				wildChild: n.wildChild,
				nType:     static,
				indices:   n.indices,
				children:  n.children,
				handler:   n.handler,
				priority:  n.priority - 1,
			}
			n.children = []*node{child}
			n.indices = string(n.path[i])
			n.path = path[:i]
			n.handler = nil
			n.wildChild = false
		}

		if i < len(path) {
			path = path[i:]
			c := path[0]

			// After a param node, continue into the single '/' child.
			if n.nType == param && c == '/' && len(n.children) == 1 {
				n = n.children[0]
				n.priority++
				continue walk
			}

			// Check whether a child starting with c already exists.
			for j := range len(n.indices) {
				if c == n.indices[j] {
					j = n.incrementChildPrio(j)
					n = n.children[j]
					continue walk
				}
			}

			// Insert a new child.
			if c != ':' && c != '*' {
				n.indices += string(c)
				child := &node{}
				n.children = append(n.children, child)
				n.incrementChildPrio(len(n.indices) - 1)
				n = child
			} else if n.wildChild {
				n = n.children[len(n.children)-1]
				n.priority++

				if len(path) >= len(n.path) &&
					n.path == path[:len(n.path)] &&
					n.nType != wildcard &&
					(len(n.path) >= len(path) || path[len(n.path)] == '/') {
					continue walk
				}

				seg := strings.SplitN(path, "/", 2)[0]
				pfx := fullPath[:strings.Index(fullPath, seg)] + n.path
				panic("'" + seg + "' in path '" + fullPath +
					"' conflicts with existing wildcard '" + pfx + "'")
			}

			n.insertChild(path, fullPath, handler)
			return
		}

		if n.handler != nil {
			panic("a handler is already registered for path '" + fullPath + "'")
		}
		n.handler = handler
		return
	}
}

// incrementChildPrio bumps the priority of child at pos and reorders children
// by descending priority. Returns the new index of the promoted child.
func (n *node) incrementChildPrio(pos int) int {
	cs := n.children
	cs[pos].priority++
	prio := cs[pos].priority

	newPos := pos
	for newPos > 0 && cs[newPos-1].priority < prio {
		cs[newPos-1], cs[newPos] = cs[newPos], cs[newPos-1]
		newPos--
	}

	if newPos != pos {
		n.indices = n.indices[:newPos] +
			n.indices[pos:pos+1] +
			n.indices[newPos:pos] +
			n.indices[pos+1:]
	}
	return newPos
}

// insertChild handles the portion of a path that contains wildcards.
func (n *node) insertChild(path, fullPath string, handler http.Handler) {
	for {
		wc, i, valid := findWildcard(path)
		if i < 0 {
			break
		}
		if !valid {
			panic("only one wildcard per path segment is allowed in '" + fullPath + "'")
		}
		if len(wc) < 2 {
			panic("wildcards must be named in path '" + fullPath + "'")
		}

		if wc[0] == ':' {
			if i > 0 {
				n.path = path[:i]
				path = path[i:]
			}
			child := &node{nType: param, path: wc}
			n.children = []*node{child}
			n.wildChild = true
			n = child
			n.priority++

			if len(wc) < len(path) {
				path = path[len(wc):]
				next := &node{priority: 1}
				n.children = []*node{next}
				n = next
				continue
			}
			n.handler = handler
			return
		}

		// Catch-all '*'
		if i+len(wc) != len(path) {
			panic("catch-all routes are only allowed at the end of the path in '" + fullPath + "'")
		}
		if len(n.path) > 0 && n.path[len(n.path)-1] == '/' {
			panic("catch-all conflicts with existing handler for the path root in '" + fullPath + "'")
		}

		i-- // step back to the '/' before '*'
		if path[i] != '/' {
			panic("no '/' before catch-all in path '" + fullPath + "'")
		}

		n.path = path[:i]

		catchAll := &node{wildChild: true, nType: static}
		n.children = []*node{catchAll}
		n.indices = "/"
		n = catchAll
		n.priority++

		leaf := &node{
			path:     path[i:],
			nType:    wildcard,
			handler:  handler,
			priority: 1,
		}
		n.children = []*node{leaf}
		return
	}

	n.path = path
	n.handler = handler
}

// getValue traverses the tree for path, filling params along the way.
// Returns the matched handler and whether a trailing-slash redirect applies.
func (n *node) getValue(path string, params *Params) (handler http.Handler, tsr bool) {
walk:
	for {
		prefix := n.path

		if len(path) > len(prefix) {
			if path[:len(prefix)] != prefix {
				return
			}
			path = path[len(prefix):]

			if !n.wildChild {
				c := path[0]
				for j := range len(n.indices) {
					if c == n.indices[j] {
						n = n.children[j]
						continue walk
					}
				}
				tsr = path == "/" && n.handler != nil
				return
			}

			n = n.children[len(n.children)-1]

			switch n.nType {
			case param:
				end := strings.IndexByte(path, '/')
				if end < 0 {
					end = len(path)
				}
				if params != nil {
					*params = append(*params, Param{Key: n.path[1:], Value: path[:end]})
				}
				if end == len(path) {
					handler = n.handler
					if handler == nil && len(n.children) == 1 {
						n = n.children[0]
						tsr = n.path == "/" && n.handler != nil
					}
					return
				}
				if len(n.children) > 0 {
					path = path[end:]
					n = n.children[0]
					continue walk
				}
				tsr = len(path) == end+1
				return

			case wildcard:
				if params != nil {
					*params = append(*params, Param{Key: n.path[2:], Value: path})
				}
				handler = n.handler
				return

			default:
				panic("muxmaster: invalid node type")
			}
		}

		if path == prefix {
			handler = n.handler
			if handler != nil {
				return
			}
			for j := range len(n.indices) {
				if n.indices[j] == '/' {
					n = n.children[j]
					tsr = (n.path == "/" && n.handler != nil) ||
						(n.nType == wildcard && n.children[0].handler != nil)
					return
				}
			}
			tsr = path == "/" ||
				(len(n.indices) == 1 && n.indices[0] == '/' && n.children[0].handler != nil)
			return
		}

		tsr = (path == "/" ||
			(len(prefix) == len(path)+1 &&
				prefix[len(path)] == '/' &&
				path == prefix[:len(prefix)-1] &&
				n.handler != nil))
		return
	}
}

// hasHandler reports whether any handler is registered at path.
// Used to build the Allow header for 405 responses.
func (n *node) hasHandler(path string) bool {
	h, _ := n.getValue(path, nil)
	return h != nil
}

// longestCommonPrefix returns the length of the shared prefix of a and b.
func longestCommonPrefix(a, b string) int {
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	for i := range limit {
		if a[i] != b[i] {
			return i
		}
	}
	return limit
}

// findWildcard scans path for the first ':' or '*' wildcard.
// Returns the wildcard token, its start index, and whether it is valid
// (no more than one special character within the segment).
func findWildcard(path string) (token string, start int, valid bool) {
	for i, c := range []byte(path) {
		if c != ':' && c != '*' {
			continue
		}
		valid = true
		for j, ch := range []byte(path[i+1:]) {
			switch ch {
			case '/':
				return path[i : i+1+j], i, valid
			case ':', '*':
				valid = false
			}
		}
		return path[i:], i, valid
	}
	return "", -1, false
}
