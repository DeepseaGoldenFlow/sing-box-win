//go:build !windows

package main

import "fmt"

// The broker pipe is a Windows named pipe; keep the command buildable on other
// platforms for vet/tests but refuse to pretend it can reach a service.
func runClient(action string) error {
	return fmt.Errorf("broker client mode is Windows-only (requested %s)", action)
}
