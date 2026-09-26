// Regression tests for rmp task #252, sprint 18 (WH-12): APIKey now injects
// the validated identity through a single fused context node (apiKeyCtx)
// instead of context.WithValue boxing a bare string, mirroring RequestID's
// existing technique. These tests pin the externally observable behaviour:
// GetAPIKeyIdentity still returns the same (id, ok) pairs, the fused node
// still falls through correctly to unrelated context keys set before OR
// after APIKey runs, and the TSC-2026-0008 header-symmetry timing
// equalisation (already covered by TestSec_TSC_2026_0008_APIKey_HeaderSymmetry
// in middleware_test.go) is untouched.
package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

func TestAPIKey_FusedContext_IdentityRetrievable(t *testing.T) {
	h := middleware.APIKey(middleware.APIKeyOptions{Keys: map[string]string{"k1": "alice", "k2": "bob"}})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := middleware.GetAPIKeyIdentity(r.Context())
			if !ok || id != "alice" {
				t.Fatalf("GetAPIKeyIdentity = (%q, %v), want (\"alice\", true)", id, ok)
			}
		}),
	)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-API-Key", "k1")
	h.ServeHTTP(httptest.NewRecorder(), r)
}

type apiKeyOtherCtxKey struct{}

// TestAPIKey_FusedContext_FallsThroughToOtherKeys mirrors requestIDCtx's own
// contract (request_id.go doc comment): a value set on the PARENT context
// before APIKey ran must remain visible afterwards, and the identity must
// still be reachable through any number of further context.WithValue layers
// wrapped by downstream middleware AFTER APIKey.
func TestAPIKey_FusedContext_FallsThroughToOtherKeys(t *testing.T) {
	h := middleware.APIKey(middleware.APIKeyOptions{Keys: map[string]string{"k1": "alice"}})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if v := r.Context().Value(apiKeyOtherCtxKey{}); v != "parent-value" {
				t.Fatalf("parent context value lost through the fused node: %v", v)
			}
			ctx := context.WithValue(r.Context(), apiKeyOtherCtxKey{}, "child-value")
			id, ok := middleware.GetAPIKeyIdentity(ctx)
			if !ok || id != "alice" {
				t.Fatalf("GetAPIKeyIdentity through a further-wrapped context = (%q, %v), want (\"alice\", true)", id, ok)
			}
			if v := ctx.Value(apiKeyOtherCtxKey{}); v != "child-value" {
				t.Fatalf("child context value not visible: %v", v)
			}
		}),
	)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-API-Key", "k1")
	r = r.WithContext(context.WithValue(r.Context(), apiKeyOtherCtxKey{}, "parent-value"))
	h.ServeHTTP(httptest.NewRecorder(), r)
}

func TestAPIKey_GetIdentity_AbsentOnBareContext(t *testing.T) {
	if id, ok := middleware.GetAPIKeyIdentity(context.Background()); ok || id != "" {
		t.Fatalf("GetAPIKeyIdentity(bare context) = (%q, %v), want (\"\", false)", id, ok)
	}
}

// TestAPIKey_TwoInstances_DoNotShareIdentity confirms each APIKey() call
// produces an independent set of valid keys/identities — the fused node
// carries no state shared across middleware instances.
func TestAPIKey_TwoInstances_DoNotShareIdentity(t *testing.T) {
	h1 := middleware.APIKey(middleware.APIKeyOptions{Keys: map[string]string{"k1": "alice"}})
	h2 := middleware.APIKey(middleware.APIKeyOptions{Keys: map[string]string{"k2": "bob"}})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-API-Key", "k1")
	h2(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("h2 must reject a key that only h1 recognises")
	})).ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("cross-instance key acceptance: got %d, want 401", rec.Code)
	}
	_ = h1
}
