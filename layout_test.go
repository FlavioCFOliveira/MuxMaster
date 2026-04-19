package muxmaster

import (
	"testing"
	"unsafe"
)

// TestBundleLayoutSizes asserts the exact byte sizes of the tiered reqBundle
// types and their embedded requestCtx types.
//
// These assertions are intentionally strict: if a future change increases any
// bundle past its current GC size class, allocator cost rises for every
// param-route request. If a change decreases a bundle into a smaller size
// class, update the assertion and document the saving in CHANGELOG.
//
// Current size classes on amd64 (Go 1.26+, 8-byte pointer):
//
//	reqBundle1  392 B → GC size class 416 B   (1-param routes)
//	reqBundle2  424 B → GC size class 448 B   (2-param routes)
//	reqBundle   456 B → GC size class 480 B   (3+-param routes)
//
// Why 384 B is not achievable for reqBundle1:
//   - http.Request is 304 B (fixed by the stdlib).
//   - requestCtx1 needs: context.Context iface (16 B) + Params slice header
//     (24 B) + pattern string (16 B) + [1]Param (32 B) = 88 B.
//   - Removing pattern from requestCtx1 yields 72 B → bundle 376 B → class
//     384 B, but then RoutePattern() cannot be served from the bundle, which
//     would be a behavioral regression.  There is no internal padding to
//     reorganise; every byte is load-bearing.
//
// Therefore 416 B is the minimum size class compatible with the current API.
func TestBundleLayoutSizes(t *testing.T) {
	t.Run("requestCtx1", func(t *testing.T) {
		const want = 88
		if got := unsafe.Sizeof(requestCtx1{}); got != want {
			t.Errorf("requestCtx1 size = %d B, want %d B — struct layout changed", got, want)
		}
	})

	t.Run("reqBundle1", func(t *testing.T) {
		// 88 (requestCtx1) + 304 (http.Request) = 392 B → GC class 416 B.
		const want = 392
		if got := unsafe.Sizeof(reqBundle1{}); got != want {
			t.Errorf("reqBundle1 size = %d B, want %d B — GC size class may have changed", got, want)
		}
	})

	t.Run("requestCtx2", func(t *testing.T) {
		const want = 120
		if got := unsafe.Sizeof(requestCtx2{}); got != want {
			t.Errorf("requestCtx2 size = %d B, want %d B", got, want)
		}
	})

	t.Run("reqBundle2", func(t *testing.T) {
		// 120 (requestCtx2) + 304 (http.Request) = 424 B → GC class 448 B.
		const want = 424
		if got := unsafe.Sizeof(reqBundle2{}); got != want {
			t.Errorf("reqBundle2 size = %d B, want %d B — GC size class may have changed", got, want)
		}
	})

	t.Run("requestCtx", func(t *testing.T) {
		const want = 152
		if got := unsafe.Sizeof(requestCtx{}); got != want {
			t.Errorf("requestCtx size = %d B, want %d B", got, want)
		}
	})

	t.Run("reqBundle", func(t *testing.T) {
		// 152 (requestCtx) + 304 (http.Request) = 456 B → GC class 480 B.
		const want = 456
		if got := unsafe.Sizeof(reqBundle{}); got != want {
			t.Errorf("reqBundle size = %d B, want %d B — GC size class may have changed", got, want)
		}
	})
}

// TestBundleFieldOffsets asserts that each bundle embeds its ctx at offset 0
// and req immediately after ctx, with no padding between them.
//
// The unsafe ctx-field shortcut in setReqCtxUnsafe depends on the http.Request
// copy living at a stable, known offset within the bundle. Any padding inserted
// between ctx and req would silently break nothing (the offset is computed by
// the Go runtime), but it would waste memory — these assertions catch that.
func TestBundleFieldOffsets(t *testing.T) {
	t.Run("reqBundle1_ctx_at_zero", func(t *testing.T) {
		if off := unsafe.Offsetof(reqBundle1{}.ctx); off != 0 {
			t.Errorf("reqBundle1.ctx offset = %d, want 0", off)
		}
	})

	t.Run("reqBundle1_req_after_ctx", func(t *testing.T) {
		wantOff := unsafe.Sizeof(requestCtx1{}) // 88
		if got := unsafe.Offsetof(reqBundle1{}.req); got != wantOff {
			t.Errorf("reqBundle1.req offset = %d, want %d (sizeof requestCtx1)", got, wantOff)
		}
	})

	t.Run("reqBundle2_req_after_ctx", func(t *testing.T) {
		wantOff := unsafe.Sizeof(requestCtx2{}) // 120
		if got := unsafe.Offsetof(reqBundle2{}.req); got != wantOff {
			t.Errorf("reqBundle2.req offset = %d, want %d (sizeof requestCtx2)", got, wantOff)
		}
	})

	t.Run("reqBundle_req_after_ctx", func(t *testing.T) {
		wantOff := unsafe.Sizeof(requestCtx{}) // 152
		if got := unsafe.Offsetof(reqBundle{}.req); got != wantOff {
			t.Errorf("reqBundle.req offset = %d, want %d (sizeof requestCtx)", got, wantOff)
		}
	})
}
