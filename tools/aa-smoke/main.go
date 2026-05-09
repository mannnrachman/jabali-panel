// aa-smoke — minimal AppArmor-rule verifier (M40.1).
//
// Single-binary that takes one or more unix-socket paths as args
// and tries to net.Dial("unix", ...) each. Exits 0 if every dial
// succeeds; non-zero on the first failure.
//
// Operator runs it under aa-exec to verify a profile actually allows
// the sockets the daemon needs to reach in production:
//
//   aa-exec -p jabali-panel -- /usr/local/bin/aa-smoke \
//     /var/run/mysqld/mysqld.sock \
//     /run/redis/redis.sock \
//     /run/jabali/agent.sock
//
// First failure halts with a clear stderr message; useful exit code
// for the make target that drives the full per-profile corpus.
//
// Why a custom binary (and not nc / ncat / curl): aa-exec attaches
// AppArmor's profile to the EXEC'd process. nc and curl have their
// own AA profiles in many distros that can muddy the test
// (transition-into-named-profile, conflicting capabilities). A
// minimal, single-purpose, no-deps binary keeps the AA mediation
// path under exact control.
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"time"
)

func main() {
	timeout := flag.Duration("timeout", 2*time.Second, "per-socket dial timeout")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: aa-smoke [--timeout=2s] <socket-path> [...]")
		os.Exit(2)
	}
	for _, p := range args {
		c, err := net.DialTimeout("unix", p, *timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL: %s — %v\n", p, err)
			os.Exit(1)
		}
		c.Close()
		fmt.Printf("OK: %s\n", p)
	}
}
