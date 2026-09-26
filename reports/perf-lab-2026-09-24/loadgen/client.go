package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// runClient drives -conns concurrent, persistent keep-alive connections
// against -target for -duration, then reports throughput and latency
// percentiles. It does NOT enable Go runtime profiling — that happens in the
// server process, which is what the contention hunt is measuring.
func runClient(args []string) {
	fs := flag.NewFlagSet("client", flag.ExitOnError)
	target := fs.String("target", "", "server address, host:port (required)")
	conns := fs.Int("conns", 1000, "number of concurrent persistent keep-alive connections")
	duration := fs.Duration("duration", 5*time.Second, "load duration")
	_ = fs.Parse(args[1:])

	if *target == "" {
		fmt.Println("error: -target is required in client mode")
		return
	}

	if soft, err := maxOpenFiles(); err == nil {
		fmt.Printf("ulimit -n (soft): %d\n", soft)
	}

	base := "http://" + *target
	fmt.Printf("=== loadgen client: conns=%d duration=%s target=%s ===\n", *conns, *duration, base)

	jwtToken := hs256Token("loadgen")
	paths := []string{
		"/static/list",
		"/users/42",
		"/users/42/posts/7/comments/99",
		"/static/assets/css/app.css",
		"/throttled/42",
		"/secure/42",
	}

	tr := &http.Transport{
		MaxIdleConnsPerHost: *conns + 10,
		MaxConnsPerHost:     0,
		IdleConnTimeout:     90 * time.Second,
	}
	client := &http.Client{Transport: tr}

	const sampleCap = 512 // ring buffer per worker — bounds memory at high conns
	type worker struct {
		samples [sampleCap]int64
		count   int64
		reqs    int64
	}
	workers := make([]worker, *conns)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var totalErrs int64

	for i := 0; i < *conns; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			shard := strconv.Itoa(idx % 16)
			w := &workers[idx]
			n := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				p := paths[n%len(paths)]
				n++
				req, _ := http.NewRequest(http.MethodGet, base+p, nil)
				req.Header.Set("X-Shard", shard)
				if p == "/secure/42" {
					req.Header.Set("Authorization", "Bearer "+jwtToken)
				}
				start := time.Now()
				resp, err := client.Do(req)
				elapsed := time.Since(start)
				if err != nil {
					atomic.AddInt64(&totalErrs, 1)
					continue
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				slot := atomic.AddInt64(&w.count, 1) % sampleCap
				w.samples[slot] = elapsed.Nanoseconds()
				atomic.AddInt64(&w.reqs, 1)
			}
		}(i)
	}

	time.Sleep(200 * time.Millisecond) // let connections establish
	time.Sleep(*duration)
	close(stop)
	wg.Wait()

	var totalReqs int64
	var allSamples []int64
	for i := range workers {
		w := &workers[i]
		totalReqs += w.reqs
		n := int(w.count)
		if n > sampleCap {
			n = sampleCap
		}
		allSamples = append(allSamples, w.samples[:n]...)
	}
	sort.Slice(allSamples, func(i, j int) bool { return allSamples[i] < allSamples[j] })

	p50 := percentile(allSamples, 0.50)
	p90 := percentile(allSamples, 0.90)
	p99 := percentile(allSamples, 0.99)
	p999 := percentile(allSamples, 0.999)

	rps := float64(totalReqs) / duration.Seconds()
	fmt.Printf("requests=%d errors=%d req/s=%.0f\n", totalReqs, totalErrs, rps)
	fmt.Printf("latency p50=%s p90=%s p99=%s p999=%s\n",
		time.Duration(p50), time.Duration(p90), time.Duration(p99), time.Duration(p999))
}

func percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(p * float64(len(sorted)))
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func maxOpenFiles() (uint64, error) {
	var rlim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rlim); err != nil {
		return 0, err
	}
	return rlim.Cur, nil
}
