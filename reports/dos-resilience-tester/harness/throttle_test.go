package dosharness

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

// TestThrottleIsGlobalNotPerIP validates H-026.
// The ThrottleBacklog middleware uses a single channel for all clients —
// a single attacker occupying the limit causes every other client to be
// rejected with 503. There is NO per-IP partitioning.
func TestThrottleIsGlobalNotPerIP(t *testing.T) {
	const limit = 5
	r := mm.New()
	release := make(chan struct{})
	r.Use(middleware.ThrottleBacklog(limit, 0, 1*time.Millisecond))
	r.GET("/slow", func(w http.ResponseWriter, req *http.Request) {
		<-release
	})
	r.GET("/fast", func(w http.ResponseWriter, req *http.Request) {})

	// Attacker fills the token budget with a single "IP" (127.0.0.1).
	var attackerWG sync.WaitGroup
	attackerWG.Add(limit)
	var attackerStatuses [limit]int
	for i := 0; i < limit; i++ {
		i := i
		go func() {
			defer attackerWG.Done()
			req := httptest.NewRequest(http.MethodGet, "/slow", nil)
			req.RemoteAddr = "1.2.3.4:9999" // attacker IP
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			attackerStatuses[i] = w.Code
		}()
	}

	// Give attacker goroutines a moment to grab the tokens.
	time.Sleep(50 * time.Millisecond)

	// Legit client — different IP — tries once. Should be rejected immediately
	// because all `limit` tokens are held by the attacker.
	legitStatuses := make([]int, 10)
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/fast", nil)
		req.RemoteAddr = "9.9.9.9:1234" // completely different IP
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		legitStatuses[i] = w.Code
	}

	// Release attackers.
	close(release)
	attackerWG.Wait()

	// Count how many legit clients got 503.
	denied := 0
	for _, s := range legitStatuses {
		if s == http.StatusServiceUnavailable {
			denied++
		}
	}
	t.Logf("legit client statuses: %v (denied=%d/10)", legitStatuses, denied)
	if denied < 8 {
		t.Errorf("expected global throttle to deny most legit clients while attacker holds limit, got only %d/10 denied", denied)
	}
}

// TestThrottleXFFSpoofDoesNotHelp confirms that real_ip middleware does NOT
// turn the global throttle into per-IP. The throttle still uses a single pool
// regardless of r.RemoteAddr. This means XFF spoofing neither bypasses the
// global limit nor exhausts per-IP state (there isn't any).
//
// This is informational: if a user stacks real_ip + throttle expecting
// per-IP limiting, they are WRONG.
func TestThrottleXFFSpoofInformational(t *testing.T) {
	const limit = 3
	r := mm.New()
	var acquired atomic.Int32
	release := make(chan struct{})
	r.Use(middleware.RealIP())
	r.Use(middleware.ThrottleBacklog(limit, 0, 1*time.Millisecond))
	r.GET("/", func(w http.ResponseWriter, req *http.Request) {
		acquired.Add(1)
		<-release
	})

	// Fire N>limit requests with different spoofed XFF — all from one source.
	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)
	var got [n]int
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("X-Forwarded-For", "10.0.0."+itos(i))
			req.RemoteAddr = "127.0.0.1:1"
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			got[i] = w.Code
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	denied := 0
	for _, s := range got {
		if s == http.StatusServiceUnavailable {
			denied++
		}
	}
	t.Logf("got statuses: %v; denied=%d/%d with limit=%d — XFF diversity did NOT partition the throttle", got, denied, n, limit)
	// Informational: assert that at least n - limit - slack are denied.
	if denied < n-limit-2 {
		t.Errorf("expected most-over-limit to be denied, got only %d/%d", denied, n)
	}
}

func itos(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}
