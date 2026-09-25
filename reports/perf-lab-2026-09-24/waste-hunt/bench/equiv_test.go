package bench

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// Equivalence tests: every harness alternative must be observably identical
// to the current MuxMaster code it is compared against. A benchmark gain is
// only reported for alternatives that pass here.

func randString(rng *rand.Rand, n int) string {
	alphabet := []string{"a", "Z", "/", "%2f", " ", "\"", "\\", "\n", "\r", "\t", "\x00", "\x7f", "é", "日本", "\xff", "\xc3", "😀", "..", "?", "#"}
	var sb strings.Builder
	for range n {
		sb.WriteString(alphabet[rng.IntN(len(alphabet))])
	}
	return sb.String()
}

func TestEquivAppendSanitised(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	corpus := []string{"", "GET", "/api/v1/books/42", "/a b", "/\"q\"", "/back\\slash", "/nl\n", "\x00", "/é", "/\xff\xfe", "/日本語"}
	for range 200000 {
		corpus = append(corpus, randString(rng, rng.IntN(12)))
	}
	for _, s := range corpus {
		q := strconv.QuoteToASCII(s)
		want := q[1 : len(q)-1]
		if got := string(appendSanitised([]byte("prefix"), s)); got != "prefix"+want {
			t.Fatalf("appendSanitised(%q) = %q, want %q", s, got, "prefix"+want)
		}
	}
}

func TestEquivAppendDuration(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	ds := []time.Duration{0, 1, 999, 1000, 1001, 999999, 1000000, 1234567, time.Second - 1, time.Second, time.Minute, time.Hour, 25*time.Hour + 3*time.Second + 7, -1, -1234567, -time.Hour, 1<<63 - 1, -1 << 63}
	for range 500000 {
		ds = append(ds, time.Duration(rng.Int64()>>uint(rng.IntN(63))))
	}
	for _, d := range ds {
		if got, want := string(appendDuration(nil, d)), d.String(); got != want {
			t.Fatalf("appendDuration(%d) = %q, want %q", int64(d), got, want)
		}
	}
}

// Both loggers must produce lines with identical structure and identical
// method/path/status fields; the timestamp must parse as RFC 3339 and the
// duration with time.ParseDuration.
func TestEquivLoggerLine(t *testing.T) {
	for _, path := range []string{"/api/v1/books/42", "/a%20b/\"x\"/é"} {
		var cur, alt bytes.Buffer
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(418) })
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.URL.Path = path
		mw.Logger(&cur)(h).ServeHTTP(httptest.NewRecorder(), r)
		loggerAlt(&alt)(h).ServeHTTP(httptest.NewRecorder(), r)
		cf := strings.Fields(strings.TrimSuffix(cur.String(), "\n"))
		af := strings.Fields(strings.TrimSuffix(alt.String(), "\n"))
		if len(cf) != len(af) {
			t.Fatalf("field count differs: %q vs %q", cur.String(), alt.String())
		}
		for i := 1; i < len(cf)-1; i++ {
			if cf[i] != af[i] {
				t.Fatalf("field %d differs: %q vs %q", i, cf[i], af[i])
			}
		}
		if _, err := time.Parse(time.RFC3339, af[0]); err != nil {
			t.Fatal(err)
		}
		if _, err := time.ParseDuration(af[len(af)-1]); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEquivSetHeader(t *testing.T) {
	for _, k := range []string{"X-Frame-Options", "x-frame-options", "WWW-Authenticate", "cache-control", "X-Request-ID"} {
		a, b := newDiscardRW(), newDiscardRW()
		mw.SetHeader(k, "v1")(nop).ServeHTTP(a, httptest.NewRequest("GET", "/", nil))
		setHeaderAlt(k, "v1")(nop).ServeHTTP(b, httptest.NewRequest("GET", "/", nil))
		if len(a.h) != 1 || len(b.h) != 1 {
			t.Fatalf("header count: %v vs %v", a.h, b.h)
		}
		for key, v := range a.h {
			if bv := b.h[key]; len(bv) != 1 || bv[0] != v[0] {
				t.Fatalf("key %q: %v vs %v", key, a.h, b.h)
			}
		}
	}
}

