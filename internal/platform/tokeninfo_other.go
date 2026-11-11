//go:build !windows

package platform

import "fmt"

// TokenInfo exists on every platform so CLI code compiles; token elevation and
// integrity are Windows-only evidence and are reported as unavailable here.
type TokenInfo struct {
	PID               uint32 `json:"pid"`
	User              string `json:"user"`
	UserSID           string `json:"user_sid"`
	SessionID         uint32 `json:"session_id"`
	ElevationType     string `json:"elevation_type"`
	Elevation         bool   `json:"elevation"`
	IntegrityLevel    string `json:"integrity_level"`
	IsLocalSystem     bool   `json:"is_local_system"`
	IsAdministrators  bool   `json:"is_administrators"`
	IntegrityLevelRaw uint32 `json:"integrity_level_raw"`
}

func ProcessTokenInfo(pid int) (TokenInfo, error) {
	return TokenInfo{PID: uint32(pid)}, fmt.Errorf("token inspection requires Windows")
}

func CurrentTokenInfo() (TokenInfo, error) {
	return TokenInfo{}, fmt.Errorf("token inspection requires Windows")
}

func (t TokenInfo) TokenSummary() string {
	return fmt.Sprintf("pid=%d token inspection requires Windows", t.PID)
}
