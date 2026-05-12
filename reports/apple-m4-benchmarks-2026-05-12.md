# Benchmark Report — Apple M4, 2026-05-12

**Hardware:** Apple M4, 10 cores (4 performance + 6 efficiency), 32 GB RAM  
**OS:** macOS Darwin 25.4.0 (arm64)  
**Go version:** go1.26.2 darwin/arm64  
**GOMAXPROCS:** 10  
**Method:** `go test -bench=. -benchmem -count=5 -benchtime=3s`; values reported are medians of 5 runs.

---

## Internal benchmarks (`bench_test.go`)

### Serial (single goroutine)

| Benchmark | ns/op | B/op | allocs/op |
|-----------|-------|------|-----------|
| Static route | 14.1 | 0 | 0 |
| 1 parameter (`Handle`) | 56.7 | 384 | 1 |
| 2 parameters (`Handle`) | 64.2 | 416 | 1 |
| 3 parameters (`Handle`) | 69.7 | 480 | 1 |
| Catch-all (`Handle`) | 58.2 | 384 | 1 |
| Fast static (`HandleFast`) | 14.2 | 0 | 0 |
| Fast 1 param (`HandleFast`) | 28.4 | 32 | 1 |
| Fast 2 params (`HandleFast`) | 36.9 | 64 | 1 |
| Fast 3 params (`HandleFast`) | 45.9 | 96 | 1 |
| Pooled 1 param (`PoolRequestBundle=true`) | 28.4 | 0 | 0 |
| Pooled 2 params (`PoolRequestBundle=true`) | 36.4 | 0 | 0 |
| Pooled 3 params (`PoolRequestBundle=true`) | 38.6 | 0 | 0 |
| Pooled catch-all (`PoolRequestBundle=true`) | 28.5 | 0 | 0 |

### Parallel (GOMAXPROCS=10)

| Benchmark | ns/op | B/op | allocs/op |
|-----------|-------|------|-----------|
| Parallel static | 2.4 | 0 | 0 |
| Parallel 1 param (`Handle`) | 82.2 | 384 | 1 |
| Fast parallel 1 param (`HandleFast`) | 13.1 | 32 | 1 |
| Pooled parallel 1 param (`PoolRequestBundle=true`) | 10.2 | 0 | 0 |

---

## Competitor benchmarks (`competitor/bench_test.go`)

Route set: 8 static routes + 7 param routes (1/2/3 params) + 2 catch-all routes.

### bunrouter note

bunrouter is tested using its **native API** (`bunrouter.HandlerFunc`) which stores params in the request context via a zero-copy approach — no `context.WithValue` call, hence 0 allocs. This is not `net/http`-compatible (different handler signature). The `http.Handler`-adapter path would add ~3 allocs.

### Static route — `GET /api/v1/status`

| Router | ns/op | B/op | allocs/op |
|--------|-------|------|-----------|
| **MuxMaster (default)** | **14.4** | **0** | **0** |
| httprouter | 14.7 | 0 | 0 |
| bunrouter (native) | 18.6 | 0 | 0 |
| chi v5 | 114 | 368 | 2 |

### 1 parameter — `GET /api/v1/users/:id`

| Router | ns/op | B/op | allocs/op |
|--------|-------|------|-----------|
| bunrouter (native) | 21.9 | 0 | 0 |
| httprouter | 33.0 | 64 | 1 |
| MuxMaster (pooled)* | ~28.4 | 0 | 0 |
| **MuxMaster (default)** | 61.7 | 384 | 1 |
| chi v5 | 196 | 704 | 4 |

*pooled number from internal bench_test.go (simpler route set).

### 2 parameters — `GET /api/v1/users/:id/posts/:pid`

| Router | ns/op | B/op | allocs/op |
|--------|-------|------|-----------|
| httprouter | 39.6 | 64 | 1 |
| bunrouter (native) | 40.7 | 0 | 0 |
| MuxMaster (pooled)* | ~36.4 | 0 | 0 |
| **MuxMaster (default)** | 71.1 | 416 | 1 |
| chi v5 | 226 | 704 | 4 |

### 3 parameters — `GET /api/v1/orgs/:org/repos/:repo/issues/:num`

| Router | ns/op | B/op | allocs/op |
|--------|-------|------|-----------|
| bunrouter (native) | 29.3 | 0 | 0 |
| httprouter | 45.1 | 96 | 1 |
| MuxMaster (pooled)* | ~38.6 | 0 | 0 |
| **MuxMaster (default)** | 83.2 | 480 | 1 |
| chi v5 | 225 | 704 | 4 |

