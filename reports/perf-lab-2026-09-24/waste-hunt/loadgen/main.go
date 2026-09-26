// Command loadgen is the scenario-driven HTTP load generator of the sprint-18
// waste hunt (rmp #249). It runs as a SEPARATE OS process from the profiled
// example server, so the server-side CPU/alloc profiles and strace counts
// contain no load-generator work.
//
// A scenario (JSON, see ../scenarios/) lists the requests to drive, each with
// a weight and an expected status code. Every response status is recorded per
// request name, so the report can prove that the load exercised the intended
// code paths (a 404 storm would otherwise masquerade as a fast route).
//
//	loadgen -scenario ../scenarios/rest-api.json -target 127.0.0.1:8080 \
//	        -conns 32 -duration 10s
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type setupStep struct {
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	Headers     map[string]string `json:"headers"`
	Body        string            `json:"body"`
	CaptureJSON string            `json:"capture_json"` // top-level JSON string field to capture
	As          string            `json:"as"`           // variable name, referenced as ${NAME}
}

type reqSpec struct {
	Name            string            `json:"name"`
	Method          string            `json:"method"`
	Path            string            `json:"path"`
	Headers         map[string]string `json:"headers"`
	Body            string            `json:"body"`
	MultipartFileKB int               `json:"multipart_file_kb"` // >0: body is a multipart form with one file of N KiB
	MultipartField  string            `json:"multipart_field"`
	Weight          int               `json:"weight"`
	Expect          int               `json:"expect"`
}

type streamSpec struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

type scenario struct {
	Name     string       `json:"name"`
	Setup    []setupStep  `json:"setup"`
	Requests []reqSpec    `json:"requests"`
	Streams  []streamSpec `json:"streams"`
}

// prepared is a request whose body and headers are fully materialised once,
// so the per-request cost in this process is minimal and deterministic.
type prepared struct {
	spec    reqSpec
	body    []byte
	headers http.Header
}

type counters struct {
	mu     sync.Mutex
	status map[string]map[int]int64
	errs   map[string]int64
}

func (c *counters) add(name string, code int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.errs[name]++
		return
	}
	m := c.status[name]
	if m == nil {
		m = make(map[int]int64)
		c.status[name] = m
	}
	m[code]++
}

