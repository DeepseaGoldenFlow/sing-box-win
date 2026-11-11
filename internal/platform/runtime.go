package platform

// Runtime directory and engine-token ownership (docs/proposal-tun-windows.md
// §0.2). The public lifecycle record (runtime.json) and the secret
// (engine.token) live in an ACL-protected per-user run directory, never in the
// settings directory that the UI also reads and writes.

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RuntimeProtocolVersion is bumped whenever runtime.json or the activation
// contract changes incompatibly. Clients must refuse unknown versions instead
// of guessing.
const RuntimeProtocolVersion = 1

// TokenBytes is the CSPRNG output size before base64url encoding. §0.2 requires
// at least 32 bytes; the encoded token is 43 characters.
const TokenBytes = 32

// RuntimeInfo is the public half of a running engine's lifecycle record. It is
// deliberately separate from engine.token: runtime.json may be read by any
// client running as the same user, the token may not.
type RuntimeInfo struct {
	URL              string `json:"url"`
	PID              int    `json:"pid"`
	ProcessStartTime string `json:"process_start_time"`
	InstanceID       string `json:"instance_id"`
	UserSID          string `json:"user_sid"`
	SessionID        uint32 `json:"session_id"`
	ProtocolVersion  int    `json:"protocol_version"`
}

// GenerateToken returns a fresh engine token from the operating system CSPRNG.
// It is never derived from a PID, user name, timestamp, Clash secret or install
// id (§0.2.1).
func GenerateToken() (string, error) {
	var b [TokenBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// NewInstanceID returns a random 128-bit instance identifier. It names the run
// directory and binds a client to one engine process, so a recycled PID cannot
// be mistaken for the previous engine (§0.2.3).
func NewInstanceID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func validInstanceID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// validToken accepts only the base64url alphabet without padding, which is what
// GenerateToken produces. Anything else (a newline, a quoted string, a Bearer
// prefix pasted into the file) is rejected before it reaches an Authorization
// header.
func validToken(token string) bool {
	if len(token) < 32 || len(token) > 128 {
		return false
	}
	for _, c := range token {
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '-' || c == '_':
		default:
			return false
		}
	}
	return true
}

// RuntimeRoot is the per-user parent of all run directories:
// %LOCALAPPDATA%\SFW\run on Windows.
func RuntimeRoot() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		var err error
		base, err = os.UserCacheDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(base, "SFW", "run"), nil
}

// rejectReparse refuses a path that is a junction/symlink. A missing path is
// fine because it is about to be created; any other stat failure is propagated
// instead of being treated as "not a link".
//
// This must run before hardening: SetNamedSecurityInfo follows a junction, so
// checking first keeps us from changing the ACL of a directory an attacker
// chose.
func rejectReparse(path, what string) error {
	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	reparse, err := hasReparsePoint(path)
	if err != nil {
		return err
	}
	if reparse {
		return fmt.Errorf("%s is a reparse point: %s", what, path)
	}
	return nil
}