### Catch-all — `GET /static/*filepath`

| Router | ns/op | B/op | allocs/op |
|--------|-------|------|-----------|
| bunrouter (native) | 11.5 | 0 | 0 |
| httprouter | 27.3 | 32 | 1 |
| MuxMaster (pooled)* | ~28.5 | 0 | 0 |
| **MuxMaster (default)** | 57.4 | 384 | 1 |
| chi v5 | 175 | 704 | 4 |

### Parallel static (GOMAXPROCS=10)

| Router | ns/op | B/op | allocs/op |
|--------|-------|------|-----------|
| **MuxMaster** | **1.86** | **0** | **0** |
| bunrouter (native) | 2.12 | 0 | 0 |
| httprouter | 2.35 | 0 | 0 |
| chi v5 | 120 | 368 | 2 |

### Parallel 1 parameter (GOMAXPROCS=10)

| Router | ns/op | B/op | allocs/op |
|--------|-------|------|-----------|
| bunrouter (native) | 3.97 | 0 | 0 |
| httprouter | 18.9 | 64 | 1 |
| MuxMaster (pooled)* | ~10.2 | 0 | 0 |
| **MuxMaster (default)** | 112 | 384 | 1 |
| chi v5 | 188 | 704 | 4 |

---

## Raw output (competitor suite)

