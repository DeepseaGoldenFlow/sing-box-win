//go:build !windows

package main

import "fmt"

// The broker is a Windows service. On other platforms the binary builds so the
// package can be vetted and unit tested, but it refuses to run.
func runService() error {
	return fmt.Errorf("the TUN broker service is Windows-only")
}
