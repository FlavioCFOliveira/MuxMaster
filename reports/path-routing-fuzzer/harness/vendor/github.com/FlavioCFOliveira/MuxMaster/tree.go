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
//
// maxParams is the number of params held inline on the stack. Routes with more
// than maxParams parameters spill into the overflow slice (one heap alloc), but
// those routes already pay a heap alloc for the reqBundle, so the extra cost is
// negligible. maxParams=3 covers ≥99% of real-world REST API routes while
// keeping the stack frame 160 B smaller than the previous maxParams=8.
const maxParams = 3

type paramsBuf struct {
	count    int
	buf      [maxParams]Param
	overflow []Param // populated only for routes with > maxParams params
}

// add appends a param to the buffer. The first maxParams params are stored
// inline on the stack; additional params spill to a heap-allocated overflow
// slice (one alloc per request, only for deep routes).
func (pb *paramsBuf) add(key, value string) {
	if pb.count < maxParams {
		pb.buf[pb.count] = Param{Key: key, Value: value}
	} else {
		pb.overflow = append(pb.overflow, Param{Key: key, Value: value})
	}
	pb.count++
}

// params returns the captured params as a Params slice. For routes with
// ≤ maxParams params the slice is backed by the inline stack array. For
// deeper routes the inline and overflow portions are concatenated into the
// overflow slice (one extra alloc, acceptable for the rare >3-param case).
//
// The returned slice must not be used after the paramsBuf goes out of scope
// (for the inline case — the overflow case is heap-allocated and is fine).
func (pb *paramsBuf) params() Params {
	if len(pb.overflow) == 0 {
		return Params(pb.buf[:pb.count])
	}
	// Combine inline and overflow into a single contiguous slice. Prepend the
	// inline params so the caller sees them in insertion order.
	all := make(Params, pb.count)
	copy(all, pb.buf[:maxParams])
	copy(all[maxParams:], pb.overflow)
	return all
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
// `fast`, `pattern`, `priority`, `nType`, `wildChild`, `regexp` are cold — CL1+.
type node struct {
	// --- Cache line 0 (offsets 0-63) ---
	path     string       // 16 bytes @ 0
	handler  http.Handler // 16 bytes @ 16  (moved from CL1 — hot on leaf match)
	indices  string       // 16 bytes @ 32
	children []*node      // 24 bytes @ 48-71 (crosses into CL1)

	// --- Cache line 1+ ---
	fast          FastHandler    // 16 bytes — nil for normal routes, set for HandleFast routes
	pattern       string         // 16 bytes (cold on lookup; set on leaves)
	priority      uint32         // 4 bytes  (written only during registration)
	nType         nodeType       // 1 byte
	wildChild     bool           // 1 byte
	regexpNameEnd uint8          // 1 byte   (end index of param name in path for regexParam nodes)
	maxParams     uint8          // 1 byte   (max wildcard depth in any path through this subtree)
	regexp        *regexp.Regexp // 8 bytes  (regex routes only)
}

// countPatternParams returns the number of wildcard tokens (:name, *name,
// {name:expr}) in a registration pattern. Used to update a tree's maxParams
// incrementally (see the defer in addRouteInternal) instead of walking the
// whole tree on every registration ([waste-hunt WH-08] / performance.md §36).
func countPatternParams(p string) int {
	n := 0
	for {
		wc, i, _ := findWildcard(p)
		if i < 0 {
			return n
		}
		n++
		p = p[i+max(len(wc), 1):]
	}
}

// copyNode returns a private copy of n suitable for mutation during the
// current registration: a shallow struct copy plus a freshly allocated
// children slice. The fresh slice matters because a shallow struct copy
// alone would still share n's children backing array — an in-place append
// or reorder on the copy could then write into memory a published tree is
// concurrently reading through n. n's own children (the *node pointers held
// in that fresh slice) remain shared with the original until the insertion
// walk actually descends into one of them, at which point that specific
// child is copied the same way and written back into the parent's (already
// private) children slice.
//
// This is the path-copying strategy of performance.md §36: only the nodes
// on the path from the root to the point of insertion are ever copied;
// every subtree the walk does not visit stays shared, unmodified, between
// the previously published tree and the one being built. A panic partway
// through a registration discards every node copied so far — none of them
// are reachable from any published tree until the registration completes
// and calls treesPtr.Store — leaving the live tree completely intact
// (MM-2026-0033).
func copyNode(n *node) *node {
	c := new(node)
	*c = *n
	if n.children != nil {
		c.children = append([]*node(nil), n.children...)
	}
	return c
}

// addRoute registers an http.Handler for the given path.
func (n *node) addRoute(path string, handler http.Handler) {
	n.addRouteInternal(path, handler, nil)
}

// addRouteFast registers a FastHandler for the given path.
func (n *node) addRouteFast(path string, fast FastHandler) {
	n.addRouteInternal(path, nil, fast)
}

// maxOptionalSegments caps the number of optional segments in a single
// pattern. Each optional segment doubles the number of expanded routes, so a
// pattern with N optional segments creates 2^N routes. The cap of 8 keeps
// expansion bounded at 256 calls — enough for realistic templates while
// blocking the DoS vector documented as MM-2026-0050 / PRF-2026-0007 where
// an attacker controlling pattern registration could submit
// /a{/:1}{/:2}…{/:20} (~2^20 expansions, multi-second registration).
const maxOptionalSegments = 8

// addRouteInternal registers either an http.Handler or a FastHandler (exactly
// one must be non-nil) for the given path, expanding optional segments first.
func (n *node) addRouteInternal(path string, handler http.Handler, fast FastHandler) {
	// Bound optional-segment expansion before recursing — each {/:name}
	// doubles the number of registrations, so cap the count to keep the
	// total expansion at 2^maxOptionalSegments.
	if c := countOptionalSegments(path); c > maxOptionalSegments {
		panic("muxmaster: pattern '" + path + "' has " + strconv.Itoa(c) +
			" optional segments; the maximum is " + strconv.Itoa(maxOptionalSegments) +
			" to prevent exponential addRoute time complexity (DoS).")
	}
	// Expand optional segments before doing anything else.
	if expanded, ok := expandOptional(path); ok {
		n.addRouteInternal(expanded[0], handler, fast)
		n.addRouteInternal(expanded[1], handler, fast)
		// Each recursive call carries its own defer that updates maxParams.
		return
	}

	fullPath := path
	if !utf8.ValidString(path) {
		panic("muxmaster: path contains invalid UTF-8: " + strconv.QuoteToASCII(path))
	}

	// After the route is fully inserted, refresh maxParams on the root node so
	// that dispatch can skip paramsBuf allocation for purely static trees.
	// Capture origRoot here — n is reassigned during tree traversal below.
	// This runs at registration time only — never in the hot path.
	//
	// Routes are only ever added, never removed, so the maximum wildcard
	// depth over all paths can only grow: fold the new pattern's own
	// wildcard count into the root's value incrementally instead of
	// re-walking the entire tree on every registration ([waste-hunt WH-08]).
	// Saturates at 255 (the previous uint8 sum wrapped at the same bound).
	origRoot := n
	defer func() {
		if c := countPatternParams(fullPath); c > int(origRoot.maxParams) {
			origRoot.maxParams = uint8(min(c, 255))
		}
	}()

	n.priority++

	if n.path == "" && n.indices == "" {
		n.insertChild(path, fullPath, handler, fast)
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
				fast:      n.fast,
				priority:  n.priority - 1,
				pattern:   n.pattern, // split child inherits the original pattern
				regexp:    n.regexp,
			}
			n.children = []*node{child}
			// Wrap the raw byte in a single-element []byte to avoid the
			// rune-coercion path of string(byte) which would re-encode any
			// non-ASCII byte (>= 0x80) as a 2-byte UTF-8 sequence and
			// desynchronise len(n.indices) from len(n.children) — see
			// PRF-2026-0009 for the multi-byte panic this fix prevents.
			n.indices = string([]byte{n.path[i]})
			n.path = path[:i]
			n.handler = nil
			n.fast = nil
			n.pattern = ""
			n.wildChild = false
		}

		if i < len(path) {
			path = path[i:]
			c := path[0]

			if n.nType == param && c == '/' && len(n.children) == 1 {
				// Descend into the existing child: copy it first so the
				// priority bump below never mutates a node a published
				// tree might still be reading (path copying, see copyNode).
				child := copyNode(n.children[0])
				n.children[0] = child
				n = child
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
				// routing.md §4.2 (rule 48): static routes always outrank named
				// parameters and catch-alls in matching precedence, and §5 lists
				// no panic for a static/named-parameter sibling pair — only for
				// two wildcards at the same position (rule 71) or a catch-all
				// sharing a root segment with an existing handler (rule 68,
				// handled separately in insertChild). A static sibling of an
				// existing param/regexParam wildchild is therefore a legal
				// registration, order-independent: it must succeed whether the
				// static or the param route was registered first (MM-2026-0256).
				//
				// A catch-all wildchild is different: it already panics via
				// rule 68 when registered AFTER a sibling forces this split, so
				// registering the sibling AFTER the catch-all must panic too,
				// for the same order-independent outcome — keep that case as a
				// hard conflict.
				if n.wildChild && n.children[len(n.children)-1].nType == wildcard {
					seg := strings.SplitN(path, "/", 2)[0]
					pfx := fullPath[:strings.Index(fullPath, seg)] + n.children[len(n.children)-1].path
					panic("muxmaster: '" + seg + "' in path '" + fullPath +
						"' conflicts with existing wildcard '" + pfx + "'")
				}
				n.indices += string([]byte{c}) // raw byte, not rune (PRF-2026-0009)
				child := &node{}
				if n.wildChild {
					// Insert the new static child just before the wildchild so
					// the "wildchild is always last" invariant getValue relies
					// on (n.children[len(n.children)-1]) keeps holding.
					last := len(n.children) - 1
					wild := n.children[last]
					n.children[last] = child
					n.children = append(n.children, wild)
				} else {
					n.children = append(n.children, child)
				}
				// incrementChildPrio replaces the child with its own copy
				// before bumping priority (path copying, see copyNode) — it
				// may not be the same *node as the local `child` above, so
				// re-read it from n.children at the position it returns.
				j := n.incrementChildPrio(len(n.indices) - 1)
				n = n.children[j]
			} else if n.wildChild {
				// Descend into the existing wildcard child: copy it first
				// (path copying, see copyNode) so the priority bump and the
				// conflict check below never mutate a published node.
				last := len(n.children) - 1
				child := copyNode(n.children[last])
				n.children[last] = child
				n = child
				n.priority++

				if len(path) >= len(n.path) &&
					n.path == path[:len(n.path)] &&
					n.nType != wildcard &&
					(len(n.path) >= len(path) || path[len(n.path)] == '/') {
					continue walk
				}

				seg := strings.SplitN(path, "/", 2)[0]
				pfx := fullPath[:strings.Index(fullPath, seg)] + n.path
				panic("muxmaster: '" + seg + "' in path '" + fullPath +
					"' conflicts with existing wildcard '" + pfx + "'")
			}

			n.insertChild(path, fullPath, handler, fast)
			return
		}

		if n.handler != nil || n.fast != nil {
			panic("muxmaster: a handler is already registered for path '" + fullPath + "'")
		}
		n.handler = handler
		n.fast = fast
		n.pattern = fullPath
		return
	}
}

