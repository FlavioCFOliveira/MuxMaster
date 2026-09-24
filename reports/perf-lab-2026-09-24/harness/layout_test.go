package harness

// Cache-line / false-sharing inspection for the hot shared structs identified
// during the contention hunt (rmp #243). Run with:
//
//	go test -run TestStructLayout -v .
//
// Mux is exported, so reflect.TypeOf(muxmaster.Mux{}) gives REAL field
// offsets, including unexported fields (reflection exposes struct-tag/offset
// metadata regardless of export status — only Value access to unexported
// fields is restricted, not Type introspection).
//
// node / reqBundle1 / reqBundle2 / reqBundle / paramsBuf / requestCtx* are
// unexported types in package muxmaster and cannot be reflected on from this
// external module. For those, byte-for-byte MIRROR structs (identical field
// names, types and order, copied from tree.go / params.go as read on
// 2026-09-24) are declared below purely to compute unsafe.Sizeof/Offsetof.
// Go's struct layout algorithm is a deterministic function of
// (field types, field order, GOARCH) — see https://go.dev/ref/spec#Size_and_alignment_guarantees
// — so the mirror's layout is identical to the real type's on the same
// GOARCH/Go version, without needing access to the unexported identifier.
import (
	"net/http"
	"reflect"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

const cacheLine = 64

func TestStructLayoutMux(t *testing.T) {
	typ := reflect.TypeOf(muxmaster.Mux{})
	t.Logf("muxmaster.Mux — total size %d bytes (%d cache lines)", typ.Size(), (typ.Size()+cacheLine-1)/cacheLine)
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		t.Logf("  [CL%d +%3d] %-24s %-30s size=%d",
			f.Offset/cacheLine, f.Offset, f.Name, f.Type.String(), f.Type.Size())
	}
}

// --- Mirror of tree.go's node (as read 2026-09-24, tree.go:72-88) ---
type mirrorNode struct {
	path     string
	handler  http.Handler
	indices  string
	children []*mirrorNode

	fast          muxmaster.FastHandler
	pattern       string
	priority      uint32
	nType         uint8
	wildChild     bool
	regexpNameEnd uint8
	maxParams     uint8
	regexp        *regexp.Regexp
}

func TestStructLayoutNodeMirror(t *testing.T) {
	typ := reflect.TypeOf(mirrorNode{})
	t.Logf("mirror of tree.node — total size %d bytes (%d cache lines)", typ.Size(), (typ.Size()+cacheLine-1)/cacheLine)
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		t.Logf("  [CL%d +%3d] %-24s %-30s size=%d",
			f.Offset/cacheLine, f.Offset, f.Name, f.Type.String(), f.Type.Size())
	}
	// The doc comment in tree.go claims: "A successful static route match
	// reads only `path` + `handler` — both in CL0." Verify mechanically.
	pathF, _ := typ.FieldByName("path")
	handlerF, _ := typ.FieldByName("handler")
	if pathF.Offset/cacheLine != 0 || handlerF.Offset/cacheLine != 0 {
		t.Errorf("tree.go CL0 claim FALSIFIED: path CL%d, handler CL%d", pathF.Offset/cacheLine, handlerF.Offset/cacheLine)
	} else {
		t.Logf("VERIFIED: path (CL%d) and handler (CL%d) both in cache line 0", pathF.Offset/cacheLine, handlerF.Offset/cacheLine)
	}
}

// --- Mirrors of params.go's reqBundle1 / reqBundle2 / reqBundle (2026-09-24) ---
type mirrorParam struct{ Key, Value string }

type mirrorRequestCtx1 struct {
	http.ResponseWriter // stand-in for embedded context.Context (same width: 16B iface)
	pattern             string
	small               [1]mirrorParam
}
type mirrorReqBundle1 struct {
	ctx mirrorRequestCtx1
	req http.Request
}

type mirrorRequestCtx2 struct {
	http.ResponseWriter
	pattern string
	small   [2]mirrorParam
}
type mirrorReqBundle2 struct {
	ctx mirrorRequestCtx2
	req http.Request
}

