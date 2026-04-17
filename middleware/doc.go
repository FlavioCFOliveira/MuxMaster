// Package middleware provides common HTTP middleware handlers for use with MuxMaster
// or any net/http-compatible router.
//
// Each middleware is a function that takes an http.Handler and returns an http.Handler,
// following the standard Go middleware pattern:
//
//	func(next http.Handler) http.Handler
//
// Usage with MuxMaster:
//
//	import "github.com/FlavioCFOliveira/MuxMaster/middleware"
//
//	r := muxmaster.New()
//	r.Use(middleware.Logger(os.Stdout))
//	r.Use(middleware.Recoverer)
//	r.Use(middleware.CORS(middleware.CORSOptions{
//	    AllowedOrigins: []string{"https://example.com"},
//	}))
package middleware
