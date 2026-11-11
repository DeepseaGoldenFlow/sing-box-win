package main

// Requirement A: engine.log keeps info by default, debug/trace only when the
// native "Log Level" setting asks for them, and rotation is bounded to
// 5 MiB x (1 current + 2 backups).

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	blog "github.com/sagernet/sing-box/log"
)

func lineOf(n int) []byte {
	return append(bytes.Repeat([]byte{'x'}, n), '\n')
}

func TestEngineLogDefaultsToInfo(t *testing.T) {
	dir := t.TempDir()
	a := &App{dir: dir}
	if got := a.fileLogLevel(); got != blog.LevelInfo {
		t.Fatalf("default file level = %v, want info", got)
	}
	a.logFile = newEngineLogFile(dir, a.fileLogLevel(), engineLogMaxBytes)
	defer a.logFile.Close()

	a.WriteMessage(blog.LevelDebug, "debug line")
	a.WriteMessage(blog.LevelTrace, "trace line")
	a.WriteMessage(blog.LevelInfo, "info line")

	b, err := os.ReadFile(filepath.Join(dir, engineLogName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "debug line") || strings.Contains(string(b), "trace line") {
		t.Fatalf("debug/trace reached engine.log at the default level:\n%s", b)
	}
	if !strings.Contains(string(b), "info line") {
		t.Fatalf("info line missing from engine.log:\n%s", b)
	}
	// The in-memory ring still receives every level so the Logs page keeps its
	// Default/Error/Warn/Info/Debug/Trace filter (SFM parity).
	if len(a.logs) != 3 {
		t.Fatalf("in-memory logs = %d, want 3", len(a.logs))
	}
}

func TestEngineLogDiagnosticSettingRaisesLevel(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "native-core.json"), []byte(`{"log_level":"debug"}`), 0600); err != nil {
		t.Fatal(err)
	}
	a := &App{dir: dir}
	if got := a.fileLogLevel(); got != blog.LevelDebug {
		t.Fatalf("diagnostic file level = %v, want debug", got)
	}
	a.logFile = newEngineLogFile(dir, a.fileLogLevel(), engineLogMaxBytes)
	defer a.logFile.Close()

	a.WriteMessage(blog.LevelDebug, "debug line")
	a.WriteMessage(blog.LevelTrace, "trace line")
	b, err := os.ReadFile(filepath.Join(dir, engineLogName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "debug line") {
		t.Fatalf("debug line missing while diagnostics enabled:\n%s", b)
	}
	if strings.Contains(string(b), "trace line") {
		t.Fatalf("trace line written while level=debug:\n%s", b)
	}

	// A PUT /api/native/core changes the gate without a restart.
	if err := os.WriteFile(filepath.Join(dir, "native-core.json"), []byte(`{"log_level":"trace"}`), 0600); err != nil {
		t.Fatal(err)
	}
	a.logFile.SetLevel(a.fileLogLevel())
	a.WriteMessage(blog.LevelTrace, "trace line")
	b, _ = os.ReadFile(filepath.Join(dir, engineLogName))
	if !strings.Contains(string(b), "trace line") {
		t.Fatalf("trace line missing after enabling trace:\n%s", b)
	}
}

func TestEngineLogRotationKeepsTwoBackups(t *testing.T) {
	dir := t.TempDir()
	f := newEngineLogFile(dir, blog.LevelInfo, 100)
	defer f.Close()

	for i := 0; i < 6; i++ {
		f.Write(lineOf(60), true)
	}
	names := []string{engineLogName, engineLogName + ".1", engineLogName + ".2"}
	for _, name := range names {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s missing after rotation: %v", name, err)
		}
		if info.Size() > 100 {
			t.Fatalf("%s = %d bytes, exceeds the 100-byte test budget", name, info.Size())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, engineLogName+".3")); !os.IsNotExist(err) {
		t.Fatalf("third backup exists; rotation must keep only %d backups", engineLogBackups)
	}

	status := (&App{dir: dir, logFile: f}).logFileStatus()
	if status["max_bytes"] != int64(100) {
		t.Fatalf("status max_bytes = %v", status["max_bytes"])
	}
	files, _ := status["files"].([]logFileStat)
	if len(files) != engineLogBackups+1 {
		t.Fatalf("status files = %d, want %d", len(files), engineLogBackups+1)
	}
}

func TestEngineLogStartupRecordsDoNotRotate(t *testing.T) {
	dir := t.TempDir()
	f := newEngineLogFile(dir, blog.LevelInfo, 10)
	defer f.Close()

	for i := 0; i < 5; i++ {
		f.Write(lineOf(100), false)
	}
	if _, err := os.Stat(filepath.Join(dir, engineLogName+".1")); !os.IsNotExist(err) {
		t.Fatal("uncounted startup records triggered rotation")
	}
	size, counted := f.Stats()
	if counted != 0 {
		t.Fatalf("counted = %d after uncounted writes, want 0", counted)
	}
	if size <= 10 {
		t.Fatalf("size = %d, uncounted records were not written", size)
	}
}

func TestEngineLogRotatesLegacyOversizedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, engineLogName), bytes.Repeat([]byte{'y'}, 500), 0600); err != nil {
		t.Fatal(err)
	}
	f := newEngineLogFile(dir, blog.LevelInfo, 100)
	defer f.Close()

	f.Write(lineOf(20), true)

	rotated, err := os.ReadFile(filepath.Join(dir, engineLogName+".1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rotated) < 500 {
		t.Fatalf("legacy file was not rotated away: .1 = %d bytes", len(rotated))
	}
	if _, counted := f.Stats(); counted > 100 {
		t.Fatalf("counted = %d after rotating the legacy file", counted)
	}
}
