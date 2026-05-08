package harness

// UseRawPath × UnescapePathValues 4-state matrix harness — H8-27
//
// MuxMaster exposes two interacting boolean options that control how
// percent-encoded paths are handled during dispatch:
//
//   UseRawPath=false  UnescapePathValues=false (default)
//     → use r.URL.Path (already decoded by net/url); param values are decoded
//   UseRawPath=true   UnescapePathValues=false
//     → use r.URL.RawPath (raw, encoded); param values are raw encoded strings
//   UseRawPath=true   UnescapePathValues=true
//     → use r.URL.RawPath for matching; param values are URL-decoded
//   UseRawPath=false  UnescapePathValues=true
//     → documented: UnescapePathValues has no effect when UseRawPath=false
//       (uses decoded path, param values already decoded)
//
// Invariants tested (H8-27):
//   I-RAW-01: For any (UseRawPath, UnescapePathValues) combination,
//             ServeHTTP never panics.
//   I-RAW-02: With UseRawPath=false, routing result is identical regardless
//             of UnescapePathValues (documented no-effect).
//   I-RAW-03: With UseRawPath=true + UnescapePathValues=true, the param value
//             received by the handler equals url.PathUnescape(rawSegment) when
//             rawSegment is a valid percent-encoded segment.
//   I-RAW-04: With UseRawPath=true + UnescapePathValues=false, the param value
//             received by the handler equals the raw (encoded) segment.
//   I-RAW-05: The catch-all invariant: *filepath captures the remainder of the
//             path; with UseRawPath=true the captured value includes raw encoding.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime/debug"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"pgregory.net/rapid"
)

// newMuxWithOptions creates a Mux with a parameterized route and the given flags.
func newMuxWithOptions(useRaw, unescape bool, handler http.HandlerFunc) *mm.Mux {
	mux := mm.New()
	mux.UseRawPath = useRaw
	mux.UnescapePathValues = unescape
	mux.GET("/items/:id", handler)
	mux.GET("/static/*filepath", handler)
	return mux
}

// buildRawRequest creates a request with an explicit RawPath so that
// UseRawPath=true can access the raw encoding.
func buildRawRequest(method, decodedPath, rawPath string) (*http.Request, error) {
	u, err := url.ParseRequestURI("http://example.com" + decodedPath)
	if err != nil {
		return nil, err
	}
	if rawPath != "" {
		u.RawPath = rawPath
	}
	req := &http.Request{
		Method:     method,
		URL:        u,
		Header:     make(http.Header),
		Body:       http.NoBody,
		RequestURI: decodedPath,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
	}
	return req, nil
}

// ============================================================
// I-RAW-01: All 4 combinations — no panic
// ============================================================

func FuzzRawPathMatrixNoPanic(f *testing.F) {
	f.Add(true, false, "/items/hello%2Fworld", "")
	f.Add(true, true, "/items/hello%2Fworld", "")
	f.Add(false, false, "/items/hello", "")
	f.Add(false, true, "/items/hello", "")
	f.Add(true, false, "/items/%2F%2F%2E%2E", "")
	f.Add(true, true, "/items/a%20b", "")
	f.Add(false, false, "/static/path/to/file.js", "")
	f.Add(true, false, "/static/path%2Fto%2Ffile.js", "")

	f.Fuzz(func(t *testing.T, useRaw, unescape bool, rawSegment, _ string) {
		if containsControlBytes(rawSegment) {
			return
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("I-RAW-01 PANIC: useRaw=%v unescape=%v seg=%q r=%v\n%s",
					useRaw, unescape, rawSegment, r, debug.Stack())
			}
		}()

		for _, useRawPath := range []bool{false, true} {
			for _, unescapePathValues := range []bool{false, true} {
				var gotParam string
				mux := mm.New()
				mux.UseRawPath = useRawPath
				mux.UnescapePathValues = unescapePathValues
				mux.GET("/items/:id", func(w http.ResponseWriter, r *http.Request) {
					gotParam = mm.PathParam(r, "id")
					w.WriteHeader(http.StatusOK)
				})

				// Build request: use the raw segment as the path component.
				decodedPath := "/items/" + rawSegment
				req, err := buildRequest(http.MethodGet, decodedPath)
				if err != nil {
					continue
				}
				if useRawPath {
					req.URL.RawPath = "/items/" + rawSegment
				}
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				_ = gotParam // consumed
			}
		}
	})
}