// CreateRunDir creates the protected directory that holds runtime.json and
// engine.token for one engine process. The directory DACL is explicit and
// non-inherited: only the interactive user SID and SYSTEM may read or write.
func CreateRunDir(instanceID string) (string, error) {
	if !validInstanceID(instanceID) {
		return "", errors.New("invalid instance id")
	}
	root, err := RuntimeRoot()
	if err != nil {
		return "", err
	}
	if err = rejectReparse(root, "runtime root"); err != nil {
		return "", err
	}
	if err = secureDir(root); err != nil {
		return "", err
	}
	dir := filepath.Join(root, instanceID)
	if err = rejectReparse(dir, "runtime directory"); err != nil {
		return "", err
	}
	if err = secureDir(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// PruneRunDirs removes run directories left behind by crashed engines. It must
// only be called while this user's single-instance lock is held, so no other
// engine of the same user can own one of these directories.
func PruneRunDirs(root, keep string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || name == keep || !validInstanceID(name) {
			continue
		}
		// Never recurse into a redirected directory: a junction left in the run
		// root must not cause deletion outside it. Removing the link itself is
		// not needed for correctness, so skip it entirely.
		if reparse, err := hasReparsePoint(filepath.Join(root, name)); err == nil && reparse {
			continue
		}
		_ = os.RemoveAll(filepath.Join(root, name))
	}
	return nil
}

// WriteToken writes the engine secret with the protected file DACL. The write
// goes through a protected temporary file and an atomic rename so a reader can
// never observe a partially written token.
func WriteToken(dir, token string) error {
	if !validToken(token) {
		return errors.New("engine token is not valid base64url")
	}
	return writeAtomic(filepath.Join(dir, "engine.token"), []byte(token), "engine.token-*.tmp")
}

// writeAtomic creates a fresh temp file (random name, O_EXCL so a pre-created
// link cannot be reused), hardens its DACL while it is still open, then renames
// over the destination. The destination itself is never opened for writing.
func writeAtomic(final string, data []byte, pattern string) error {
	f, err := os.CreateTemp(filepath.Dir(final), pattern)
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op after a successful rename
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = secureFile(tmp); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// ReadToken reads and validates engine.token. A missing, linked, reparse-point,
// foreign-owned or malformed token is an error: the client must not fall back
// to an unauthenticated request (§0.2.3).
func ReadToken(dir string) (string, error) {
	path := filepath.Join(dir, "engine.token")
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("engine token is not a regular file: %s", path)
	}
	if info.Size() > 512 {
		return "", errors.New("engine token file is too large")
	}
	if reparse, err := hasReparsePoint(path); err != nil {
		return "", err
	} else if reparse {
		return "", errors.New("engine token is a reparse point")
	}
	if err = verifyFileOwner(path); err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(b))
	if !validToken(token) {
		return "", errors.New("engine token file is malformed")
	}
	return token, nil
}

// WriteRuntime writes runtime.json atomically inside the protected run
// directory. It never contains the token.
func WriteRuntime(dir string, info RuntimeInfo) error {
	if info.ProtocolVersion == 0 {
		info.ProtocolVersion = RuntimeProtocolVersion
	}
	b, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, "runtime.json"), b, "runtime-*.tmp")
}

// ReadRuntime loads runtime.json from a run directory. It does not read the
// token; callers that need to authenticate must call ReadToken separately.
// A linked or redirected record is refused rather than followed (§0.2.3).
func ReadRuntime(dir string) (RuntimeInfo, error) {
	var info RuntimeInfo
	path := filepath.Join(dir, "runtime.json")
	if reparse, err := hasReparsePoint(path); err != nil {
		return info, err
	} else if reparse {
		return info, errors.New("runtime record is a reparse point")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return info, err
	}
	if err = json.Unmarshal(b, &info); err != nil {
		return info, err
	}
	if info.ProtocolVersion != RuntimeProtocolVersion {
		return info, fmt.Errorf("unsupported runtime protocol version %d", info.ProtocolVersion)
	}
	if info.URL == "" || info.PID <= 0 || !validInstanceID(info.InstanceID) {
		return info, errors.New("runtime record is incomplete")
	}
	return info, nil
}

// RuntimeDirs lists run directories that contain a runtime.json, newest first.
func RuntimeDirs(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		dir     string
		modTime time.Time
	}
	var dirs []candidate
	for _, entry := range entries {
		if !entry.IsDir() || !validInstanceID(entry.Name()) {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		if _, err := os.Stat(filepath.Join(dir, "runtime.json")); err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		dirs = append(dirs, candidate{dir: dir, modTime: info.ModTime()})
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].modTime.After(dirs[j].modTime) })
	out := make([]string, len(dirs))
	for i, d := range dirs {
		out[i] = d.dir
	}
	return out, nil
}