// incrementChildPrio bumps the priority of the child at pos and reorders
// n.children/n.indices to keep higher-priority children earlier, for faster
// average-case lookups. n.children is assumed to already be n's own private
// slice (path copying, see copyNode). The child at pos is copied before its
// priority is mutated — everything else this function does is pointer
// rearrangement within n's own slice, which never touches a published node.
func (n *node) incrementChildPrio(pos int) int {
	cs := n.children
	child := copyNode(cs[pos])
	child.priority++
	cs[pos] = child
	prio := child.priority

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

func (n *node) insertChild(path, fullPath string, handler http.Handler, fast FastHandler) {
	for {
		wc, i, valid := findWildcard(path)
		if i < 0 {
			break
		}
		if !valid {
			panic("muxmaster: only one wildcard per path segment is allowed in '" + fullPath + "'")
		}
		if len(wc) < 2 {
			panic("muxmaster: wildcards must be named in path '" + fullPath + "'")
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
			n.fast = fast
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
			nameEnd := 1 + colonIdx
			// regexpNameEnd is uint8 (0..255). The name slice ends at this
			// index, exclusive — so the maximum supported name length is
			// 254 bytes (uint8(256) would wrap to 0). PRF-2026-0003 fixed
			// the off-by-one in the panic message.
			if nameEnd > 255 {
				panic("muxmaster: regex param name must be at most 254 bytes in '" + fullPath + "'")
			}
			child := &node{nType: regexParam, path: wc, regexp: re, regexpNameEnd: uint8(nameEnd)}
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
			n.fast = fast
			n.pattern = fullPath
			return
		}

		// Catch-all '*'
		if i+len(wc) != len(path) {
			panic("muxmaster: catch-all routes are only allowed at the end of the path in '" + fullPath + "'")
		}
		if len(n.path) > 0 && n.path[len(n.path)-1] == '/' {
			panic("muxmaster: catch-all conflicts with existing handler for the path root in '" + fullPath + "'")
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
			fast:     fast,
			pattern:  fullPath,
			priority: 1,
		}
		n.children = []*node{leaf}
		return
	}

	n.path = path
	n.handler = handler
	n.fast = fast
	n.pattern = fullPath
}

// backtrackInline is how many fork frames getValueBacktrack tracks inline,
// as bare scalar locals in its own stack frame — not fields of an
// address-taken struct (see getValue's doc comment for the rejected
// designs this replaced). A single static-vs-wildchild collision (e.g.
// "/users/list" registered alongside "/users/:id", rule 48/49's own
// motivating example) is by far the common case in real route sets, and
// resolves via an immediate static match in getValue's own inlined walk —
// getValueBacktrack, where this constant applies, is only ever entered
// when that optimistic walk fails. backtrackInline=2 keeps
// the single- and double-collision RETRY cases (by far the common shapes,
// on the already-rare path that needs a retry at all) allocation-free;
// only trees forking more than backtrackInline times along one retried
// lookup pay the (rarer still) overflow allocation. See
// reports/perf-lab-2026-09-24/waste-hunt/results/fixes/259.txt for the
// benchmark evidence behind this constant.
//
// This constant is documentary, not a generic array-size template:
// getValueBacktrack hand-declares exactly backtrackInline (2) named scalar
// frames (frame0*/frame1*). Changing this constant without also adding or
// removing the matching frameN* block and fail: case there would silently
// desynchronise the two — there is intentionally no single array indexed by
// it, because a dynamically-indexed array would force an address-taken
// aggregate again.
const backtrackInline = 2

// backtrackFrame records one static-vs-wildchild fork getValueBacktrack can
// resume from — via the parent node's wildchild sibling — if the static
// branch it chose first ultimately fails to produce a match. It is used
// only (a) as the element type of the rare, heap-backed overflow slice for
// forks past backtrackInline, and (b) as the argument/return shape of
// pushOverflowFrame/popOverflowFrame. The first backtrackInline frames
// themselves are never stored in a backtrackFrame value — see getValue's
// doc comment.
type backtrackFrame struct {
	parent      *node
	path        string
	paramCount  int
	overflowLen int
}

// pushOverflowFrame handles the rare case of more than backtrackInline
// simultaneous forks along one lookup: the frame is built as a plain value
// and appended to the heap-backed overflow slice (allocated by append on
// first use — see getValueBacktrack's overflow local). Marked noinline and
// called directly from getValueBacktrack so this heap-allocating slice growth never adds
// so much as a call site's worth of code to the hot, allocation-free path
// that handles the first backtrackInline forks.
//
//go:noinline
func pushOverflowFrame(overflow *[]backtrackFrame, parent *node, path string, params *paramsBuf) {
	f := backtrackFrame{parent: parent, path: path}
	if params != nil {
		f.paramCount = params.count
		f.overflowLen = len(params.overflow)
	}
	*overflow = append(*overflow, f)
}

// popOverflowFrame removes and returns the most recently pushed overflow
// frame (LIFO: the last element of *overflow). Marked noinline for the same
// reason as pushOverflowFrame — its cost is paid only on the (already
// rarer) overflow path, never on the first backtrackInline forks.
//
//go:noinline
func popOverflowFrame(overflow *[]backtrackFrame) backtrackFrame {
	s := *overflow
	f := s[len(s)-1]
	*overflow = s[:len(s)-1]
	return f
}

// Why no cap is needed (linear-in-tree-size proof):
//
// Claim: a single getValueBacktrack call enters each tree node at most once, and
// therefore pushes at most one frame per node. Total work across an
// entire call — including every abandoned (backtracked-out-of) branch —
// is bounded by the total number of nodes in the tree, a fixed, build-time
// quantity set by the registered route table, never by request input.
//
// Proof:
//  1. A node N is entered in exactly one of two ways: (a) normal forward
//     descent from its parent (the static-match branch, or the wildchild
//     fallthrough when no static child matches), or (b) a backtrack
//     resume, which always targets a SPECIFIC parent's wildchild — the
//     exact child pointer recorded when that parent's frame was pushed.
//  2. A frame is pushed for a node N at most once: the push fires only at
//     the instant N is entered via normal forward descent AND N has a
//     matching static child AND N.wildChild is true. If that frame is
//     later popped (N's static subtree having failed all the way through),
//     the walk resumes directly at N's wildchild via viaWildchild=true —
//     it never re-examines N's own static children, so N cannot be pushed
//     a second time. If the frame is never popped (the static subtree
//     succeeds, or an enclosing frame is retried and matches first), N's
//     wildchild is simply never visited through this frame at all.
//  3. A trie node has exactly one parent, so N cannot be reached any way
//     other than (1) — there is no aliasing and no shared substructure
//     between two different forks' subtrees; each fork's wildchild subtree
//     is a disjoint set of descendant nodes.
//  4. Therefore the number of node-entries in one getValueBacktrack call equals the
//     number of DISTINCT nodes visited, which cannot exceed the tree's
//     total node count — a hard structural ceiling independent of the
//     specific request.
//
// Consequence: worst-case cost per request is O(N), N = total node count
// of the registered tree for that HTTP method — fixed at startup by the
// operator's own route table, not amplifiable by request content. This is
// polynomial (linear in tree size), never exponential: each fork
// contributes at most ONE retried alternative, not a combinatorial product
// of alternatives, unlike classical catastrophic-backtracking regexes,
// which repeatedly re-explore overlapping subproblems — this trie has no
// overlap by construction (point 3). There is deliberately no depth cap:
// see reports/perf-lab-2026-09-24/waste-hunt/results/fixes/260.txt for the
// empirical confirmation, including a harsher construction (each abandoned
// branch itself O(depth) deep) that scales quadratically in the number of
// forks — still polynomial, still tied to registered tree size, never
// exponential.

// getValue looks up the handler for path.
// ci enables case-insensitive static prefix matching.
// Returns the http.Handler (or nil), the FastHandler (or nil), the registered
// route pattern, and a trailing-slash-redirect hint. Exactly one of handler
// and fast will be non-nil when a route is found.
// params may be nil (for static-route fast path) or point to a stack-allocated paramsBuf.
//
// rmp #261 (waste-hunt gate follow-up to DIV-001 / rmp #259): the walk below
// is byte-for-byte the pre-DIV-001 walk (see git history prior to rmp #259)
// plus a single extra local, couldBacktrack, set true at the exact point a
// static child is chosen over an available wildchild sibling — a fork that
// MIGHT need to be revisited, not a commitment to revisit it. If the walk
// finds a handler, or fails with couldBacktrack still false (no wildchild
// sibling was ever skipped anywhere on the walk, so the failure is already
// authoritative — this is the common NotFound shape too), getValue returns
// directly. Only when the walk fails AND a fork was skipped does getValue
// discard that (now-known-unreliable) result, rewind params, and call
// getValueBacktrack — the full, tested bounded-backtracking walk, in its
// own noinline function — from the root. This redoes the work for that one,
// already-rare case (routing through a static/param collision that the
// static branch could not actually satisfy), instead of paying any
// backtracking bookkeeping cost on every call.
//
// Two earlier versions of this fix were measured and rejected:
//   - `var stack backtrackStack` declared unconditionally at the top of a
//     single getValue, doing the whole walk in one pass: because its pop()
//     had a pointer receiver and its push site took
//     `&stack.inline[stack.n]`, the ~112-byte stack value was address-taken,
//     forcing the compiler to pre-zero it (a DUFFZERO-class block store) on
//     EVERY call so the garbage collector never observes garbage pointer
//     bits in it — whether or not a fork was ever taken. Benchstat:
//     +18-22% ns/op on StaticRoute/FastStaticRoute/ParallelStaticRoute
//     (mixed trees where the looked-up route always resolves via its
//     static child, but a wildchild sibling is ALSO present at that node,
//     so DIV-001 correctness requires recording a fallback before
//     committing even though it is never used), 0 allocs/op change.
//   - The same one-pass shape, but with the first backtrackInline frames
//     replaced by independent named scalar locals instead of an
//     address-taken struct: benchmarked WORSE (+28% on StaticRoute) — more
//     branches and more register/spill traffic than the original
//     array-indexed writes, for a route that forks on every single call
//     regardless of how cheaply the fork is recorded, since forking here is
//     an unavoidable consequence of DIV-001's correctness requirement, not
//     an avoidable cost.
//   - A split getValueFast (a separate, un-inlined function doing exactly
//     the walk below) called unconditionally from a thin getValue wrapper:
//     removed the backtracking cost as intended (StaticRoute regression
//     roughly halved, to +10.5%), but the mandatory extra function-call
//     boundary — paid on every single lookup, forking or not — made the
//     ALREADY-FINE param/pooled/fast paths measurably worse (e.g.
//     PooledParamRoute1 +9% -> +28%) than either of the one-pass versions
//     above. Net loss.
//
// The version below avoids all three costs at once: the walk is inlined
// directly into getValue (no call boundary for the common path), and
// couldBacktrack is a single plain bool — never address-taken, never part
// of a struct — so it costs the same one word as any other bool local in
// this function (tsr, viaWildchild), not a block zero. Verify with
// `go build -gcflags=-S .`: getValue's disassembly for a non-forking or
// successfully-resolved-via-static input contains no DUFFZERO/block-zero
// for fork bookkeeping, because getValueBacktrack — the only place that
// bookkeeping exists — is a separate, noinline function never reached on
// that path.
//
// TSR hints: sawTSR is not needed here (unlike getValueBacktrack) because
// this walk never retries — if it fails, either the failure is
// authoritative (couldBacktrack false, tsr as set by the single failed
// attempt is already correct) or the result is discarded entirely in favour
// of getValueBacktrack's own, independently-correct sawTSR accumulation.
func (n *node) getValue(path string, params *paramsBuf, ci bool) (handler http.Handler, fast FastHandler, pattern string, tsr bool) {
	origN, origPath := n, path
	var couldBacktrack bool
	viaWildchild := false
walk:
	for {
		if !viaWildchild {
			prefix := n.path

			if len(path) > len(prefix) {
				if !prefixMatch(path[:len(prefix)], prefix, ci) {
					break walk
				}
				path = path[len(prefix):]

				// Always try static children first so that /users/list beats /users/:id.
				// Opt O3: avoid the slice header construction `children := n.children[:len(n.indices)]`
				// that the previous code wrote on every walk iteration. The loop already bounds j by
				// len(n.indices); n.children has at least that many elements (registration invariant),
				// so n.children[j] is in-bounds without the intermediate slice.
				c := path[0]
				for j := range len(n.indices) {
					if foldEq(c, n.indices[j], ci) {
						// DIV-001 (rmp #259): a wildchild sibling here means this
						// commitment to the static child MIGHT be wrong — the
						// caller (getValue) must redo the walk with full
						// backtracking if this attempt ultimately fails. A
						// single bool, set at most once per call in the
						// overwhelmingly common case (see getValue's doc
						// comment for the measured cost of trying to do more
						// than this here).
						if n.wildChild {
							couldBacktrack = true
						}
						n = n.children[j]
						continue walk
					}
				}

				if !n.wildChild {
					tsr = path == "/" && (n.handler != nil || n.fast != nil)
					break walk
				}

				// No static child matched — fall through to wildchild (always last).
				n = n.children[len(n.children)-1]
				viaWildchild = true
				continue walk
			}

			if prefixMatch(path, prefix, ci) && len(path) == len(prefix) {
				handler = n.handler
				fast = n.fast
				pattern = n.pattern
				if handler != nil || fast != nil {
					break walk
				}
				for j := range len(n.indices) {
					if n.indices[j] == '/' {
						n = n.children[j]
						// n is either a genuine static "/" node with its own
						// handler, or the catch-all wrapper node (wildChild=true,
						// nType=static, empty path — see addRoute) whose single
						// child is the actual wildcard leaf. The wrapper itself
						// never carries a handler, so its wildcard CHILD must be
						// inspected, not the wrapper's own (always static) nType.
						// Fixes rmp #255: "/assets" against "/assets/*filepath",
						// and Mount's bare prefix (specification/groups.md §28),
						// both 404'd instead of TSR-redirecting to the trailing
						// slash form.
						tsr = (n.path == "/" && (n.handler != nil || n.fast != nil)) ||
							(n.wildChild && len(n.children) > 0 && n.children[0].nType == wildcard &&
								(n.children[0].handler != nil || n.children[0].fast != nil))
						break walk
					}
				}
				tsr = path == "/" ||
					(len(n.indices) == 1 && n.indices[0] == '/' && (n.children[0].handler != nil || n.children[0].fast != nil))
				break walk
			}

			tsr = (path == "/" ||
				(len(prefix) == len(path)+1 &&
					prefix[len(path)] == '/' &&
					prefixMatch(path, prefix[:len(prefix)-1], ci) &&
					(n.handler != nil || n.fast != nil)))
			break walk
		}

		// Dispatch through the wildchild n was set to, either by falling
		// through normally above (viaWildchild set just before continue
		// walk) or by a genuine wildcard-chain descent below.
		viaWildchild = false

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
				fast = n.fast
				pattern = n.pattern
				if handler == nil && fast == nil && len(n.children) == 1 {
					n = n.children[0]
					tsr = n.path == "/" && (n.handler != nil || n.fast != nil)
				}
				break walk
			}
			if len(n.children) > 0 {
				path = path[end:]
				n = n.children[0]
				continue walk
			}
			tsr = len(path) == end+1
			break walk

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
				break walk
			}
			// Param name is between '{' and ':' in n.path, e.g. "{name:expr}".
			// regexpNameEnd is pre-computed at registration — no strings.Index here.
			name := n.path[1:n.regexpNameEnd]
			if params != nil {
				params.add(name, seg)
			}
			if end == len(path) {
				handler = n.handler
				fast = n.fast
				pattern = n.pattern
				if handler == nil && fast == nil && len(n.children) == 1 {
					n = n.children[0]
					tsr = n.path == "/" && (n.handler != nil || n.fast != nil)
				}
				break walk
			}
			if len(n.children) > 0 {
				path = path[end:]
				n = n.children[0]
				continue walk
			}
			tsr = len(path) == end+1
			break walk

		case wildcard:
			if params != nil {
				params.add(n.path[2:], path)
			}
			handler = n.handler
			fast = n.fast
			pattern = n.pattern
			break walk

		default:
			panic("muxmaster: invalid node type")
		}
	}

	if handler != nil || fast != nil || !couldBacktrack {
		return
	}
	// The walk above greedily committed to at least one static child that
	// had an unexplored wildchild sibling, and still failed to find a
	// match — its result (and any params it captured along the way) is not
	// authoritative. Discard it and redo the whole lookup from the root
	// with full backtracking. params must be rewound to empty first: a
	// partially-filled paramsBuf from the abandoned attempt would otherwise
	// leak into (or be double-counted by) the fresh walk. origN/origPath
	// (not n/path, which the walk above mutated while descending) are the
	// root and full path this getValue call was actually invoked with.
	if params != nil {
		params.count = 0
		params.overflow = params.overflow[:0]
	}
	return origN.getValueBacktrack(origPath, params, ci)
}