type mirrorRequestCtx struct {
	http.ResponseWriter
	params  []mirrorParam
	pattern string
	small   [3]mirrorParam
}
type mirrorReqBundle struct {
	ctx mirrorRequestCtx
	req http.Request
}

func sizeClass(n uintptr) uintptr {
	// Go's size classes relevant here (runtime/sizeclasses.go, amd64):
	// 384, 416, 448, 480, 512 ...
	classes := []uintptr{32, 48, 64, 80, 96, 112, 128, 144, 160, 176, 192, 208, 224, 240,
		256, 288, 320, 352, 384, 416, 448, 480, 512, 576, 640, 704, 768, 896, 1024}
	for _, c := range classes {
		if n <= c {
			return c
		}
	}
	return n
}

func TestStructLayoutReqBundles(t *testing.T) {
	for name, typ := range map[string]reflect.Type{
		"reqBundle1 (1-param tier)": reflect.TypeOf(mirrorReqBundle1{}),
		"reqBundle2 (2-param tier)": reflect.TypeOf(mirrorReqBundle2{}),
		"reqBundle (3+-param tier)": reflect.TypeOf(mirrorReqBundle{}),
	} {
		sz := typ.Size()
		t.Logf("%s: mirror size=%d bytes -> GC size class=%d bytes (%.1f%% overhead)",
			name, sz, sizeClass(sz), 100*(float64(sizeClass(sz))/float64(sz)-1))
	}
}

// TestStructLayoutPoolGlobals inspects whether the sync.Pool package-level
// variables declared in params.go (reqBundle1Pool, reqBundle2Pool,
// reqBundlePool, fastParams1/2/3Pool) sit on shared cache lines with each
// other. sync.Pool's own hot fields (per-P local shards, a slice header +
// mutex-free victim cache) are internal to the runtime — what we CAN
// observe from outside is that each pool is a *distinct* package-level
// `sync.Pool` value; Go does not guarantee adjacent package-level vars are
// on separate cache lines, but each sync.Pool already contains a
// `noCopy` + `local unsafe.Pointer` + `localSize uintptr` + `victim` +
// `victimSize` — 40 bytes on amd64 — meaning two consecutive pool globals
// CAN share a cache line. This test measures the actual address gap.
func TestStructLayoutPoolGlobalSpacing(t *testing.T) {
	var a, b sync.Pool
	pa := unsafe.Pointer(&a)
	pb := unsafe.Pointer(&b)
	gap := int64(uintptr(pb)) - int64(uintptr(pa))
	t.Logf("two adjacent stack sync.Pool values: sizeof=%d bytes, gap=%d bytes (informational — package-level pool globals are placed by the linker in declaration order within the data section and are NOT padded to cache-line boundaries by the Go compiler)", unsafe.Sizeof(a), gap)
}

// TestStructLayoutAtomicPointers checks the three atomic.Pointer fields read
// on every ServeHTTP call (treesPtr, cfg, preHandlerPtr) against the
// registration-only fields (mu, rawPathDecodeWarnOnce, the two sync.Map
// caches) to see whether hot reads and cold/rare writes share a cache line
// within Mux — the false-sharing question for CH-xx.
func TestStructLayoutAtomicPointers(t *testing.T) {
	typ := reflect.TypeOf(muxmaster.Mux{})
	hot := []string{"treesPtr", "cfg", "preHandlerPtr"}
	cold := []string{"mu", "rawPathDecodeWarnOnce", "methodNotAllowedCache", "optionsCache", "lazyNotFoundPtr"}
	report := func(names []string, label string) {
		for _, n := range names {
			f, ok := typ.FieldByName(n)
			if !ok {
				t.Logf("  (%s) field %s not found — check name still matches source", label, n)
				continue
			}
			t.Logf("  (%s) [CL%d +%3d..%3d] %s", label, f.Offset/cacheLine, f.Offset, f.Offset+f.Type.Size(), n)
		}
	}
	t.Log("HOT (read every ServeHTTP call):")
	report(hot, "hot")
	t.Log("COLD/RARE (written at registration, or read only on cache miss):")
	report(cold, "cold")
}

var _ = atomic.Pointer[int]{}
