package muxmaster

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// paramsBuf is a fixed-size params accumulator used in the getValue hot path.
// Using a fixed-size struct instead of a []Param slice prevents the backing
// array from escaping to the heap — the compiler can see the size is bounded.
// maxParams covers ≥99% of real-world APIs.
const maxParams = 8

type paramsBuf struct {
	count int
	buf   [maxParams]Param
}

// add appends a param to the buffer, silently dropping overflow (> maxParams).
func (pb *paramsBuf) add(key, value string) {
	if pb.count < maxParams {
		pb.buf[pb.count] = Param{Key: key, Value: value}
		pb.count++
	}
}

// params returns a Params slice backed by the buffer's inline array.
// The returned slice must not be used after the paramsBuf goes out of scope.
func (pb *paramsBuf) params() Params {
	return Params(pb.buf[:pb.count])
}

type nodeType uint8

const (
	static nodeType = iota
	root
	param
	wildcard
	regexParam // {name:expr}
)

// Field layout is hand-tuned to put the hot-read fields in cache line 0 (0-63).
// A successful static route match reads only `path` + `handler` — both in CL0.
// `pattern`, `priority`, `nType`, `wildChild`, `regexp` are cold — CL1.
type node struct {
	// --- Cache line 0 (offsets 0-63) ---
	path     string       // 16 bytes @ 0
	handler  http.Handler // 16 bytes @ 16  (moved from CL1 — hot on leaf match)
	indices  string       // 16 bytes @ 32
	children []*node      // 24 bytes @ 48-71 (crosses into CL1)

	// --- Cache line 1 (offsets 64-103) ---
	pattern       string         // 16 bytes @ 72 (cold on lookup; set on leaves)
	priority      uint32         // 4 bytes  @ 88 (written only during registration)
	nType         nodeType       // 1 byte   @ 92
	wildChild     bool           // 1 byte   @ 93
	regexpNameEnd uint8          // 1 byte   @ 94 (end index of param name in path for regexParam nodes)
	regexp        *regexp.Regexp // 8 bytes  @ 96 (regex routes only)
}