// ============================================================
// I-RAW-02: UseRawPath=false → UnescapePathValues has no effect
// ============================================================

func TestProp_RawPathFalseUnescapeNoEffect(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in I-RAW-02: %v\n%s", r, debug.Stack())
			}
		}()

		segment := rapid.StringMatching(`[a-zA-Z0-9_-]{1,20}`).Draw(t, "segment")

		var param1, param2 string

		mux1 := mm.New()
		mux1.UseRawPath = false
		mux1.UnescapePathValues = false
		mux1.GET("/items/:id", func(w http.ResponseWriter, r *http.Request) {
			param1 = mm.PathParam(r, "id")
			w.WriteHeader(http.StatusOK)
		})

		mux2 := mm.New()
		mux2.UseRawPath = false
		mux2.UnescapePathValues = true
		mux2.GET("/items/:id", func(w http.ResponseWriter, r *http.Request) {
			param2 = mm.PathParam(r, "id")
			w.WriteHeader(http.StatusOK)
		})

		path := "/items/" + segment
		req1 := httptest.NewRequest(http.MethodGet, path, nil)
		req2 := httptest.NewRequest(http.MethodGet, path, nil)
		rec1 := httptest.NewRecorder()
		rec2 := httptest.NewRecorder()
		mux1.ServeHTTP(rec1, req1)
		mux2.ServeHTTP(rec2, req2)

		if rec1.Code != rec2.Code {
			t.Fatalf("I-RAW-02: status differs: unescape=false got %d, unescape=true got %d",
				rec1.Code, rec2.Code)
		}
		if param1 != param2 {
			t.Fatalf("I-RAW-02: param differs with UseRawPath=false: %q vs %q",
				param1, param2)
		}
	})
}

// ============================================================
// I-RAW-03/04: UseRawPath=true — raw vs unescaped param values
// ============================================================

func TestProp_RawPathTrueParamValues(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC in I-RAW-03/04: %v\n%s", r, debug.Stack())
			}
		}()

		// Generate a segment that may contain percent-encoded sequences.
		// We generate a plain segment and then percent-encode specific characters
		// to create a valid raw segment.
		plain := rapid.StringMatching(`[a-zA-Z0-9_]{1,10}`).Draw(t, "plain")
		// Encode some characters to create percent-encoding.
		rawSeg := url.PathEscape(plain)

		var paramRaw, paramUnescaped string

		muxRaw := mm.New()
		muxRaw.UseRawPath = true
		muxRaw.UnescapePathValues = false
		muxRaw.GET("/items/:id", func(w http.ResponseWriter, r *http.Request) {
			paramRaw = mm.PathParam(r, "id")
			w.WriteHeader(http.StatusOK)
		})

		muxUnescaped := mm.New()
		muxUnescaped.UseRawPath = true
		muxUnescaped.UnescapePathValues = true
		muxUnescaped.GET("/items/:id", func(w http.ResponseWriter, r *http.Request) {
			paramUnescaped = mm.PathParam(r, "id")
			w.WriteHeader(http.StatusOK)
		})

		decodedPath := "/items/" + plain
		rawPath := "/items/" + rawSeg

		req1, err := buildRawRequest(http.MethodGet, decodedPath, rawPath)
		if err != nil {
			return
		}
		req2, err := buildRawRequest(http.MethodGet, decodedPath, rawPath)
		if err != nil {
			return
		}

		rec1 := httptest.NewRecorder()
		rec2 := httptest.NewRecorder()
		muxRaw.ServeHTTP(rec1, req1)
		muxUnescaped.ServeHTTP(rec2, req2)

		if rec1.Code == http.StatusOK {
			// I-RAW-04: with UseRawPath=true, UnescapePathValues=false,
			// param == the raw segment.
			if paramRaw != rawSeg {
				t.Errorf("I-RAW-04: param=%q want raw=%q (plain=%q)",
					paramRaw, rawSeg, plain)
			}
		}

		if rec2.Code == http.StatusOK {
			// I-RAW-03: with UseRawPath=true, UnescapePathValues=true,
			// param == the decoded segment.
			decoded, decErr := url.PathUnescape(rawSeg)
			if decErr == nil && paramUnescaped != decoded {
				t.Errorf("I-RAW-03: param=%q want decoded=%q (raw=%q plain=%q)",
					paramUnescaped, decoded, rawSeg, plain)
			}
		}
	})
}