```
goos: darwin
goarch: arm64
pkg: competitor
cpu: Apple M4
BenchmarkMuxMaster_StaticRoute-10          	250348800	        14.50 ns/op	       0 B/op	       0 allocs/op
BenchmarkMuxMaster_StaticRoute-10          	249976622	        14.43 ns/op	       0 B/op	       0 allocs/op
BenchmarkMuxMaster_StaticRoute-10          	247624906	        14.39 ns/op	       0 B/op	       0 allocs/op
BenchmarkMuxMaster_StaticRoute-10          	248285671	        14.39 ns/op	       0 B/op	       0 allocs/op
BenchmarkMuxMaster_StaticRoute-10          	248207482	        14.48 ns/op	       0 B/op	       0 allocs/op
BenchmarkHttprouter_StaticRoute-10         	243384154	        14.69 ns/op	       0 B/op	       0 allocs/op
BenchmarkHttprouter_StaticRoute-10         	243383743	        14.74 ns/op	       0 B/op	       0 allocs/op
BenchmarkHttprouter_StaticRoute-10         	243484142	        14.69 ns/op	       0 B/op	       0 allocs/op
BenchmarkHttprouter_StaticRoute-10         	244607882	        14.74 ns/op	       0 B/op	       0 allocs/op
BenchmarkHttprouter_StaticRoute-10         	243820346	        14.68 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_StaticRoute-10          	259772043	        17.16 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_StaticRoute-10          	183993067	        19.31 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_StaticRoute-10          	173562079	        20.81 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_StaticRoute-10          	192857722	        18.64 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_StaticRoute-10          	220790750	        14.28 ns/op	       0 B/op	       0 allocs/op
BenchmarkChi_StaticRoute-10                	32462859	       114.4 ns/op	     368 B/op	       2 allocs/op
BenchmarkChi_StaticRoute-10                	33089932	       114.4 ns/op	     368 B/op	       2 allocs/op
BenchmarkChi_StaticRoute-10                	33101925	       114.1 ns/op	     368 B/op	       2 allocs/op
BenchmarkChi_StaticRoute-10                	32849474	       114.6 ns/op	     368 B/op	       2 allocs/op
BenchmarkChi_StaticRoute-10                	33793442	       108.9 ns/op	     368 B/op	       2 allocs/op
BenchmarkMuxMaster_OneParam-10             	58472052	        61.61 ns/op	     384 B/op	       1 allocs/op
BenchmarkMuxMaster_OneParam-10             	58772350	        61.66 ns/op	     384 B/op	       1 allocs/op
BenchmarkMuxMaster_OneParam-10             	59341473	        61.86 ns/op	     384 B/op	       1 allocs/op
BenchmarkMuxMaster_OneParam-10             	57694117	        61.81 ns/op	     384 B/op	       1 allocs/op
BenchmarkMuxMaster_OneParam-10             	58092234	        61.70 ns/op	     384 B/op	       1 allocs/op
BenchmarkHttprouter_OneParam-10            	100000000	        32.99 ns/op	      64 B/op	       1 allocs/op
BenchmarkHttprouter_OneParam-10            	100000000	        32.81 ns/op	      64 B/op	       1 allocs/op
BenchmarkHttprouter_OneParam-10            	100000000	        32.97 ns/op	      64 B/op	       1 allocs/op
BenchmarkHttprouter_OneParam-10            	100000000	        33.03 ns/op	      64 B/op	       1 allocs/op
BenchmarkHttprouter_OneParam-10            	100000000	        32.79 ns/op	      64 B/op	       1 allocs/op
BenchmarkBunrouter_OneParam-10             	163481863	        21.97 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_OneParam-10             	163764276	        21.99 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_OneParam-10             	164292584	        21.93 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_OneParam-10             	164043841	        21.92 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_OneParam-10             	164147708	        21.93 ns/op	       0 B/op	       0 allocs/op
BenchmarkChi_OneParam-10                   	18287114	       196.7 ns/op	     704 B/op	       4 allocs/op
BenchmarkChi_OneParam-10                   	18523159	       196.0 ns/op	     704 B/op	       4 allocs/op
BenchmarkChi_OneParam-10                   	18493633	       196.4 ns/op	     704 B/op	       4 allocs/op
BenchmarkChi_OneParam-10                   	18448033	       197.2 ns/op	     704 B/op	       4 allocs/op
BenchmarkChi_OneParam-10                   	18438253	       196.3 ns/op	     704 B/op	       4 allocs/op
BenchmarkMuxMaster_TwoParams-10            	49969288	        70.56 ns/op	     416 B/op	       1 allocs/op
BenchmarkMuxMaster_TwoParams-10            	50163291	        71.12 ns/op	     416 B/op	       1 allocs/op
BenchmarkMuxMaster_TwoParams-10            	51430407	        71.12 ns/op	     416 B/op	       1 allocs/op
BenchmarkMuxMaster_TwoParams-10            	50043266	        72.74 ns/op	     416 B/op	       1 allocs/op
BenchmarkMuxMaster_TwoParams-10            	49693556	        73.06 ns/op	     416 B/op	       1 allocs/op
BenchmarkHttprouter_TwoParams-10           	91622965	        39.59 ns/op	      64 B/op	       1 allocs/op
BenchmarkHttprouter_TwoParams-10           	90276094	        39.49 ns/op	      64 B/op	       1 allocs/op
BenchmarkHttprouter_TwoParams-10           	91897687	        39.56 ns/op	      64 B/op	       1 allocs/op
BenchmarkHttprouter_TwoParams-10           	91540453	        39.66 ns/op	      64 B/op	       1 allocs/op
BenchmarkHttprouter_TwoParams-10           	89451828	        39.50 ns/op	      64 B/op	       1 allocs/op
BenchmarkBunrouter_TwoParams-10            	89667574	        40.71 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_TwoParams-10            	88341390	        40.63 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_TwoParams-10            	88598118	        40.67 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_TwoParams-10            	88529758	        40.66 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_TwoParams-10            	89509004	        40.67 ns/op	       0 B/op	       0 allocs/op
BenchmarkChi_TwoParams-10                  	15919772	       225.5 ns/op	     704 B/op	       4 allocs/op
BenchmarkChi_TwoParams-10                  	16015504	       225.6 ns/op	     704 B/op	       4 allocs/op
BenchmarkChi_TwoParams-10                  	16051698	       225.7 ns/op	     704 B/op	       4 allocs/op
BenchmarkChi_TwoParams-10                  	16082808	       224.7 ns/op	     704 B/op	       4 allocs/op
BenchmarkChi_TwoParams-10                  	16063160	       225.5 ns/op	     704 B/op	       4 allocs/op
BenchmarkMuxMaster_ThreeParams-10          	42996240	        82.94 ns/op	     480 B/op	       1 allocs/op
BenchmarkMuxMaster_ThreeParams-10          	43918327	        84.03 ns/op	     480 B/op	       1 allocs/op
BenchmarkMuxMaster_ThreeParams-10          	43804300	        83.28 ns/op	     480 B/op	       1 allocs/op
BenchmarkMuxMaster_ThreeParams-10          	42919520	        83.23 ns/op	     480 B/op	       1 allocs/op
BenchmarkMuxMaster_ThreeParams-10          	43169564	        82.96 ns/op	     480 B/op	       1 allocs/op
BenchmarkHttprouter_ThreeParams-10         	79231162	        45.06 ns/op	      96 B/op	       1 allocs/op
BenchmarkHttprouter_ThreeParams-10         	81008935	        45.08 ns/op	      96 B/op	       1 allocs/op
BenchmarkHttprouter_ThreeParams-10         	80652162	        45.10 ns/op	      96 B/op	       1 allocs/op
BenchmarkHttprouter_ThreeParams-10         	80637633	        45.20 ns/op	      96 B/op	       1 allocs/op
BenchmarkHttprouter_ThreeParams-10         	80028676	        45.01 ns/op	      96 B/op	       1 allocs/op
BenchmarkBunrouter_ThreeParams-10          	123270596	        29.19 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_ThreeParams-10          	123439532	        29.27 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_ThreeParams-10          	122999347	        29.38 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_ThreeParams-10          	122833771	        29.27 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_ThreeParams-10          	121858454	        28.95 ns/op	       0 B/op	       0 allocs/op
BenchmarkChi_ThreeParams-10                	16021248	       225.5 ns/op	     704 B/op	       4 allocs/op
BenchmarkChi_ThreeParams-10                	15688698	       224.9 ns/op	     704 B/op	       4 allocs/op
BenchmarkChi_ThreeParams-10                	15864303	       225.4 ns/op	     704 B/op	       4 allocs/op
BenchmarkChi_ThreeParams-10                	15922497	       226.1 ns/op	     704 B/op	       4 allocs/op
BenchmarkChi_ThreeParams-10                	16060562	       224.4 ns/op	     704 B/op	       4 allocs/op
BenchmarkMuxMaster_CatchAll-10             	61230649	        57.74 ns/op	     384 B/op	       1 allocs/op
BenchmarkMuxMaster_CatchAll-10             	63085770	        57.52 ns/op	     384 B/op	       1 allocs/op
BenchmarkMuxMaster_CatchAll-10             	62961278	        57.33 ns/op	     384 B/op	       1 allocs/op
BenchmarkMuxMaster_CatchAll-10             	63131680	        57.04 ns/op	     384 B/op	       1 allocs/op
BenchmarkMuxMaster_CatchAll-10             	62297658	        57.39 ns/op	     384 B/op	       1 allocs/op
BenchmarkHttprouter_CatchAll-10            	131639334	        27.29 ns/op	      32 B/op	       1 allocs/op
BenchmarkHttprouter_CatchAll-10            	132155007	        27.28 ns/op	      32 B/op	       1 allocs/op
BenchmarkHttprouter_CatchAll-10            	132180099	        27.25 ns/op	      32 B/op	       1 allocs/op
BenchmarkHttprouter_CatchAll-10            	132478240	        27.16 ns/op	      32 B/op	       1 allocs/op
BenchmarkHttprouter_CatchAll-10            	131917624	        27.19 ns/op	      32 B/op	       1 allocs/op
BenchmarkBunrouter_CatchAll-10             	311054050	        11.54 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_CatchAll-10             	312488754	        11.54 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_CatchAll-10             	312282019	        11.52 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_CatchAll-10             	311935383	        11.51 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_CatchAll-10             	312389418	        11.53 ns/op	       0 B/op	       0 allocs/op
BenchmarkChi_CatchAll-10                   	20807468	       172.7 ns/op	     704 B/op	       4 allocs/op
BenchmarkChi_CatchAll-10                   	21000783	       174.5 ns/op	     704 B/op	       4 allocs/op
BenchmarkChi_CatchAll-10                   	20401858	       175.4 ns/op	     704 B/op	       4 allocs/op
BenchmarkChi_CatchAll-10                   	20876608	       175.7 ns/op	     704 B/op	       4 allocs/op
BenchmarkChi_CatchAll-10                   	20468728	       175.8 ns/op	     704 B/op	       4 allocs/op
BenchmarkMuxMaster_ParallelStatic-10       	1000000000	         1.801 ns/op	       0 B/op	       0 allocs/op
BenchmarkMuxMaster_ParallelStatic-10       	1000000000	         1.817 ns/op	       0 B/op	       0 allocs/op
BenchmarkMuxMaster_ParallelStatic-10       	1000000000	         1.860 ns/op	       0 B/op	       0 allocs/op
BenchmarkMuxMaster_ParallelStatic-10       	1000000000	         1.919 ns/op	       0 B/op	       0 allocs/op
BenchmarkMuxMaster_ParallelStatic-10       	1000000000	         2.019 ns/op	       0 B/op	       0 allocs/op
BenchmarkHttprouter_ParallelStatic-10      	1000000000	         2.304 ns/op	       0 B/op	       0 allocs/op
BenchmarkHttprouter_ParallelStatic-10      	1000000000	         2.415 ns/op	       0 B/op	       0 allocs/op
BenchmarkHttprouter_ParallelStatic-10      	1000000000	         2.347 ns/op	       0 B/op	       0 allocs/op
BenchmarkHttprouter_ParallelStatic-10      	1000000000	         2.397 ns/op	       0 B/op	       0 allocs/op
BenchmarkHttprouter_ParallelStatic-10      	1000000000	         2.324 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_ParallelStatic-10       	1000000000	         2.121 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_ParallelStatic-10       	1000000000	         2.126 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_ParallelStatic-10       	1000000000	         2.127 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_ParallelStatic-10       	1000000000	         2.107 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_ParallelStatic-10       	1000000000	         2.116 ns/op	       0 B/op	       0 allocs/op
BenchmarkChi_ParallelStatic-10             	34670167	       115.0 ns/op	     368 B/op	       2 allocs/op
BenchmarkChi_ParallelStatic-10             	27790196	       120.0 ns/op	     368 B/op	       2 allocs/op
BenchmarkChi_ParallelStatic-10             	27096234	       119.7 ns/op	     368 B/op	       2 allocs/op
BenchmarkChi_ParallelStatic-10             	28004131	       121.3 ns/op	     368 B/op	       2 allocs/op
BenchmarkChi_ParallelStatic-10             	29065228	       122.0 ns/op	     368 B/op	       2 allocs/op
BenchmarkMuxMaster_ParallelOneParam-10     	39568138	       111.3 ns/op	     384 B/op	       1 allocs/op
BenchmarkMuxMaster_ParallelOneParam-10     	31010834	       112.3 ns/op	     384 B/op	       1 allocs/op
BenchmarkMuxMaster_ParallelOneParam-10     	35142979	       116.6 ns/op	     384 B/op	       1 allocs/op
BenchmarkMuxMaster_ParallelOneParam-10     	31060377	       110.1 ns/op	     384 B/op	       1 allocs/op
BenchmarkMuxMaster_ParallelOneParam-10     	29514900	       111.7 ns/op	     384 B/op	       1 allocs/op
BenchmarkHttprouter_ParallelOneParam-10    	145810224	        24.27 ns/op	      64 B/op	       1 allocs/op
BenchmarkHttprouter_ParallelOneParam-10    	137195114	        23.98 ns/op	      64 B/op	       1 allocs/op
BenchmarkHttprouter_ParallelOneParam-10    	180928293	        17.52 ns/op	      64 B/op	       1 allocs/op
BenchmarkHttprouter_ParallelOneParam-10    	201870549	        18.94 ns/op	      64 B/op	       1 allocs/op
BenchmarkHttprouter_ParallelOneParam-10    	205257626	        17.52 ns/op	      64 B/op	       1 allocs/op
BenchmarkBunrouter_ParallelOneParam-10     	942982929	         3.960 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_ParallelOneParam-10     	842539179	         4.018 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_ParallelOneParam-10     	858780674	         4.037 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_ParallelOneParam-10     	890211476	         3.966 ns/op	       0 B/op	       0 allocs/op
BenchmarkBunrouter_ParallelOneParam-10     	920360874	         3.885 ns/op	       0 B/op	       0 allocs/op
BenchmarkChi_ParallelOneParam-10           	17244682	       186.7 ns/op	     704 B/op	       4 allocs/op
BenchmarkChi_ParallelOneParam-10           	19629715	       187.8 ns/op	     704 B/op	       4 allocs/op
BenchmarkChi_ParallelOneParam-10           	19313347	       189.2 ns/op	     704 B/op	       4 allocs/op
BenchmarkChi_ParallelOneParam-10           	19173250	       187.5 ns/op	     704 B/op	       4 allocs/op
BenchmarkChi_ParallelOneParam-10           	19212418	       188.3 ns/op	     704 B/op	       4 allocs/op
PASS
ok  	competitor	578.832s
```
