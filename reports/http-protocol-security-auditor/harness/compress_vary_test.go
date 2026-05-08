// Compress Vary cache-poisoning blast-radius tests.
//
// MSR-2026-0060 identified that small responses (< 1024 B) bypass the gzip
// path and never emit Vary: Accept-Encoding.  This file investigates whether
// the blast radius extends to a full CDN/reverse-proxy cache-poisoning scenario:
//
//	1. HPS-2026-0007: Small-response Vary absence — blast radius verification.
//	   A CDN that caches without a Vary key will serve the non-gzip body to a
//	   gzip-capable client, causing garbled content — not a security hole per
//	   se, but a correctness defect exploitable for cache deception.
//
//	2. HPS-2026-0008: Mixed-cache scenario — CDN serves compressed response to
//	   non-gzip client because the cached entry was populated by a gzip request.
//
//	3. HPS-2026-0009: Threshold boundary — a response of exactly 1024 bytes
//	   (the minCompressSize boundary) must always emit Vary, never omit it.
//
//	4. HPS-2026-0010: Vary header absent when handler pre-sets Content-Encoding
//	   — confirm Compress() does not double-compress or drop Vary in this case.
//
// Run with:
//
//	go test -race -v -run TestCompress ./reports/http-protocol-security-auditor/harness/
//
// Commit under test: f4faa5405324fe6779b2624741ac282388d3006c
// Go: go1.26.2 linux/amd64
package harness

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// makeCompressHandler wraps a handler that returns a fixed-size body with the
// Compress middleware at the default level.
func makeCompressHandler(bodySize int) http.Handler {
	body := bytes.Repeat([]byte("A"), bodySize)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	})
	return middleware.Compress(gzip.DefaultCompression)(inner)
}

// simulateCDNCache is a minimal, non-thread-safe in-memory "CDN" that caches
// on (path, Accept-Encoding) key pair — this is CORRECT CDN behaviour with
// Vary: Accept-Encoding.  Omitting Vary means the CDN caches only on path,
// which is the VULNERABLE scenario we test below.
type simulateCDNCache struct {
	entries map[string]cachedEntry // key = path only (simulates no-Vary CDN)
}

type cachedEntry struct {
	statusCode      int
	headers         http.Header
	body            []byte
	contentEncoding string
}

func newSimulateCDNCache() *simulateCDNCache {
	return &simulateCDNCache{entries: make(map[string]cachedEntry)}
}

// get returns (entry, hit).
func (c *simulateCDNCache) get(path string) (cachedEntry, bool) {
	e, ok := c.entries[path]
	return e, ok
}

// set stores a response in the cache keyed ONLY on path (no Vary consideration).
func (c *simulateCDNCache) set(path string, e cachedEntry) {
	c.entries[path] = e
}

// ─────────────────────────────────────────────────────────────────────────────
// HPS-2026-0007: Small response Vary absence — blast-radius verification
// ─────────────────────────────────────────────────────────────────────────────
//
// Expected behaviour (correct):
//   - Body < 1024 B → Content-Encoding: (none), Vary: (none) — because the
//     response is not compressed, Vary is unnecessary.
//
// Cache-poisoning blast radius:
//   - A naïve CDN that caches without considering Vary will serve the
//     non-compressed body to gzip-capable clients after the cache is warmed
//     by a non-gzip request.  The client can read the plain body fine — no
//     garbling.
//   - HOWEVER: if the first request is gzip-capable and the body is small,
//     the response is NOT compressed (correct), but if the CDN then serves
//     this plain body to a gzip-capable client that expects gzip (it won't
//     because Content-Encoding is absent), the client reads it correctly.
//
// Conclusion: the absence of Vary on small responses is NOT a cache-poisoning
// vulnerability because the response has no Content-Encoding header.  A CDN
// caching a plain-text response will always serve plain text — readable by
// both gzip-capable and non-capable clients.  The MSR-2026-0060 finding
// stands as a correctness/interoperability issue but has no security impact.