// getValueBacktrack is the full bounded-backtracking walk (rmp #259 /
// DIV-001): entered only from getValue, only when getValue's own inlined,
// optimistic walk committed to at least one static child that had an
// unexplored wildchild sibling and still failed to find a match. Marked
// noinline so its bookkeeping (the frame0*/frame1* locals and the rare
// overflow slice below) never becomes part of getValue's own compiled code
// — it is a deliberately separate, rarely-entered function, not an
// inlining candidate.
//
// At every point where a static child is chosen over an available
// wildchild sibling, it records a fallback point (node + remaining path +
// params count) before committing. If the chosen branch ultimately fails to
// produce a match — control reaches the `fail:` label with handler == fast
// == nil — the most recently recorded fallback is popped and retried via
// its wildchild instead (viaWildchild = true resumes the switch below, not
// the generic prefix-matching branch, since a wildchild's raw path field
// like ":id" or "{id:\d+}" is not a literal prefix to match). This repeats
// until a match is found or no fallback remains.
//
// TSR hints from a failed attempt are preserved (OR'd) across retries via
// sawTSR: a static branch that itself doesn't match but reports a
// trailing-slash redirect opportunity must not lose that hint just because
// a later wildchild fallback attempt also fails to match — rule 48's
// static priority is honoured by trying static first and keeping ITS tsr
// hint unless a later attempt produces an actual handler match (which
// always wins outright via the early `return`s below).
//
// The first backtrackInline (2) forks are tracked as independent, named
// scalar locals (frame0Parent/frame0Path/..., frame1Parent/frame1Path/...)
// plus a plain `stackN int` depth counter, rather than a dynamically-indexed
// array field on an address-taken struct — see getValue's doc comment for
// why that distinction mattered when this walk ran unconditionally on every
// lookup. Now that it only runs on the rare backtrack-needed path, the
// distinction is no longer performance-critical, but the scalar form is
// kept: it is no worse, and avoids reintroducing an address-taken
// aggregate.
//
//go:noinline
func (n *node) getValueBacktrack(path string, params *paramsBuf, ci bool) (handler http.Handler, fast FastHandler, pattern string, tsr bool) {
	var sawTSR bool
	viaWildchild := false

	var stackN int
	var frame0Parent, frame1Parent *node
	var frame0Path, frame1Path string
	var frame0ParamCount, frame0OverflowLen int
	var frame1ParamCount, frame1OverflowLen int
	var overflow []backtrackFrame

walk:
	for {
		if !viaWildchild {
			prefix := n.path

			if len(path) > len(prefix) {
				if !prefixMatch(path[:len(prefix)], prefix, ci) {
					goto fail
				}
				path = path[len(prefix):]

				c := path[0]
				for j := range len(n.indices) {
					if foldEq(c, n.indices[j], ci) {
						if n.wildChild {
							switch stackN {
							case 0:
								frame0Parent = n
								frame0Path = path
								if params != nil {
									frame0ParamCount = params.count
									frame0OverflowLen = len(params.overflow)
								}
							case 1:
								frame1Parent = n
								frame1Path = path
								if params != nil {
									frame1ParamCount = params.count
									frame1OverflowLen = len(params.overflow)
								}
							default:
								pushOverflowFrame(&overflow, n, path, params)
							}
							stackN++
						}
						n = n.children[j]
						continue walk
					}
				}

				if !n.wildChild {
					tsr = path == "/" && (n.handler != nil || n.fast != nil)
					goto fail
				}

				n = n.children[len(n.children)-1]
				viaWildchild = true
				continue walk
			}

			if prefixMatch(path, prefix, ci) && len(path) == len(prefix) {
				handler = n.handler
				fast = n.fast
				pattern = n.pattern
				if handler != nil || fast != nil {
					return
				}
				for j := range len(n.indices) {
					if n.indices[j] == '/' {
						n = n.children[j]
						tsr = (n.path == "/" && (n.handler != nil || n.fast != nil)) ||
							(n.wildChild && len(n.children) > 0 && n.children[0].nType == wildcard &&
								(n.children[0].handler != nil || n.children[0].fast != nil))
						goto fail
					}
				}
				tsr = path == "/" ||
					(len(n.indices) == 1 && n.indices[0] == '/' && (n.children[0].handler != nil || n.children[0].fast != nil))
				goto fail
			}

			tsr = (path == "/" ||
				(len(prefix) == len(path)+1 &&
					prefix[len(path)] == '/' &&
					prefixMatch(path, prefix[:len(prefix)-1], ci) &&
					(n.handler != nil || n.fast != nil)))
			goto fail
		}

		viaWildchild = false

		switch n.nType {
		case param:
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
				fast = n.fast
				pattern = n.pattern
				if handler == nil && fast == nil && len(n.children) == 1 {
					n = n.children[0]
					tsr = n.path == "/" && (n.handler != nil || n.fast != nil)
				}
				if handler != nil || fast != nil {
					return
				}
				goto fail
			}
			if len(n.children) > 0 {
				path = path[end:]
				n = n.children[0]
				continue walk
			}
			tsr = len(path) == end+1
			goto fail

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
				goto fail
			}
			name := n.path[1:n.regexpNameEnd]
			if params != nil {
				params.add(name, seg)
			}
			if end == len(path) {
				handler = n.handler
				fast = n.fast
				pattern = n.pattern
				if handler == nil && fast == nil && len(n.children) == 1 {
					n = n.children[0]
					tsr = n.path == "/" && (n.handler != nil || n.fast != nil)
				}
				if handler != nil || fast != nil {
					return
				}
				goto fail
			}
			if len(n.children) > 0 {
				path = path[end:]
				n = n.children[0]
				continue walk
			}
			tsr = len(path) == end+1
			goto fail

		case wildcard:
			if params != nil {
				params.add(n.path[2:], path)
			}
			handler = n.handler
			fast = n.fast
			pattern = n.pattern
			if handler != nil || fast != nil {
				return
			}
			goto fail

		default:
			panic("muxmaster: invalid node type")
		}

	fail:
		sawTSR = sawTSR || tsr
		if stackN == 0 {
			handler = nil
			fast = nil
			pattern = ""
			tsr = sawTSR
			return
		}
		stackN--
		var parent *node
		var rpath string
		var pCount, oLen int
		switch {
		case stackN >= backtrackInline:
			f := popOverflowFrame(&overflow)
			parent, rpath, pCount, oLen = f.parent, f.path, f.paramCount, f.overflowLen
		case stackN == 1:
			parent, rpath, pCount, oLen = frame1Parent, frame1Path, frame1ParamCount, frame1OverflowLen
		default: // stackN == 0
			parent, rpath, pCount, oLen = frame0Parent, frame0Path, frame0ParamCount, frame0OverflowLen
		}
		if params != nil {
			params.count = pCount
			params.overflow = params.overflow[:oLen]
		}
		n = parent.children[len(parent.children)-1]
		path = rpath
		viaWildchild = true
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
	h, f, _, _ := n.getValue(path, nil, false)
	return h != nil || f != nil
}