// addRoute registers a handler for the given path, expanding optional segments first.
func (n *node) addRoute(path string, handler http.Handler) {
	// Expand optional segments before doing anything else.
	if expanded, ok := expandOptional(path); ok {
		n.addRoute(expanded[0], handler)
		n.addRoute(expanded[1], handler)
		return
	}

	fullPath := path
	if !utf8.ValidString(path) {
		panic("muxmaster: path contains invalid UTF-8: " + strconv.QuoteToASCII(path))
	}
	n.priority++

	if n.path == "" && n.indices == "" {
		n.insertChild(path, fullPath, handler)
		n.nType = root
		return
	}

walk:
	for {
		i := longestCommonPrefix(path, n.path)

		if i < len(n.path) {
			child := &node{
				path:      n.path[i:],
				wildChild: n.wildChild,
				nType:     static,
				indices:   n.indices,
				children:  n.children,
				handler:   n.handler,
				priority:  n.priority - 1,
				pattern:   n.pattern, // split child inherits the original pattern
				regexp:    n.regexp,
			}
			n.children = []*node{child}
			n.indices = string(n.path[i])
			n.path = path[:i]
			n.handler = nil
			n.pattern = ""
			n.wildChild = false
		}

		if i < len(path) {
			path = path[i:]
			c := path[0]

			if n.nType == param && c == '/' && len(n.children) == 1 {
				n = n.children[0]
				n.priority++
				continue walk
			}

			for j := range len(n.indices) {
				if c == n.indices[j] {
					j = n.incrementChildPrio(j)
					n = n.children[j]
					continue walk
				}
			}

			if c != ':' && c != '*' && c != '{' {
				if n.wildChild {
					seg := strings.SplitN(path, "/", 2)[0]
					pfx := fullPath[:strings.Index(fullPath, seg)] + n.children[len(n.children)-1].path
					panic("'" + seg + "' in path '" + fullPath +
						"' conflicts with existing wildcard '" + pfx + "'")
				}
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
		n.pattern = fullPath
		return
	}
}

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
			// Append: preserve existing static children so they remain reachable via n.indices.
			// The wildchild is always the last element; getValue uses children[len-1] for it.
			n.children = append(n.children, child)
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
			n.pattern = fullPath
			return
		}

		if wc[0] == '{' {
			// Regex param: {name:expr}
			colonIdx := strings.Index(wc[1:], ":")
			if colonIdx < 0 {
				panic("muxmaster: regex param must have the form {name:expr} in '" + fullPath + "'")
			}
			expr := wc[2+colonIdx : len(wc)-1] // strip '{', name, ':', and trailing '}'
			re, err := regexp.Compile("^(?:" + expr + ")$")
			if err != nil {
				panic("muxmaster: invalid regexp in path '" + fullPath + "': " + err.Error())
			}

			if i > 0 {
				n.path = path[:i]
				path = path[i:]
			}
			child := &node{nType: regexParam, path: wc, regexp: re, regexpNameEnd: uint8(1 + colonIdx)}
			// Append: preserve existing static children so they remain reachable via n.indices.
			n.children = append(n.children, child)
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
			n.pattern = fullPath
			return
		}

		// Catch-all '*'
		if i+len(wc) != len(path) {
			panic("catch-all routes are only allowed at the end of the path in '" + fullPath + "'")
		}
		if len(n.path) > 0 && n.path[len(n.path)-1] == '/' {
			panic("catch-all conflicts with existing handler for the path root in '" + fullPath + "'")
		}

		i--
		if i < 0 || path[i] != '/' {
			panic("muxmaster: catch-all requires a '/' prefix in path '" + fullPath + "'")
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
			pattern:  fullPath,
			priority: 1,
		}
		n.children = []*node{leaf}
		return
	}

	n.path = path
	n.handler = handler
	n.pattern = fullPath
}

// getValue looks up the handler for path.
// ci enables case-insensitive static prefix matching.
// Returns the handler, the registered route pattern, and a trailing-slash-redirect hint.
// params may be nil (for static-route fast path) or point to a stack-allocated paramsBuf.
func (n *node) getValue(path string, params *paramsBuf, ci bool) (handler http.Handler, pattern string, tsr bool) {
walk:
	for {
		prefix := n.path

		if len(path) > len(prefix) {
			if !prefixMatch(path[:len(prefix)], prefix, ci) {
				return
			}
			path = path[len(prefix):]

			// Always try static children first so that /users/list beats /users/:id.
			c := path[0]
			children := n.children[:len(n.indices)]
			for j := range len(n.indices) {
				if foldEq(c, n.indices[j], ci) {
					n = children[j]
					continue walk
				}
			}

			if !n.wildChild {
				tsr = path == "/" && n.handler != nil
				return
			}

			// No static child matched — fall through to wildchild (always last).
			n = n.children[len(n.children)-1]

			switch n.nType {
			case param:
				// Inline scan for '/' — avoids strings.IndexByte call for short params.
				end := len(path)
				for i := 0; i < len(path); i++ {
					if path[i] == '/' {
						end = i
						break
					}
				}
				if params != nil {
					params.add(n.path[1:], path[:end])
				}
				if end == len(path) {
					handler = n.handler
					pattern = n.pattern
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

			case regexParam:
				end := len(path)
				for i := 0; i < len(path); i++ {
					if path[i] == '/' {
						end = i
						break
					}
				}
				seg := path[:end]
				if !n.regexp.MatchString(seg) {
					return
				}
				// Param name is between '{' and ':' in n.path, e.g. "{name:expr}".
				// regexpNameEnd is pre-computed at registration — no strings.Index here.
				name := n.path[1:n.regexpNameEnd]
				if params != nil {
					params.add(name, seg)
				}
				if end == len(path) {
					handler = n.handler
					pattern = n.pattern
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
					params.add(n.path[2:], path)
				}
				handler = n.handler
				pattern = n.pattern
				return

			default:
				panic("muxmaster: invalid node type")
			}
		}

		if prefixMatch(path, prefix, ci) && len(path) == len(prefix) {
			handler = n.handler
			pattern = n.pattern
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
				prefixMatch(path, prefix[:len(prefix)-1], ci) &&
				n.handler != nil))
		return
	}
}

