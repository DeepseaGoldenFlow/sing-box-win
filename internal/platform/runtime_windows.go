//go:build windows

package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"golang.org/x/sys/windows"
)

// The runtime directory DACL is explicit and protected (no inherited entries):
// the interactive user SID and SYSTEM get full control, and nothing else is
// granted. Administrators are deliberately absent — §0.2.2 says token
// readability for administrators is decided by the OS, not granted by us, and
// §P0 requires the token directory to show only the expected SID/SYSTEM.
const dirAccess = windows.GENERIC_READ | windows.GENERIC_WRITE | windows.GENERIC_EXECUTE | windows.DELETE
const fileAccess = windows.GENERIC_READ | windows.GENERIC_WRITE | windows.DELETE

func currentUserSID() (*windows.SID, error) {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	return user.User.Sid, nil
}

func systemSID() (*windows.SID, error) {
	return windows.CreateWellKnownSid(windows.WinLocalSystemSid)
}

// setExplicitDACL replaces the object's DACL with exactly userSID and SYSTEM.
// The DACL is marked protected so the parent's inheritable ACEs cannot re-add
// Users/Everyone access to a freshly created file.
func setExplicitDACL(path string, isDir bool, access windows.ACCESS_MASK) error {
	userSID, err := currentUserSID()
	if err != nil {
		return err
	}
	sysSID, err := systemSID()
	if err != nil {
		return err
	}
	inheritance := uint32(windows.NO_INHERITANCE)
	if isDir {
		inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	entries := []windows.EXPLICIT_ACCESS{
		{
			AccessPermissions: access,
			AccessMode:        windows.SET_ACCESS,
			Inheritance:       inheritance,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(userSID),
			},
		},
		{
			AccessPermissions: access,
			AccessMode:        windows.SET_ACCESS,
			Inheritance:       inheritance,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(sysSID),
			},
		},
	}
	// The trustees point at SIDs held by Go values; pin them until
	// SetNamedSecurityInfo has copied the descriptor.
	var pinner runtime.Pinner
	pinner.Pin(userSID)
	pinner.Pin(sysSID)
	defer pinner.Unpin()

	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return fmt.Errorf("build runtime ACL: %w", err)
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil,
	)
}

// secureDir creates (if needed) and hardens the run directory.
func secureDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	if err := setExplicitDACL(path, true, dirAccess); err != nil {
		return fmt.Errorf("secure runtime directory %s: %w", filepath.Base(path), err)
	}
	return nil
}

// secureFile hardens a file created inside a protected run directory. The
// directory ACEs are inheritable, but applying an explicit DACL to the secret
// itself keeps it safe even if the directory ACL is later loosened by hand.
func secureFile(path string) error {
	if err := setExplicitDACL(path, false, fileAccess); err != nil {
		return fmt.Errorf("secure runtime file %s: %w", filepath.Base(path), err)
	}
	return nil
}

// hasReparsePoint rejects junctions/symlinks so a local attacker cannot
// redirect token reads or writes to a directory they control (§0.2.3).
func hasReparsePoint(path string) (bool, error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	attrs, err := windows.GetFileAttributes(ptr)
	if err != nil {
		return false, err
	}
	return attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0, nil
}

// verifyFileOwner requires the token file to be owned by the current user.
func verifyFileOwner(path string) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	if owner == nil {
		return fmt.Errorf("engine token has no owner: %s", filepath.Base(path))
	}
	current, err := currentUserSID()
	if err != nil {
		return err
	}
	if !owner.Equals(current) {
		return fmt.Errorf("engine token is owned by %s, not the current user", owner)
	}
	return nil
}

// ProcessStartTime returns the creation time of pid as a stable identity token.
// Clients compare it with runtime.json so a recycled PID cannot be mistaken for
// the engine that wrote the record (§0.2.3).
//
// The layout is fixed at seven fractional digits (100 ns, Windows FILETIME
// resolution) so .NET's `DateTime.ToUniversalTime().ToString("O")` — used by
// EngineClient.cs and the PowerShell scripts — matches byte-for-byte.
// RFC3339Nano would strip trailing zeros and break that comparison.
func ProcessStartTime(pid int) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	var creation, exit, kernel, user windows.Filetime
	if err = windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return "", err
	}
	return time.Unix(0, creation.Nanoseconds()).UTC().Format("2006-01-02T15:04:05.0000000Z"), nil
}

// SessionID returns the Windows terminal session of pid.
func SessionID(pid int) (uint32, error) {
	var session uint32
	if err := windows.ProcessIdToSessionId(uint32(pid), &session); err != nil {
		return 0, err
	}
	return session, nil
}

// UserSID returns the owning user SID of pid's token.
func UserSID(pid int) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	var token windows.Token
	if err = windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token); err != nil {
		return "", err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}

// CurrentSessionID is the terminal session of this process.
func CurrentSessionID() (uint32, error) { return SessionID(os.Getpid()) }

// CurrentUserSID is this process user's SID in string form.
func CurrentUserSID() (string, error) {
	sid, err := currentUserSID()
	if err != nil {
		return "", err
	}
	return sid.String(), nil
}

// TokenFilePattern is the icacls-visible ACE shape documented for operators.
// It is exported only so tests and scripts agree on the expectation string.
const TokenFilePattern = "user SID:(R,W,D) SYSTEM:(R,W,D)"
