// Command loadgen is a real-socket load harness for MuxMaster (rmp #243,
// sprint 18 contention hunt).
//
// It has two modes, intended to run as SEPARATE OS PROCESSES so that a
// server-side mutex/block/CPU profile is not polluted by the load
// generator's own net/http.Transport connection-pool lock. An earlier,
// combined-process version of this harness showed 100% of captured mutex
// delay inside net/http.(*Transport).queueForIdleConn / tryPutIdleConn —
// client-side noise, not MuxMaster contention. See contention-hunt.md,
// "Methodology correction: separating client and server processes".
//
//	loadgen -mode=server -addr=127.0.0.1:PORT -profduration=Ns
//	loadgen -mode=client -target=127.0.0.1:PORT -conns=N -duration=Ns
package main

import (
	"fmt"
	"os"
	"strings"
)

// peekMode scans raw args for -mode=X / --mode=X / -mode X, without using
// package flag (which would choke on the OTHER mode's flags appearing in
// the same argv). Defaults to "server" when absent.
func peekMode(args []string) string {
	for i, a := range args {
		switch {
		case a == "-mode" || a == "--mode":
			if i+1 < len(args) {
				return args[i+1]
			}
		case strings.HasPrefix(a, "-mode="):
			return strings.TrimPrefix(a, "-mode=")
		case strings.HasPrefix(a, "--mode="):
			return strings.TrimPrefix(a, "--mode=")
		}
	}
	return "server"
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: loadgen -mode=server|client [flags]")
		os.Exit(2)
	}
	switch peekMode(os.Args[1:]) {
	case "server":
		runServer(os.Args[1:])
	case "client":
		runClient(os.Args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown -mode (want server|client)\n")
		os.Exit(2)
	}
}
