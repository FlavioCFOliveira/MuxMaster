package dosharness

import (
	"net/http"
	"net/http/httptest"
	"testing"

	mm "github.com/FlavioCFOliveira/MuxMaster"
)

// TestParamsBufSilentOverflow confirms H-012: the 4th+ path param is silently
// dropped because maxInlineParams=3. The handler observes "" for these params.
//
// This is a Medium-severity CORRECTNESS bug; it can turn into a security bug
// when a downstream middleware or handler uses the missing param for auth.
func TestParamsBufSilentOverflow(t *testing.T) {
	r := mm.New()
	var seen map[string]string
	r.GET("/a/:p1/:p2/:p3/:p4/:p5", func(w http.ResponseWriter, req *http.Request) {
		seen = map[string]string{
			"p1": mm.PathParam(req, "p1"),
			"p2": mm.PathParam(req, "p2"),
			"p3": mm.PathParam(req, "p3"),
			"p4": mm.PathParam(req, "p4"),
			"p5": mm.PathParam(req, "p5"),
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/a/alpha/beta/gamma/delta/epsilon", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	t.Logf("observed params: %+v", seen)

	if seen["p1"] != "alpha" || seen["p2"] != "beta" || seen["p3"] != "gamma" {
		t.Errorf("first 3 params wrong: %+v", seen)
	}
	if seen["p4"] != "" || seen["p5"] != "" {
		// If a future version fixes this, the test needs updating — but
		// today these MUST be empty per paramsBuf.add's silent-drop rule.
		t.Logf("NOTE: p4/p5 captured (overflow fixed): %q / %q", seen["p4"], seen["p5"])
	}
	if seen["p4"] != "" {
		t.Errorf("EXPECTED silent overflow — p4 should be empty but is %q", seen["p4"])
	}
}

// BenchmarkManyParamsRoute establishes the ns/op cost of param-walking, and
// whether the overflow adds cost.
func BenchmarkManyParamsRoute(b *testing.B) {
	for _, k := range []int{1, 2, 3, 4, 5, 6} {
		k := k
		b.Run(labelForParams(k), func(b *testing.B) {
			r := mm.New()
			pattern := ""
			path := ""
			for i := 1; i <= k; i++ {
				pattern += "/:p" + intToStr(i)
				path += "/v" + intToStr(i)
			}
			r.GET(pattern, func(w http.ResponseWriter, req *http.Request) {})
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			b.ResetTimer()
			b.ReportAllocs()
			for range b.N {
				r.ServeHTTP(w, req)
			}
		})
	}
}

func labelForParams(k int) string {
	return "params=" + intToStr(k)
}

func intToStr(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return intToStr(i/10) + string(rune('0'+i%10))
}