func TestEquivText(t *testing.T) {
	a, b := httptest.NewRecorder(), httptest.NewRecorder()
	_ = mm.Text(a, 0, "hello")
	_ = textAlt(b, 0, "hello")
	if a.Code != b.Code || a.Body.String() != b.Body.String() || a.Header().Get("Content-Type") != b.Header().Get("Content-Type") {
		t.Fatalf("Text differs: %v %q %v vs %v %q %v", a.Code, a.Body, a.Header(), b.Code, b.Body, b.Header())
	}
}

func TestEquivRealIP(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	atoms := []string{"203.0.113.9", "10.0.0.7", "127.0.0.1", "192.168.1.1", "172.16.5.4", "2001:db8::1", "fe80::1%eth0", "::1", "garbage", "", " ", "1.2.3", "8.8.8.8"}
	var corpus []string
	for range 50000 {
		n := 1 + rng.IntN(40) // crosses the 30-hop bound
		parts := make([]string, n)
		for i := range parts {
			sp := strings.Repeat(" ", rng.IntN(2))
			parts[i] = sp + atoms[rng.IntN(len(atoms))] + sp
		}
		corpus = append(corpus, strings.Join(parts, ","))
	}
	corpus = append(corpus, ",", ",,", "203.0.113.9,", ",203.0.113.9", " 203.0.113.9 , 10.0.0.1 ")
	for _, trusted := range [][]string{{"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}, {}} {
		cur := mw.RealIP(toPrefixes(trusted)...)(nop)
		alt := realIPAlt(toPrefixes(trusted)...)(nop)
		for _, xff := range corpus {
			r1 := httptest.NewRequest("GET", "/", nil)
			r1.RemoteAddr = "127.0.0.1:1"
			r1.Header.Set("X-Forwarded-For", xff)
			r2 := r1.Clone(context.Background())
			cur.ServeHTTP(httptest.NewRecorder(), r1)
			alt.ServeHTTP(httptest.NewRecorder(), r2)
			if r1.RemoteAddr != r2.RemoteAddr {
				t.Fatalf("trusted=%v xff=%q: current=%q alternative=%q", trusted, xff, r1.RemoteAddr, r2.RemoteAddr)
			}
		}
	}
}

func TestEquivCompress(t *testing.T) {
	for _, h := range []http.Handler{smallHandler, chunkedHandler} {
		a, b := httptest.NewRecorder(), httptest.NewRecorder()
		r := realisticRequest("GET", "/")
		mw.Compress(gzip.BestSpeed)(h).ServeHTTP(a, r)
		compressAlt(gzip.BestSpeed)(h).ServeHTTP(b, r)
		if a.Code != b.Code || !bytes.Equal(a.Body.Bytes(), b.Body.Bytes()) {
			t.Fatalf("body/status differ (len %d vs %d)", a.Body.Len(), b.Body.Len())
		}
		for _, k := range []string{"Content-Encoding", "Vary", "Content-Type", "Content-Length"} {
			if strings.Join(a.Header()[k], ",") != strings.Join(b.Header()[k], ",") {
				t.Fatalf("header %s differs: %v vs %v", k, a.Header()[k], b.Header()[k])
			}
		}
	}
	// Sequential reuse of the pooled writer must not leak state between requests.
	alt := compressAlt(gzip.BestSpeed)
	for i := range 50 {
		h := smallHandler
		if i%2 == 0 {
			h = chunkedHandler
		}
		a, b := httptest.NewRecorder(), httptest.NewRecorder()
		mw.Compress(gzip.BestSpeed)(h).ServeHTTP(a, realisticRequest("GET", "/"))
		alt(h).ServeHTTP(b, realisticRequest("GET", "/"))
		if !bytes.Equal(a.Body.Bytes(), b.Body.Bytes()) {
			t.Fatalf("iteration %d: pooled writer leaked state", i)
		}
	}
}

func TestEquivThrottle(t *testing.T) {
	for name, mk := range map[string]func() func(http.Handler) http.Handler{
		"current":     func() func(http.Handler) http.Handler { return mw.ThrottlePerIPCapped(2, 50*time.Millisecond, 100, nil) },
		"alternative": func() func(http.Handler) http.Handler { return throttleAlt(2, 50*time.Millisecond, 100, nil) },
	} {
		release := make(chan struct{})
		entered := make(chan struct{}, 2)
		h := mk()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/block" {
				entered <- struct{}{}
				<-release
			}
		}))
		var wg sync.WaitGroup
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/block", nil))
			}()
		}
		<-entered
		<-entered
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: third concurrent request got %d, want 503", name, rec.Code)
		}
		close(release)
		wg.Wait()
		for range 1000 { // sequential reuse after release: always admitted
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
			if rec.Code != 200 {
				t.Fatalf("%s: sequential request got %d", name, rec.Code)
			}
		}
	}
}