// getValueStatic is the inline-eligible fast path used when the tree root has
// maxParams == 0 — i.e. when no node under this root captures path parameters
// (no param, regex, or wildcard children anywhere). With wildchild ruled out,
// the lookup degenerates to plain prefix walking + leaf match, with no params
// buffer, no switch, no type discrimination.
//
// Returns the matched handler/fast/pattern, and the TSR (trailing-slash-redirect)
// hint when applicable. The semantics mirror getValue exactly for static trees.
//
//go:nosplit
func (n *node) getValueStatic(path string, ci bool) (handler http.Handler, fast FastHandler, pattern string, tsr bool) {
walk:
	for {
		prefix := n.path
		if len(path) > len(prefix) {
			if !prefixMatch(path[:len(prefix)], prefix, ci) {
				return
			}
			path = path[len(prefix):]
			c := path[0]
			for j := range len(n.indices) {
				if foldEq(c, n.indices[j], ci) {
					n = n.children[j]
					continue walk
				}
			}
			tsr = path == "/" && (n.handler != nil || n.fast != nil)
			return
		}
		if prefixMatch(path, prefix, ci) && len(path) == len(prefix) {
			handler = n.handler
			fast = n.fast
			pattern = n.pattern
			if handler != nil || fast != nil {
				return
			}
			for j := range len(n.indices) {
				if n.indices[j] == '/' {
					n = n.children[j]
					tsr = n.path == "/" && (n.handler != nil || n.fast != nil)
					return
				}
			}
			tsr = path == "/" ||
				(len(n.indices) == 1 && n.indices[0] == '/' && (n.children[0].handler != nil || n.children[0].fast != nil))
			return
		}
		tsr = (path == "/" ||
			(len(prefix) == len(path)+1 &&
				prefix[len(path)] == '/' &&
				prefixMatch(path, prefix[:len(prefix)-1], ci) &&
				(n.handler != nil || n.fast != nil)))
		return
	}
}

