package main

// Requirement B/C: one startup-timing record per node, written once, and the
// /api/native/ui-ready handshake that proves the window painted before the core.
// Basis for the node set: upstream sing-box reports only its own elapsed time
// ("sing-box started (X.XXs)", box.go) and each remote rule-set fetch
// ("updated rule-set <tag>", route/rule/rule_set_remote.go); the rest are SFW
// stages that upstream does not instrument.

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	blog "github.com/sagernet/sing-box/log"
)

func readStartupRecords(t *testing.T, dir string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, engineLogName))
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("engine.log line is not JSON: %v: %q", err, line)
		}
		if record["kind"] == "startup-timing" {
			out = append(out, record)
		}
	}
	return out
}

func TestStartupMarksAreRecordedOnce(t *testing.T) {
	dir := t.TempDir()
	f := newEngineLogFile(dir, blog.LevelInfo, engineLogMaxBytes)
	defer f.Close()
	r := newStartupRecorder(f)

	r.Mark(nodeProcessStart, map[string]any{"pid": 1})
	r.Mark(nodeConfigParsed, map[string]any{"profile": "p"})
	r.Mark(nodeConfigParsed, map[string]any{"profile": "p"}) // duplicate must be ignored
	r.Mark(nodeAPIListening, map[string]any{"url": "http://127.0.0.1:1"})

	records := readStartupRecords(t, dir)
	if len(records) != 3 {
		t.Fatalf("startup-timing records = %d, want 3: %v", len(records), records)
	}
	seen := make(map[string]int)
	for _, record := range records {
		node, _ := record["node"].(string)
		seen[node]++
		if _, ok := record["ms"]; !ok {
			t.Fatalf("record missing ms: %v", record)
		}
		if _, ok := record["at"]; !ok {
			t.Fatalf("record missing at: %v", record)
		}
	}
	for node, count := range seen {
		if count != 1 {
			t.Fatalf("node %q written %d times", node, count)
		}
	}

	// Marks are also exposed for /api/native/startup.
	marks := r.Marks()
	if len(marks) != 3 || marks[0].Node != nodeProcessStart {
		t.Fatalf("Marks() = %+v", marks)
	}
	if _, counted := f.Stats(); counted != 0 {
		t.Fatalf("startup records counted %d bytes towards rotation", counted)
	}
}

func TestStartupMarkAtUsesSuppliedInstant(t *testing.T) {
	dir := t.TempDir()
	f := newEngineLogFile(dir, blog.LevelInfo, engineLogMaxBytes)
	defer f.Close()
	r := newStartupRecorder(f)

	uiAt := processStart.Add(1500 * time.Millisecond)
	r.MarkAt(nodeUIFirstFrame, uiAt, map[string]any{"phase": "ui-first-frame"})
	if got := r.Marks()[0].Ms; got != 1500 {
		t.Fatalf("ui-first-frame ms = %d, want 1500", got)
	}
}

func TestStartupCoreStartedAndRuleSetMarks(t *testing.T) {
	dir := t.TempDir()
	f := newEngineLogFile(dir, blog.LevelInfo, engineLogMaxBytes)
	defer f.Close()
	r := newStartupRecorder(f)
	at := processStart.Add(2 * time.Second)
	ruleSetInventory{local: 1, remote: 2, cacheFile: filepath.Join(dir, "cache.db")}.markReadyAt(r, at)
	r.MarkAt(nodeCoreStarted, at, nil)

	records := readStartupRecords(t, dir)
	byNode := make(map[string]map[string]any)
	for _, record := range records {
		byNode[record["node"].(string)] = record
	}
	local := byNode[nodeRuleSetLocal]
	if local["count"] != float64(1) {
		t.Fatalf("local rule-set record = %v", local)
	}
	remote := byNode[nodeRuleSetRemote]
	if remote["count"] != float64(2) || remote["cache_present"] != false {
		t.Fatalf("remote rule-set record = %v", remote)
	}
	if _, ok := byNode[nodeCoreStarted]; !ok {
		t.Fatalf("core-started record missing: %v", records)
	}
	if started, ok := r.CoreStarted(); !ok || !started.Equal(at) {
		t.Fatalf("CoreStarted() = %v, %v", started, ok)
	}
}

func TestUIFirstFrameReportsWindowBeforeCore(t *testing.T) {
	dir := t.TempDir()
	a := &App{dir: dir, startup: newStartupRecorder(nil)}
	a.logFile = newEngineLogFile(dir, blog.LevelInfo, engineLogMaxBytes)
	defer a.logFile.Close()
	a.startup.log = a.logFile
	a.startup.app = a

	// Window paints before the core reports started.
	body := strings.NewReader(`{"elapsed_ms":800}`)
	request := httptest.NewRequest("POST", "/api/native/ui-ready", body)
	recorder := httptest.NewRecorder()
	if !a.pageAPI(recorder, request) {
		t.Fatal("ui-ready route not handled")
	}
	var reply map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if reply["window_before_core"] != true {
		t.Fatalf("window_before_core = %v, want true", reply["window_before_core"])
	}
	if reply["ui_ms"] != float64(800) {
		t.Fatalf("ui_ms = %v, want 800", reply["ui_ms"])
	}
	if reply["core_ms"] != nil {
		t.Fatalf("core_ms = %v, want null before the core starts", reply["core_ms"])
	}

	// Core starts later; a second report must not rewrite the first record.
	a.startup.MarkAt(nodeCoreStarted, processStart.Add(5*time.Second), nil)
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest("POST", "/api/native/ui-ready", strings.NewReader(`{"elapsed_ms":9000}`))
	if !a.pageAPI(recorder, request) {
		t.Fatal("ui-ready route not handled")
	}
	reply = map[string]any{}
	_ = json.Unmarshal(recorder.Body.Bytes(), &reply)
	if reply["window_before_core"] != true {
		t.Fatalf("window_before_core = %v, want true (first frame at 800 ms)", reply["window_before_core"])
	}
	if reply["core_ms"] != float64(5000) {
		t.Fatalf("core_ms = %v, want 5000", reply["core_ms"])
	}
}

func TestStateReportsStartingWhileCoreStarts(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "profiles"), 0700); err != nil {
		t.Fatal(err)
	}
	a := &App{dir: dir, settings: Settings{Port: 0, Profiles: []Profile{{ID: "p", Enabled: true}}, AutoConnect: true}}
	a.startup = newStartupRecorder(nil)
	a.logFile = newEngineLogFile(dir, blog.LevelInfo, engineLogMaxBytes)
	defer a.logFile.Close()
	a.startup.log = a.logFile
	a.startup.app = a
	// No profile file on disk: prepareOptions fails, so startAsync must return
	// immediately with the failure recorded instead of blocking the caller.
	a.mu.Lock()
	start := time.Now()
	a.startAsync()
	elapsed := time.Since(start)
	a.mu.Unlock()
	if elapsed > 2*time.Second {
		t.Fatalf("startAsync blocked for %v", elapsed)
	}
	if a.starting {
		t.Fatal("starting = true after a preparation failure")
	}
	if a.lastError == "" {
		t.Fatal("preparation failure not recorded in lastError")
	}

	request := httptest.NewRequest("GET", "/api/state", nil)
	recorder := httptest.NewRecorder()
	a.api(recorder, request)
	var state map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state["starting"] != false || state["running"] != false {
		t.Fatalf("state = %v", state)
	}
	if state["error"] == "" {
		t.Fatalf("state missing error: %v", state)
	}
}