func TestEquivMountVariants(t *testing.T) {
	type seen struct{ path, raw, hdr, orig string }
	capture := func(dst *seen) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*dst = seen{r.URL.Path, r.URL.RawPath, r.Header.Get("User-Agent"), ""}
		})
	}
	for _, target := range []string{"/docs/v2/guide/index.html", "/docs/v2", "/docs/v2/", "/docs/v2/a%2fb/c"} {
		var s1, s2, s3 seen
		m1 := mm.New()
		m1.Mount("/docs/v2", capture(&s1))
		m2 := mm.New()
		m2.Handle("*", "/docs/v2/*"+mountParam, mountNoRedundantURL("/docs/v2", capture(&s2)))
		m3 := mm.New()
		m3.Handle("*", "/docs/v2/*"+mountParam, mountShallow("/docs/v2", capture(&s3)))
		for i, m := range []*mm.Mux{m1, m2, m3} {
			r := realisticRequest("GET", target)
			before := r.URL.String()
			m.ServeHTTP(httptest.NewRecorder(), r)
			if r.URL.String() != before {
				t.Fatalf("variant %d mutated the caller's URL", i)
			}
		}
		if s1 != s2 || s1 != s3 {
			t.Fatalf("%s: variants disagree: %+v %+v %+v", target, s1, s2, s3)
		}
	}
}

func TestEquivFileServeLoggerRF(t *testing.T) {
	dir := fileServeFixture(t)
	for _, f := range []string{"small.css", "large.bin"} {
		var bodies [][]byte
		var logs []string
		for _, use := range []func(io.Writer) func(http.Handler) http.Handler{mw.Logger, loggerRF} {
			var lb bytes.Buffer
			m := mm.New()
			m.Use(use(&lb))
			m.ServeFiles("/assets/*filepath", http.Dir(dir))
			srv := httptest.NewServer(m)
			resp, err := http.Get(srv.URL + "/assets/" + f)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			srv.Close()
			bodies = append(bodies, b)
			fs := strings.Fields(lb.String())
			logs = append(logs, strings.Join(fs[1:4], " "))
		}
		if !bytes.Equal(bodies[0], bodies[1]) || logs[0] != logs[1] {
			t.Fatalf("%s: responses or log fields differ: %q vs %q", f, logs[0], logs[1])
		}
	}
}

func TestEquivCleanPath(t *testing.T) {
	type seen struct{ path, raw, ua string }
	for _, target := range []string{"/docs/v2/", "/a//b/../c", "/a/./b", "/x%2Fy/../z", "/ok"} {
		var a, b seen
		capture := func(dst *seen) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				*dst = seen{r.URL.Path, r.URL.RawPath, r.Header.Get("User-Agent")}
			})
		}
		r1, r2 := realisticRequest("GET", target), realisticRequest("GET", target)
		mw.CleanPath()(capture(&a)).ServeHTTP(httptest.NewRecorder(), r1)
		cleanPathShallow()(capture(&b)).ServeHTTP(httptest.NewRecorder(), r2)
		if a != b || r1.URL.String() != r2.URL.String() {
			t.Fatalf("%s: %+v vs %+v", target, a, b)
		}
	}
}
