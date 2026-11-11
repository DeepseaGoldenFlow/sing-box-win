package main

// Startup timing marks.
//
// Basis: the ticket asks for one structured `startup-timing` record per startup
// stage instead of estimating. Upstream sing-box reports only its own elapsed
// time, once: "sing-box started (X.XXs)" (box.go:507). It does not report
// process start, config parsing, rule-set readiness or API listen time, so SFW
// records those stages itself. The core-started mark is taken at the instant
// box.Start has returned, i.e. after every rule-set is resolved
// (route/router.go StartStateStart -> task.Group with FastFail) and around the
// same instant upstream logs that line. Remote rule-sets are loaded from the
// upstream cache file rather than fetched when a cache entry exists
// (route/rule/rule_set_remote.go StartContext), which is what
// rulesetcache.go enables.
//
// The records go to engine.log but are excluded from the rotation budget
// (requirement A/B), and each node is written at most once per process.

import (
	"encoding/json"
	"sync"
	"time"

	blog "github.com/sagernet/sing-box/log"
)

// processStart is initialised before main(), giving the earliest Go timestamp
// for t0. Everything else is reported as milliseconds since this instant.
var processStart = time.Now()

type startupMark struct {
	Node string `json:"node"`
	Ms   int64  `json:"ms"`
	At   string `json:"at"`
}

type startupRecorder struct {
	mu          sync.Mutex
	log         *engineLogFile
	app         *App
	marks       []startupMark
	done        map[string]bool
	coreStarted time.Time
}

func newStartupRecorder(log *engineLogFile) *startupRecorder {
	return &startupRecorder{log: log, done: make(map[string]bool)}
}

// Mark records a stage once, at now.
func (s *startupRecorder) Mark(node string, fields map[string]any) {
	s.MarkAt(node, time.Now(), fields)
}

// MarkAt records a stage once at a caller-supplied instant. UI first-frame
// timing arrives from the native process, so it is reported against the engine
// clock as offset = at - processStart.
func (s *startupRecorder) MarkAt(node string, at time.Time, fields map[string]any) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.done[node] {
		s.mu.Unlock()
		return
	}
	s.done[node] = true
	ms := at.Sub(processStart).Milliseconds()
	record := map[string]any{
		"kind": "startup-timing",
		"node": node,
		"ms":   ms,
		"at":   at.UTC().Format(time.RFC3339Nano),
	}
	for k, v := range fields {
		record[k] = v
	}
	s.marks = append(s.marks, startupMark{Node: node, Ms: ms, At: at.UTC().Format(time.RFC3339Nano)})
	if node == nodeCoreStarted {
		s.coreStarted = at
	}
	line, err := json.Marshal(record)
	log, app := s.log, s.app
	s.mu.Unlock()
	if err != nil {
		return
	}
	if log != nil {
		// Not counted: a startup record must never trigger rotation by itself.
		log.Write(append(line, '\n'), false)
	}
	if app != nil {
		app.appendStartupLog(string(line))
	}
}

// CoreStarted reports the instant the core reported `sing-box started`, if it
// has happened in this process.
func (s *startupRecorder) CoreStarted() (time.Time, bool) {
	if s == nil {
		return time.Time{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.coreStarted.IsZero() {
		return time.Time{}, false
	}
	return s.coreStarted, true
}

// First returns the recorded instant for a node. A repeated report (for
// example a second ui-ready call) answers with the first frame that was
// actually recorded rather than the retry's timestamp.
func (s *startupRecorder) First(node string) (time.Time, bool) {
	if s == nil {
		return time.Time{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, mark := range s.marks {
		if mark.Node == node {
			at, err := time.Parse(time.RFC3339Nano, mark.At)
			if err != nil {
				return time.Time{}, false
			}
			return at, true
		}
	}
	return time.Time{}, false
}

// Marks returns a copy for diagnostics and tests.
func (s *startupRecorder) Marks() []startupMark {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]startupMark, len(s.marks))
	copy(out, s.marks)
	return out
}

const (
	nodeProcessStart  = "process-start"
	nodeConfigParsed  = "config-parsed"
	nodeRuleSetLocal  = "ruleset-local-ready"
	nodeRuleSetRemote = "ruleset-remote-ready"
	nodeCoreStarted   = "core-started"
	nodeAPIListening  = "api-listening"
	nodeUIFirstFrame  = "ui-first-frame"
)

// msSince renders a mark relative to base, or nil when it has not happened.
func msSince(base, at time.Time, ok bool) any {
	if !ok {
		return nil
	}
	return at.Sub(base).Milliseconds()
}

// startupCoreStartedAt exposes the core-started instant as an RFC3339 string.
func (a *App) startupCoreStartedAt() string {
	at, ok := a.startup.CoreStarted()
	if !ok {
		return ""
	}
	return at.UTC().Format(time.RFC3339Nano)
}

// appendStartupLog mirrors the record into the in-memory ring so the Logs page
// and /api/native/status can show timings without reading the file.
func (a *App) appendStartupLog(message string) {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	a.nativeLogs = append(a.nativeLogs, Log{Time: time.Now().Format(time.RFC3339Nano), Level: blog.FormatLevel(blog.LevelInfo), Message: message})
	if len(a.nativeLogs) > 3000 {
		a.nativeLogs = append([]Log(nil), a.nativeLogs[len(a.nativeLogs)-3000:]...)
	}
}