// walk visits every leaf node (nodes with a registered handler or fast handler)
// in depth-first order.
func (n *node) walk(fn func(pattern string, handler http.Handler, fast FastHandler)) {
	if n.pattern != "" && (n.handler != nil || n.fast != nil) {
		fn(n.pattern, n.handler, n.fast)
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
			// FPE-2026-0001: locate the closing '}' that ends the {name:expr}
			// param token. The regex body may legitimately contain unbalanced
			// '}' characters (literal `}`, char classes `[}]`, escaped `\}`,
			// or quantifiers like `a{2,3}`), so a naive depth scan rejects
			// valid Go regexes. Treat the path-segment delimiter ('/' or end
			// of path) as the hard upper bound, and pick the LAST '}' inside
			// that segment as the closing brace. The shortest valid token
			// is `{a:x}` (5 bytes), so any '}' before that is too early.
			end := len(path)
			for k := i + 1; k < end; k++ {
				if path[k] == '/' {
					end = k
					break
				}
			}
			closeIdx := -1
			for k := end - 1; k > i; k-- {
				if path[k] == '}' {
					closeIdx = k
					break
				}
			}
			if closeIdx < 0 {
				// No closing '}' in this segment.
				return "", -1, false
			}
			return path[i : closeIdx+1], i, true
		}
	}
	return "", -1, false
}