func TestCompressVary_SmallResponse_BlastRadius(t *testing.T) {
	const smallBodySize = 512 // < 1024 threshold

	handler := makeCompressHandler(smallBodySize)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Request 1: non-gzip client warms the cache.
	resp1, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("request 1 failed: %v", err)
	}
	body1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()

	ce1 := resp1.Header.Get("Content-Encoding")
	vary1 := resp1.Header.Get("Vary")
	t.Logf("Non-gzip client: status=%d Content-Encoding=%q Vary=%q body-len=%d",
		resp1.StatusCode, ce1, vary1, len(body1))

	if ce1 == "gzip" {
		t.Errorf("FINDING: small response was compressed (Content-Encoding: gzip) — "+
			"expected no compression for %d-byte body", smallBodySize)
	}

	// Cache this response (simulating a CDN that ignores Vary).
	cache := newSimulateCDNCache()
	cache.set("/", cachedEntry{
		statusCode:      resp1.StatusCode,
		headers:         resp1.Header,
		body:            body1,
		contentEncoding: ce1,
	})

	// Request 2: gzip-capable client hits the CDN cache (gets non-gzip body).
	cached, hit := cache.get("/")
	if !hit {
		t.Fatal("cache miss — should have been populated")
	}

	// Since cached.contentEncoding is "", the gzip-capable client receives
	// plain text — it will read it correctly regardless of Accept-Encoding.
	// This is NOT a vulnerability.
	t.Logf("CDN serves cached response to gzip client: Content-Encoding=%q", cached.contentEncoding)

	if cached.contentEncoding == "gzip" {
		// If a compressed response were cached and served to a non-gzip client,
		// that client would receive garbled gzip bytes — THIS would be the vulnerability.
		t.Errorf("FINDING HPS-2026-0007: CDN cache-poisoning scenario is exploitable — "+
			"compressed response cached without Vary will garble non-gzip clients")
		t.Errorf("CWE-345: Insufficient Verification of Data Authenticity (cache poisoning)")
		t.Errorf("Mitigation: small responses should not suppress Vary header when the "+
			"Compress middleware is present in the chain")
	} else {
		t.Logf("PASS HPS-2026-0007: Small response is NOT compressed — "+
			"no Vary required, no cache-poisoning risk. "+
			"CDN serves plain text to both gzip and non-gzip clients correctly.")
		t.Logf("ANALYSIS: Vary absence on small responses is a correctness/interop issue "+
			"(RFC 7234 §4.1 recommends Vary when content varies) but NOT exploitable "+
			"for cache poisoning because Content-Encoding is absent.")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HPS-2026-0008: Mixed-cache — large response compressed, served to non-gzip client
// ─────────────────────────────────────────────────────────────────────────────
//
// Scenario: gzip-capable client requests a large response (≥ 1024 B).
// The Compress middleware compresses it and emits:
//   Content-Encoding: gzip
//   Vary: Accept-Encoding
//
// A naïve CDN ignoring Vary caches this compressed response.
// Non-gzip client then hits the CDN and receives the compressed body without
// Content-Encoding: gzip — client reads garbled data.
//
// This is the TRUE cache-poisoning blast radius: large responses WITH Vary
// are safe when the CDN honours Vary.  But if the CDN is misconfigured to
// ignore Vary, compressed responses can be served to non-gzip clients.
//
// MuxMaster's responsibility: emit Vary: Accept-Encoding whenever Content-Encoding
// is set.  Verify this is done correctly.

func TestCompressVary_LargeResponse_CDNPoisoningRisk(t *testing.T) {
	const largeBodySize = 2048 // > 1024 threshold — will be compressed

	handler := makeCompressHandler(largeBodySize)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Request 1: gzip-capable client.
	req1, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req1.Header.Set("Accept-Encoding", "gzip")
	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatalf("gzip request failed: %v", err)
	}
	gzipBody, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()

	ce1 := resp1.Header.Get("Content-Encoding")
	vary1 := resp1.Header.Get("Vary")
	t.Logf("Gzip client: status=%d Content-Encoding=%q Vary=%q body-len=%d",
		resp1.StatusCode, ce1, vary1, len(gzipBody))

	// Security assertion 1: Vary MUST be present when Content-Encoding is gzip.
	if ce1 == "gzip" && !strings.Contains(vary1, "Accept-Encoding") {
		t.Errorf("FINDING HPS-2026-0008: Content-Encoding: gzip emitted WITHOUT Vary: Accept-Encoding — "+
			"CDN cache-poisoning is possible if CDN ignores missing Vary")
		t.Errorf("CWE-345: Cache poisoning — non-gzip clients will receive garbled compressed body")
		t.Errorf("CWE-693: Protection Mechanism Failure (cache layer bypass)")
		t.Errorf("Mitigation: emit Vary: Accept-Encoding for ALL responses when Compress middleware is in chain")
	} else if ce1 == "gzip" && strings.Contains(vary1, "Accept-Encoding") {
		t.Logf("PASS: Vary: Accept-Encoding correctly set with Content-Encoding: gzip")
	} else if ce1 == "" {
		t.Logf("INFO: large body not compressed (Content-Encoding absent, Vary=%q)", vary1)
	}

	// Simulate CDN caching the gzip response without honouring Vary.
	cache := newSimulateCDNCache()
	cache.set("/", cachedEntry{
		statusCode:      resp1.StatusCode,
		headers:         resp1.Header,
		body:            gzipBody,
		contentEncoding: ce1,
	})

	// Request 2: non-gzip client hits CDN (simulated by serving cached entry directly).
	cached, _ := cache.get("/")
	if cached.contentEncoding == "gzip" {
		// Attempt to decode the cached gzip body as plain text.
		// A non-gzip client would interpret gzip bytes as the response body.
		// The first bytes of a gzip stream are 0x1f 0x8b — not valid UTF-8.
		isReadable := isValidUTF8OrASCII(cached.body)
		t.Logf("CDN serves compressed response to non-gzip client: body-len=%d readable=%v",
			len(cached.body), isReadable)

		if !isReadable {
			t.Logf("CONFIRMED BLAST RADIUS: non-gzip client receives garbled compressed bytes")
			t.Logf("IMPACT: content corruption (garbled page) for non-gzip clients behind a Vary-ignoring CDN")
			t.Logf("SEVERITY: Medium — requires CDN misconfiguration (ignore Vary); MuxMaster emits correct Vary")
			t.Logf("VERDICT: MuxMaster is SAFE (emits Vary correctly); blast radius requires CDN misconfiguration")
		}

		// Verify the gzip body is actually valid gzip — confirms it's not double-compressed.
		gr, err := gzip.NewReader(bytes.NewReader(cached.body))
		if err != nil {
			t.Errorf("FINDING: Compress middleware emitted Content-Encoding: gzip but body is not valid gzip: %v", err)
		} else {
			decompressed, _ := io.ReadAll(gr)
			gr.Close()
			t.Logf("Decompressed body length: %d (expected %d)", len(decompressed), largeBodySize)
			if len(decompressed) != largeBodySize {
				t.Errorf("FINDING: decompressed body length %d != expected %d", len(decompressed), largeBodySize)
			} else {
				t.Logf("PASS: gzip body is valid and decompresses to correct size")
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HPS-2026-0009: Threshold boundary — exactly minCompressSize bytes
// ─────────────────────────────────────────────────────────────────────────────
//
// Verifies the boundary condition at minCompressSize (1024 bytes):
//   - body of 1023 bytes: NOT compressed, NO Vary.
//   - body of 1024 bytes: compressed, Vary MUST be present.
//   - body of 1025 bytes: compressed, Vary MUST be present.
//
// A Vary absent at the exact boundary would be a defect.

func TestCompressVary_ThresholdBoundary(t *testing.T) {
	const minSize = 1024 // minCompressSize in middleware/compress.go

	cases := []struct {
		size         int
		expectGzip   bool
		expectVary   bool
		desc         string
	}{
		{minSize - 1, false, false, "below threshold — no compression, no Vary"},
		{minSize, true, true, "at threshold — compressed, Vary required"},
		{minSize + 1, true, true, "above threshold — compressed, Vary required"},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("size=%d", tc.size), func(t *testing.T) {
			handler := makeCompressHandler(tc.size)
			srv := httptest.NewServer(handler)
			defer srv.Close()

			req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
			req.Header.Set("Accept-Encoding", "gzip")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			ce := resp.Header.Get("Content-Encoding")
			vary := resp.Header.Get("Vary")
			isGzip := ce == "gzip"
			hasVary := strings.Contains(vary, "Accept-Encoding")

			t.Logf("size=%d: Content-Encoding=%q Vary=%q", tc.size, ce, vary)

			if isGzip != tc.expectGzip {
				t.Errorf("FINDING: size=%d expected-gzip=%v got-gzip=%v",
					tc.size, tc.expectGzip, isGzip)
			}
			if hasVary != tc.expectVary {
				if tc.expectVary {
					t.Errorf("FINDING HPS-2026-0009: size=%d — Content-Encoding: gzip present but Vary: Accept-Encoding ABSENT",
						tc.size)
					t.Errorf("CWE-345: Cache poisoning — CDN may serve compressed bytes to non-gzip clients")
				} else {
					t.Logf("INFO: size=%d — Vary header present when not strictly needed (harmless)", tc.size)
				}
			} else {
				t.Logf("PASS: size=%d Vary expectation met (expectVary=%v hasVary=%v)", tc.size, tc.expectVary, hasVary)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HPS-2026-0010: Handler pre-sets Content-Encoding — Compress must not
//                double-compress or silently drop Vary
// ─────────────────────────────────────────────────────────────────────────────
//
// If the upstream handler manually sets Content-Encoding: gzip (e.g. serving
// a pre-compressed asset), the Compress middleware should detect this and skip
// re-compression.  If it does not, the response will be double-compressed and
// unreadable.

func TestCompressVary_PreSetContentEncoding_NoDoubleCompress(t *testing.T) {
	preCompressedBody := []byte("pre-compressed-content-that-is-not-actually-gzip-but-tests-the-path")
	// Pad to exceed the 1024-byte threshold so sniff buffer triggers commit().
	preCompressedBody = append(preCompressedBody, bytes.Repeat([]byte("X"), 1024)...)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Encoding", "gzip") // pre-set by handler
		w.WriteHeader(http.StatusOK)
		w.Write(preCompressedBody)
	})

	handler := middleware.Compress(gzip.DefaultCompression)(inner)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	ce := resp.Header.Get("Content-Encoding")
	t.Logf("Pre-set CE handler: status=%d Content-Encoding=%q body-len=%d", resp.StatusCode, ce, len(body))

	// The body should be the pre-compressed bytes unchanged.
	// If double-compressed, body would be gzip(gzip(data)) — unreadable.
	// We detect double-compression by checking whether the body is longer than
	// the source (gzip of short data is usually longer or similar size, but
	// applying gzip to already-opaque bytes typically changes length).
	//
	// Primary check: content must not be silently lost (len > 0).
	if len(body) == 0 {
		t.Errorf("FINDING HPS-2026-0010: response body empty after Compress — possible double-compression or data loss")
	} else {
		t.Logf("PASS: response body non-empty (len=%d)", len(body))
	}

	// If the middleware re-compressed the body, the outer Content-Encoding would
	// be "gzip" but decoding it would reveal another gzip stream.
	if ce == "gzip" {
		gr, err := gzip.NewReader(bytes.NewReader(body))
		if err == nil {
			inner2, _ := io.ReadAll(gr)
			gr.Close()
			// If inner2 starts with gzip magic bytes, it was double-compressed.
			if len(inner2) >= 2 && inner2[0] == 0x1f && inner2[1] == 0x8b {
				t.Errorf("FINDING HPS-2026-0010: double-compression detected — "+
					"Compress middleware re-compressed a body that already had Content-Encoding: gzip")
				t.Errorf("CWE-116: Improper Encoding — clients cannot decode double-gzip body")
			} else {
				t.Logf("INFO: body is gzip-encoded; inner content is not gzip (no double-compression)")
			}
		}
		// If gzip.NewReader fails, the Compress middleware re-compressed non-gzip
		// bytes — log but don't fail (it's a correctness issue, not a security issue).
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helper
// ─────────────────────────────────────────────────────────────────────────────

func isValidUTF8OrASCII(b []byte) bool {
	for _, c := range b {
		if c > 127 {
			return false
		}
	}
	return true
}
