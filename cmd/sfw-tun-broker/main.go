// Command sfw-tun-broker is the narrow Windows service broker described by
// docs/proposal-tun-windows.md §0.1 and §0.2.6.
//
// P0 scope: it answers only hello/ping/version and a dry-run recover over the
// ACL-restricted named pipe. It does not create a TUN adapter, does not touch
// routes or DNS, does not start a host, and has no profile/subscription/URL/
// path command surface. Installing it is an administrator action performed by
// the operator on a test machine; this binary must not be registered from a
// desktop or build directory.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/JARVIS/sing-box-win/internal/platform"
)

// tokenInfo prints the secret-free token facts for one process (or this one).
// scripts/test-windows-service-security.ps1 uses it to prove the UI and broker
// run under different identities. It intentionally cannot print secrets.
func tokenInfo(args []string) error {
	pid := os.Getpid()
	if len(args) > 0 {
		v, err := strconv.Atoi(args[0])
		if err != nil || v <= 0 {
			return fmt.Errorf("invalid pid")
		}
		pid = v
	}
	info, err := platform.ProcessTokenInfo(pid)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "token-info":
			if err := tokenInfo(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		case "version":
			fmt.Println("sfw-tun-broker 0.1.0-p0 (dry-run only)")
			return
		case "hello", "ping", "recover":
			// Client mode against the installed service: one request/response
			// over the ACL-restricted pipe. P0 actions are read-only; recover is
			// always dry-run.
			if err := runClient(os.Args[1]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		case "run":
			// Only the SCM may launch this command.
		default:
			fmt.Fprintln(os.Stderr, "usage: sfw-tun-broker [run|version|hello|ping|recover|token-info [pid]]")
			os.Exit(2)
		}
	}
	if err := runService(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
