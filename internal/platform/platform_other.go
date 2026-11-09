//go:build !windows

package platform

import "fmt"

type ProxyBackup struct {
	Enabled    uint64 `json:"enabled"`
	Server     string `json:"server"`
	HadEnabled bool   `json:"had_enabled"`
	HadServer  bool   `json:"had_server"`
}

func ReadProxy() (ProxyBackup, error) {
	return ProxyBackup{}, fmt.Errorf("system proxy requires Windows")
}
func WriteProxy(ProxyBackup) error { return fmt.Errorf("system proxy requires Windows") }
func AutoStart(bool) error         { return nil }
func OpenUI(string) error          { return nil }
