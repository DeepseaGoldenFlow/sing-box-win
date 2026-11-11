//go:build !windows

package platform

import (
	"errors"
	"os"
	"syscall"
)

// Registry-free platforms have no NTFS DACL; the engine still uses the same
// runtime layout so Linux vet/tests exercise the token and activation paths.
// Real ACL behaviour is only claimed on Windows, where scripts check it with
// icacls.

func secureDir(path string) error {
	return os.MkdirAll(path, 0700)
}

func secureFile(path string) error {
	return os.Chmod(path, 0600)
}

func hasReparsePoint(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	return info.Mode()&os.ModeSymlink != 0, nil
}

func verifyFileOwner(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		if int(stat.Uid) != os.Geteuid() {
			return errors.New("engine token is not owned by the current user")
		}
	}
	return nil
}

func ProcessStartTime(pid int) (string, error) { return "", nil }

func SessionID(pid int) (uint32, error) { return 0, nil }

func CurrentSessionID() (uint32, error) { return 0, nil }

func CurrentUserSID() (string, error) { return "", nil }

func UserSID(pid int) (string, error) { return "", nil }

const TokenFilePattern = "mode 0600, owner is current user"