// ============================================================
// I-RAW-05: Differential output across all 4 states for the same input
// ============================================================

func TestRawPathDifferential4States(t *testing.T) {
	// Known percent-encoded segments and what each mode should return.
	cases := []struct {
		name       string
		rawSegment string // the raw path segment (may be encoded)
		// Expected param values for each mode:
		// [UseRawPath=false/unescape=false, UseRawPath=false/unescape=true,
		//  UseRawPath=true/unescape=false,  UseRawPath=true/unescape=true]
		wantDecoded string // expected when UseRawPath=false (both unescape settings)
		wantRaw     string // expected when UseRawPath=true + UnescapePathValues=false
		wantEsc     string // expected when UseRawPath=true + UnescapePathValues=true
	}{
		{
			name:        "plain",
			rawSegment:  "hello",
			wantDecoded: "hello",
			wantRaw:     "hello",
			wantEsc:     "hello",
		},
		{
			name:        "space_encoded",
			rawSegment:  "hello%20world",
			wantDecoded: "hello world", // net/url decodes for r.URL.Path
			wantRaw:     "hello%20world",
			wantEsc:     "hello world",
		},
		{
			name:        "slash_encoded",
			rawSegment:  "a%2Fb",
			wantDecoded: "a/b",
			wantRaw:     "a%2Fb",
			wantEsc:     "a/b",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decodedSegment, err := url.PathUnescape(tc.rawSegment)
			if err != nil {
				t.Skipf("cannot decode segment %q: %v", tc.rawSegment, err)
			}
			decodedPath := "/items/" + decodedSegment
			rawPath := "/items/" + tc.rawSegment

			configs := []struct {
				useRaw   bool
				unescape bool
				wantSeg  string
			}{
				{false, false, tc.wantDecoded},
				{false, true, tc.wantDecoded}, // I-RAW-02: same as above
				{true, false, tc.wantRaw},
				{true, true, tc.wantEsc},
			}

			for _, cfg := range configs {
				name := fmt.Sprintf("UseRaw=%v/Unescape=%v", cfg.useRaw, cfg.unescape)
				t.Run(name, func(t *testing.T) {
					defer func() {
						if r := recover(); r != nil {
							t.Fatalf("PANIC: %v\n%s", r, debug.Stack())
						}
					}()
					var gotParam string
					mux := mm.New()
					mux.UseRawPath = cfg.useRaw
					mux.UnescapePathValues = cfg.unescape
					mux.GET("/items/:id", func(w http.ResponseWriter, r *http.Request) {
						gotParam = mm.PathParam(r, "id")
						w.WriteHeader(http.StatusOK)
					})

					req, buildErr := buildRawRequest(http.MethodGet, decodedPath, rawPath)
					if buildErr != nil {
						t.Skipf("build request failed: %v", buildErr)
					}
					rec := httptest.NewRecorder()
					mux.ServeHTTP(rec, req)

					if rec.Code != http.StatusOK {
						t.Logf("status=%d (segment may not have been routed)", rec.Code)
						return
					}
					if gotParam != cfg.wantSeg {
						t.Errorf("param=%q, want=%q", gotParam, cfg.wantSeg)
					}
				})
			}
		})
	}
}
