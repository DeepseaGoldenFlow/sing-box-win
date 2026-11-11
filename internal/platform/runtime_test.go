package platform

// Cross-platform tests for the runtime/token contract. NTFS DACL behaviour is
// checked by scripts/test-windows-service-security.ps1 on Windows; these tests
// cover the parts that must not regress on any platform: token entropy/shape,
// instance naming, atomic publication, reparse rejection and version checks.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateTokenShapeAndEntropy(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 64; i++ {
		token, err := GenerateToken()
		if err != nil {
			t.Fatal(err)
		}
		if !validToken(token) {
			t.Fatalf("generated token rejected by validator: %q", token)
		}
		// 32 random bytes encode to 43 base64url characters.
		if len(token) != 43 {
			t.Fatalf("token length = %d, want 43", len(token))
		}
		if seen[token] {
			t.Fatal("token repeated across generations")
		}
		seen[token] = true
	}
}

func TestValidTokenRejectsHeadersAndPaths(t *testing.T) {
	bad := []string{
		"",
		"short",
		"Bearer " + strings.Repeat("a", 43),
		strings.Repeat("a", 42) + "=",  // padding is not used
		strings.Repeat("a", 30) + "/+", // standard base64 characters
		strings.Repeat("a", 200),       // too long
		strings.Repeat("a", 42) + "\n",
	}
	for _, value := range bad {
		if validToken(value) {
			t.Fatalf("invalid token accepted: %q", value)
		}
	}
}

func TestValidInstanceID(t *testing.T) {
	good, err := NewInstanceID()
	if err != nil || !validInstanceID(good) {
		t.Fatalf("generated instance id rejected: %q %v", good, err)
	}
	for _, value := range []string{"", "../../etc", strings.Repeat("A", 32), strings.Repeat("g", 32), strings.Repeat("a", 31)} {
		if validInstanceID(value) {
			t.Fatalf("invalid instance id accepted: %q", value)
		}
	}
}

func TestRunDirTokenAndRuntimeRoundTrip(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	instance, err := NewInstanceID()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := CreateRunDir(instance)
	if err != nil {
		t.Fatal(err)
	}
	token, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	if err = WriteToken(dir, token); err != nil {
		t.Fatal(err)
	}
	got, err := ReadToken(dir)
	if err != nil || got != token {
		t.Fatalf("ReadToken = %q, %v", got, err)
	}
	// The secret must be a separate file; runtime.json must not contain it.
	info := RuntimeInfo{URL: "http://127.0.0.1:12345", PID: os.Getpid(), InstanceID: instance}
	if err = WriteRuntime(dir, info); err != nil {
		t.Fatal(err)
	}
	read, err := ReadRuntime(dir)
	if err != nil {
		t.Fatal(err)
	}
	if read.URL != info.URL || read.InstanceID != instance || read.ProtocolVersion != RuntimeProtocolVersion {
		t.Fatalf("unexpected runtime record: %+v", read)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "runtime.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), token) {
		t.Fatal("runtime.json contains the engine token")
	}
	// No temporary write artifact may remain after the atomic rename.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "engine.token" && entry.Name() != "runtime.json" {
			t.Fatalf("stray file after atomic write: %s", entry.Name())
		}
	}
}

func TestWriteTokenRejectsMalformedValue(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	instance, err := NewInstanceID()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := CreateRunDir(instance)
	if err != nil {
		t.Fatal(err)
	}
	if err = WriteToken(dir, "Bearer not-a-token"); err == nil {
		t.Fatal("malformed token was written")
	}
	if _, err = os.Stat(filepath.Join(dir, "engine.token")); !os.IsNotExist(err) {
		t.Fatal("token file created for malformed value")
	}
}

func TestReadRuntimeRejectsWrongVersionAndIncomplete(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	instance := strings.Repeat("c", 32)
	dir, err := CreateRunDir(instance)
	if err != nil {
		t.Fatal(err)
	}
	cases := []string{
		`{"url":"http://127.0.0.1:1","pid":1,"instance_id":"` + instance + `","protocol_version":2}`,
		`{"url":"","pid":1,"instance_id":"` + instance + `","protocol_version":1}`,
		`{"url":"http://127.0.0.1:1","pid":0,"instance_id":"` + instance + `","protocol_version":1}`,
		`{"url":"http://127.0.0.1:1","pid":1,"instance_id":"nope","protocol_version":1}`,
		`not json`,
	}
	for _, raw := range cases {
		if err = os.WriteFile(filepath.Join(dir, "runtime.json"), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = ReadRuntime(dir); err == nil {
			t.Fatalf("invalid runtime record accepted: %s", raw)
		}
	}
}

// runtime.json must name the same user/session as the engine; the writer records
// both so the security script can compare them with the current token.
func TestRuntimeInfoJSONFields(t *testing.T) {
	b, err := json.Marshal(RuntimeInfo{URL: "http://127.0.0.1:1", PID: 7, ProcessStartTime: "2026-01-01T00:00:00.0000000Z", InstanceID: strings.Repeat("d", 32), UserSID: "S-1-5-21-1", SessionID: 2, ProtocolVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"url"`, `"pid"`, `"process_start_time"`, `"instance_id"`, `"user_sid"`, `"session_id"`, `"protocol_version"`} {
		if !strings.Contains(string(b), field) {
			t.Fatalf("runtime record is missing %s: %s", field, b)
		}
	}
	if strings.Contains(strings.ToLower(string(b)), "token") {
		t.Fatalf("runtime record mentions a token: %s", b)
	}
}

func TestPruneRunDirsKeepsCurrentAndForeignEntries(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	current, err := CreateRunDir(strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	stale, err := CreateRunDir(strings.Repeat("b", 32))
	if err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(root, "SFW", "run", "not-an-instance")
	if err = os.MkdirAll(foreign, 0700); err != nil {
		t.Fatal(err)
	}
	runRoot := filepath.Join(root, "SFW", "run")
	if err = PruneRunDirs(runRoot, strings.Repeat("a", 32)); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(current); err != nil {
		t.Fatal("current run directory was pruned")
	}
	if _, err = os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale run directory survived")
	}
	if _, err = os.Stat(foreign); err != nil {
		t.Fatal("non-instance directory was pruned")
	}
}

// A redirected entry must not be recursed into: deleting through a link could
// remove files outside the run root. Skipping it is the safe behaviour.
func TestPruneRunDirsSkipsReparsePoints(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	if _, err := CreateRunDir(strings.Repeat("a", 32)); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "SFW", "run", strings.Repeat("e", 32))
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := PruneRunDirs(filepath.Join(root, "SFW", "run"), strings.Repeat("a", 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "keep.txt")); err != nil {
		t.Fatal("prune followed a redirected run directory")
	}
}