func main() {
	scenarioPath := flag.String("scenario", "", "scenario JSON file (required)")
	target := flag.String("target", "127.0.0.1:8080", "server host:port")
	conns := flag.Int("conns", 32, "concurrent keep-alive workers")
	duration := flag.Duration("duration", 10*time.Second, "load duration")
	flag.Parse()
	if *scenarioPath == "" {
		fmt.Fprintln(os.Stderr, "-scenario is required")
		os.Exit(2)
	}
	raw, err := os.ReadFile(*scenarioPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var sc scenario
	if err := json.Unmarshal(raw, &sc); err != nil {
		fmt.Fprintln(os.Stderr, "scenario:", err)
		os.Exit(2)
	}
	base := "http://" + *target

	tr := &http.Transport{
		MaxIdleConns:        *conns * 2,
		MaxIdleConnsPerHost: *conns * 2,
		IdleConnTimeout:     90 * time.Second,
		// The scenario states Accept-Encoding explicitly where a browser-like
		// client is intended; the transport must not add or strip it.
		DisableCompression: true,
	}
	client := &http.Client{
		Transport: tr,
		// Redirects are part of what we measure (e.g. RedirectTrailingSlash);
		// never follow them, record the 3xx instead.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	vars := map[string]string{}
	for _, st := range sc.Setup {
		if err := runSetup(client, base, st, vars); err != nil {
			fmt.Fprintln(os.Stderr, "setup failed:", err)
			os.Exit(1)
		}
	}

	var plan []prepared
	for _, rs := range sc.Requests {
		p, err := prepare(rs, vars)
		if err != nil {
			fmt.Fprintln(os.Stderr, "prepare", rs.Name, err)
			os.Exit(1)
		}
		w := max(rs.Weight, 1)
		for range w {
			plan = append(plan, p)
		}
	}

	cnt := &counters{status: map[string]map[int]int64{}, errs: map[string]int64{}}
	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Long-lived streams (SSE). Each stream reads until stop.
	var streamBytes atomic.Int64
	var streamWG sync.WaitGroup
	streamClient := &http.Client{Transport: &http.Transport{DisableCompression: true, MaxIdleConnsPerHost: 4096}}
	for _, ss := range sc.Streams {
		for range ss.Count {
			streamWG.Add(1)
			go func(path string) {
				defer streamWG.Done()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
				req.Header.Set("Accept", "text/event-stream")
				go func() { <-stop; cancel() }()
				resp, err := streamClient.Do(req)
				if err != nil {
					cnt.add("stream "+path, 0, err)
					return
				}
				cnt.add("stream "+path, resp.StatusCode, nil)
				buf := make([]byte, 4096)
				for {
					n, err := resp.Body.Read(buf)
					streamBytes.Add(int64(n))
					if err != nil {
						break
					}
				}
				_ = resp.Body.Close()
			}(ss.Path)
		}
	}
	if len(sc.Streams) > 0 {
		time.Sleep(500 * time.Millisecond) // let subscribers attach
	}

	type lat struct{ samples []int64 }
	lats := make([]lat, *conns)
	var total atomic.Int64
	start := time.Now()
	for i := range *conns {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			n := idx // stagger the round-robin position per worker
			for {
				select {
				case <-stop:
					return
				default:
				}
				p := plan[n%len(plan)]
				n++
				var body io.Reader
				if p.body != nil {
					body = bytes.NewReader(p.body)
				}
				req, _ := http.NewRequest(p.spec.Method, base+p.spec.Path, body)
				for k, v := range p.headers {
					req.Header[k] = v
				}
				t0 := time.Now()
				resp, err := client.Do(req)
				if err != nil {
					cnt.add(p.spec.Name, 0, err)
					continue
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				el := time.Since(t0)
				cnt.add(p.spec.Name, resp.StatusCode, nil)
				if len(lats[idx].samples) < 20000 {
					lats[idx].samples = append(lats[idx].samples, el.Nanoseconds())
				}
				total.Add(1)
			}
		}(i)
	}
	time.Sleep(*duration)
	close(stop)
	wg.Wait()
	elapsed := time.Since(start)
	streamWG.Wait()

	var all []int64
	for _, l := range lats {
		all = append(all, l.samples...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	pct := func(p float64) time.Duration {
		if len(all) == 0 {
			return 0
		}
		i := min(int(p*float64(len(all))), len(all)-1)
		return time.Duration(all[i])
	}

	fmt.Printf("scenario=%s conns=%d duration=%s\n", sc.Name, *conns, elapsed.Round(time.Millisecond))
	fmt.Printf("requests=%d req/s=%.0f p50=%s p90=%s p99=%s\n",
		total.Load(), float64(total.Load())/elapsed.Seconds(), pct(0.50), pct(0.90), pct(0.99))
	if len(sc.Streams) > 0 {
		fmt.Printf("stream_bytes=%d\n", streamBytes.Load())
	}
	// Per-request status accounting, with mismatches flagged.
	names := make([]string, 0, len(cnt.status))
	seen := map[string]bool{}
	for _, rs := range sc.Requests {
		if !seen[rs.Name] {
			names = append(names, rs.Name)
			seen[rs.Name] = true
		}
	}
	for k := range cnt.status {
		if !seen[k] {
			names = append(names, k)
			seen[k] = true
		}
	}
	expect := map[string]int{}
	for _, rs := range sc.Requests {
		expect[rs.Name] = rs.Expect
	}
	mismatch := false
	for _, n := range names {
		var parts []string
		codes := make([]int, 0)
		for c := range cnt.status[n] {
			codes = append(codes, c)
		}
		sort.Ints(codes)
		for _, c := range codes {
			parts = append(parts, fmt.Sprintf("%d×%d", c, cnt.status[n][c]))
			if e := expect[n]; e != 0 && c != e {
				mismatch = true
			}
		}
		if cnt.errs[n] > 0 {
			parts = append(parts, fmt.Sprintf("ERR×%d", cnt.errs[n]))
			mismatch = true
		}
		fmt.Printf("  %-40s expect=%d got %s\n", n, expect[n], strings.Join(parts, " "))
	}
	if mismatch {
		fmt.Println("STATUS-MISMATCH: at least one request returned an unexpected status or error")
	} else {
		fmt.Println("STATUS-OK: every response matched its expected status")
	}
}

func expand(s string, vars map[string]string) string {
	for k, v := range vars {
		s = strings.ReplaceAll(s, "${"+k+"}", v)
	}
	return s
}

func prepare(rs reqSpec, vars map[string]string) (prepared, error) {
	p := prepared{spec: rs, headers: http.Header{}}
	p.spec.Path = expand(rs.Path, vars)
	for k, v := range rs.Headers {
		p.headers.Set(k, expand(v, vars))
	}
	switch {
	case rs.MultipartFileKB > 0:
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.SetBoundary("wastehuntboundary0123456789")
		field := rs.MultipartField
		if field == "" {
			field = "file"
		}
		fw, err := mw.CreateFormFile(field, "payload.bin")
		if err != nil {
			return p, err
		}
		chunk := bytes.Repeat([]byte("0123456789abcdef"), 64) // 1 KiB, deterministic
		for range rs.MultipartFileKB {
			_, _ = fw.Write(chunk)
		}
		_ = mw.Close()
		p.body = buf.Bytes()
		p.headers.Set("Content-Type", mw.FormDataContentType())
	case rs.Body != "":
		p.body = []byte(expand(rs.Body, vars))
	}
	return p, nil
}

func runSetup(client *http.Client, base string, st setupStep, vars map[string]string) error {
	var body io.Reader
	if st.Body != "" {
		body = strings.NewReader(expand(st.Body, vars))
	}
	req, err := http.NewRequest(st.Method, base+expand(st.Path, vars), body)
	if err != nil {
		return err
	}
	for k, v := range st.Headers {
		req.Header.Set(k, expand(v, vars))
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if st.CaptureJSON != "" {
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			return fmt.Errorf("setup %s %s: %w (body %q)", st.Method, st.Path, err, data)
		}
		v, ok := m[st.CaptureJSON].(string)
		if !ok {
			return fmt.Errorf("setup %s %s: field %q missing in %s", st.Method, st.Path, st.CaptureJSON, data)
		}
		vars[st.As] = v
	}
	return nil
}
