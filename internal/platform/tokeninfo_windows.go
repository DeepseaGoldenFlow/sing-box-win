//go:build windows

package platform

// Process token inspection for scripts/test-windows-service-security.ps1.
// Dumping this is the evidence that the UI runs as the interactive user and the
// broker runs as the service identity (docs/proposal-tun-windows.md §0.1, §P0).
// The dump never includes secrets; a process token carries none.

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TokenInfo is a secret-free snapshot of a Windows access token.
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

func tokenInfoFromToken(token windows.Token, pid uint32) (TokenInfo, error) {
	info := TokenInfo{PID: pid}
	user, err := token.GetTokenUser()
	if err != nil {
		return info, err
	}
	info.UserSID = user.User.Sid.String()
	info.User = accountName(user.User.Sid)

	var outLen uint32
	if err = windows.GetTokenInformation(token, windows.TokenSessionId, (*byte)(unsafe.Pointer(&info.SessionID)), uint32(unsafe.Sizeof(info.SessionID)), &outLen); err != nil {
		return info, err
	}
	var elevationType uint32
	if err = windows.GetTokenInformation(token, windows.TokenElevationType, (*byte)(unsafe.Pointer(&elevationType)), uint32(unsafe.Sizeof(elevationType)), &outLen); err != nil {
		return info, err
	}
	info.ElevationType = elevationTypeName(elevationType)
	var elevated int32
	if err = windows.GetTokenInformation(token, windows.TokenElevation, (*byte)(unsafe.Pointer(&elevated)), uint32(unsafe.Sizeof(elevated)), &outLen); err != nil {
		return info, err
	}
	info.Elevation = elevated != 0

	// TokenIntegrityLevel returns a TOKEN_MANDATORY_LABEL whose SID follows the
	// fixed struct. Passing only unsafe.Sizeof(label) bytes as the buffer would
	// let the kernel write the SID past a stack variable, so use a real buffer
	// and read only the attributes word we need.
	labelBuf := make([]byte, 256)
	if err = windows.GetTokenInformation(token, windows.TokenIntegrityLevel, &labelBuf[0], uint32(len(labelBuf)), &outLen); err != nil {
		return info, err
	}
	label := (*windows.Tokenmandatorylabel)(unsafe.Pointer(&labelBuf[0]))
	info.IntegrityLevelRaw = label.Label.Attributes & 0x0000ffff
	info.IntegrityLevel = integrityName(info.IntegrityLevelRaw)

	if system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid); err == nil {
		info.IsLocalSystem = user.User.Sid.Equals(system)
	}
	if admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid); err == nil {
		member, _ := token.IsMember(admins)
		info.IsAdministrators = member
	}
	return info, nil
}

// ProcessTokenInfo inspects the primary token of pid.
func ProcessTokenInfo(pid int) (TokenInfo, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return TokenInfo{}, err
	}
	defer windows.CloseHandle(h)
	var token windows.Token
	if err = windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token); err != nil {
		return TokenInfo{}, err
	}
	defer token.Close()
	return tokenInfoFromToken(token, uint32(pid))
}

// CurrentTokenInfo inspects this process's own token.
func CurrentTokenInfo() (TokenInfo, error) {
	token := windows.GetCurrentProcessToken()
	return tokenInfoFromToken(token, windows.GetCurrentProcessId())
}

func accountName(sid *windows.SID) string {
	account, domain, _, err := sid.LookupAccount("")
	if err != nil {
		return ""
	}
	if domain == "" {
		return account
	}
	return domain + `\` + account
}

func elevationTypeName(v uint32) string {
	switch v {
	case 1:
		return "TokenElevationTypeDefault"
	case 2:
		return "TokenElevationTypeFull"
	case 3:
		return "TokenElevationTypeLimited"
	default:
		return fmt.Sprintf("unknown(%d)", v)
	}
}

func integrityName(v uint32) string {
	switch v {
	case 0x0000:
		return "Untrusted"
	case 0x1000:
		return "Low"
	case 0x2000:
		return "Medium"
	case 0x2100:
		return "MediumPlus"
	case 0x3000:
		return "High"
	case 0x4000:
		return "System"
	case 0x5000:
		return "Protected"
	default:
		return fmt.Sprintf("unknown(0x%x)", v)
	}
}

// TokenSummary renders a single-line, secret-free description for scripts and
// logs. It intentionally omits everything except identity and integrity.
func (t TokenInfo) TokenSummary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "pid=%d user=%s sid=%s session=%d elevation_type=%s elevated=%t integrity=%s system=%t admin=%t",
		t.PID, t.User, t.UserSID, t.SessionID, t.ElevationType, t.Elevation, t.IntegrityLevel, t.IsLocalSystem, t.IsAdministrators)
	return b.String()
}