// countOptionalSegments returns how many {/:name[:expr]} optional segments
// appear in path. Used to bound the 2^N expansion performed by recursive
// expandOptional calls.
func countOptionalSegments(path string) int {
	count := 0
	for i := 0; i < len(path); {
		j := strings.Index(path[i:], "{/:")
		if j < 0 {
			break
		}
		i += j
		closing := strings.Index(path[i:], "}")
		if closing < 0 {
			// Unclosed { — addRouteInternal/expandOptional will panic with a
			// dedicated message; do not double-count here.
			break
		}
		count++
		i += closing + 1
	}
	return count
}

// expandOptional detects a {/:name} or {/:name:expr} optional segment and returns
// the two expanded paths (without and with the segment). Returns (nil, false) when
// no optional segment is found.
//
// PRF-2026-0001: consecutive optional segments (e.g. /a{/:p1}{/:p2}) cannot
// be expanded into a coherent radix tree — both `:p1` and `:p2` would land
// at the same depth as wildcard children, triggering the "only one wildcard
// per path segment" invariant. Detect this at expansion time and panic with
// a clear message that points the operator at the supported pattern.
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

	// Reject consecutive optional segments: `}{/:`, possibly with no
	// literal between them. The expansion would produce sibling wildcards.
	if j+1 < len(path) && strings.HasPrefix(path[j+1:], "{/:") {
		panic("muxmaster: consecutive optional segments not supported in path '" + path +
			"' — separate optional segments with a literal segment, e.g. " +
			"/users{/:id}/posts{/:post}")
	}

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