// prefixMatch checks if s equals prefix, using case-insensitive comparison when ci is true.
// Both s and prefix must have the same length.
func prefixMatch(s, prefix string, ci bool) bool {
	if !ci {
		return s == prefix
	}
	if len(s) != len(prefix) {
		return false
	}
	for i := range len(s) {
		if !foldEq(s[i], prefix[i], ci) {
			return false
		}
	}
	return true
}

// foldEq compares two bytes, folding ASCII case when ci is true.
func foldEq(a, b byte, ci bool) bool {
	if !ci {
		return a == b
	}
	if a >= 'A' && a <= 'Z' {
		a += 32
	}
	if b >= 'A' && b <= 'Z' {
		b += 32
	}
	return a == b
}

func (n *node) hasHandler(path string) bool {
	h, _, _ := n.getValue(path, nil, false)
	return h != nil
}

// walk visits every leaf node (nodes with a registered handler) in depth-first order.
func (n *node) walk(fn func(pattern string, handler http.Handler)) {
	if n.handler != nil && n.pattern != "" {
		fn(n.pattern, n.handler)
	}
	for _, child := range n.children {
		child.walk(fn)
	}
}

func longestCommonPrefix(a, b string) int {
	limit := min(len(a), len(b))
	for i := range limit {
		if a[i] != b[i] {
			return i
		}
	}
	return limit
}

// findWildcard finds the first wildcard token in path.
// Recognised forms: :name, *name, {name:expr}.
func findWildcard(path string) (token string, start int, valid bool) {
	for i, c := range []byte(path) {
		switch c {
		case ':':
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

		case '*':
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

		case '{':
			// Scan for the matching '}', rejecting nested '{'.
			depth := 1
			valid = true
			for j, ch := range []byte(path[i+1:]) {
				switch ch {
				case '{':
					valid = false
					depth++
				case '}':
					depth--
					if depth == 0 {
						return path[i : i+1+j+1], i, valid
					}
				}
			}
			// No closing '}' found.
			return "", -1, false
		}
	}
	return "", -1, false
}

// expandOptional detects a {/:name} or {/:name:expr} optional segment and returns
// the two expanded paths (without and with the segment). Returns (nil, false) when
// no optional segment is found.
func expandOptional(path string) ([]string, bool) {
	i := strings.Index(path, "{/:")
	if i < 0 {
		return nil, false
	}
	j := strings.Index(path[i:], "}")
	if j < 0 {
		panic("muxmaster: unclosed { in path '" + path + "'")
	}
	j += i

	inner := path[i+1 : j] // "/:name" or "/:name:expr"

	// Path without the optional segment.
	without := path[:i] + path[j+1:]
	if without == "" {
		without = "/"
	}

	// inner starts with "/", so inner[1:] is ":name" or ":name:expr".
	seg := inner[1:]

	// Detect regex optional {/:name:expr}: find a second colon after the leading ':'.
	colonIdx := strings.Index(seg[1:], ":")
	var paramToken string
	if colonIdx >= 0 {
		// ":name:expr" → convert to "{name:expr}"
		name := seg[1 : 1+colonIdx]
		expr := seg[2+colonIdx:]
		paramToken = "{" + name + ":" + expr + "}"
	} else {
		paramToken = seg // keep as ":name"
	}

	withOpt := path[:i] + "/" + paramToken + path[j+1:]
	return []string{without, withOpt}, true
}
