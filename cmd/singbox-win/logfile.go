package main

// Engine log file: level gating and size-based rotation.
//
// Basis (upstream sing-box v1.14.4, vendored module source):
//   - option.LogOptions.Level / log.ParseLevel (option/options.go:69, log/level.go)
//     select the level the core itself keeps.
//   - log/observable.go output() hands *every* level to PlatformWriter, bypassing
//     the level gate it applies to its own writer (observableLogger.Log only
//     short-circuits when no platform writer and no observable exist). SFW is
//     that platform writer, so the file gate has to be re-applied here.
//   - Upstream has no log rotation. SFM keeps a bounded in-memory ring
//     (docs/sfm-feature-spec.md: "日志环形缓冲上限 max_log_lines 默认 300"), so a
//     bounded on-disk file is the Windows equivalent rather than a new feature.
//
// Rotation keeps engine.log plus two backups, so the on-disk total is at most
// 3 x engineLogMaxBytes. The `startup-timing` records are written once per
// process start and deliberately not counted towards the rotation budget.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	blog "github.com/sagernet/sing-box/log"
)

const (
	engineLogName     = "engine.log"
	engineLogMaxBytes = int64(5 << 20) // 5 MiB per file
	engineLogBackups  = 2              // engine.log.1, engine.log.2
)

type engineLogFile struct {
	mu       sync.Mutex
	dir      string
	level    blog.Level
	maxBytes int64
	file     *os.File
	size     int64
	// counted tracks bytes that count towards the rotation budget. Startup
	// timing records are written to the same file but excluded, as required.
	counted int64
}

func newEngineLogFile(dir string, level blog.Level, maxBytes int64) *engineLogFile {
	if maxBytes <= 0 {
		maxBytes = engineLogMaxBytes
	}
	return &engineLogFile{dir: dir, level: level, maxBytes: maxBytes}
}

func (f *engineLogFile) SetLevel(level blog.Level) {
	f.mu.Lock()
	f.level = level
	f.mu.Unlock()
}

func (f *engineLogFile) Level() blog.Level {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.level
}

// Write appends one already-encoded JSON line. counted selects whether the
// line counts against the rotation budget; uncounted startup-timing records are
// written to the same file but can never trigger rotation.
func (f *engineLogFile) Write(line []byte, counted bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.file == nil {
		if err := f.openLocked(); err != nil {
			return
		}
	}
	// Rotate before the write that would cross the budget, so no single file
	// exceeds maxBytes. A single line larger than the budget is still written
	// once (the file is empty at that point) instead of rotating forever.
	if counted && f.counted > 0 && f.counted+int64(len(line)) > f.maxBytes {
		if err := f.rotateLocked(); err != nil {
			return
		}
	}
	n, err := f.file.Write(line)
	if err != nil {
		// A broken handle must not silence the file forever; reopen next time.
		f.closeLocked()
		return
	}
	f.size += int64(n)
	if counted {
		f.counted += int64(n)
	}
}

func (f *engineLogFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.file == nil {
		return nil
	}
	err := f.file.Close()
	f.file = nil
	return err
}

// Stats is used by tests and diagnostics.
func (f *engineLogFile) Stats() (size, counted int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.size, f.counted
}

func (f *engineLogFile) openLocked() error {
	path := filepath.Join(f.dir, engineLogName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}
	f.file = file
	f.size = info.Size()
	// Content that predates this process (for example a 12.8 MB file from an
	// older build) counts towards the budget so the first write rotates it away.
	f.counted = info.Size()
	return nil
}

func (f *engineLogFile) closeLocked() {
	if f.file != nil {
		_ = f.file.Close()
		f.file = nil
	}
}

// rotateLocked shifts engine.log -> .1 -> .2 and drops anything older.
func (f *engineLogFile) rotateLocked() error {
	f.closeLocked()
	base := filepath.Join(f.dir, engineLogName)
	_ = os.Remove(base + ".2")
	_ = os.Rename(base+".1", base+".2")
	if err := os.Rename(base, base+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	f.size, f.counted = 0, 0
	return f.openLocked()
}

// rotateNames reports the files rotation manages, in order, for diagnostics.
func rotateNames() []string {
	return []string{
		engineLogName,
		engineLogName + ".1",
		engineLogName + ".2",
	}
}

// logFileStatus reports the size of every rotation file for diagnostics.
type logFileStat struct {
	Name    string `json:"name"`
	Bytes   int64  `json:"bytes"`
	Present bool   `json:"present"`
}

func (a *App) logFileStatus() map[string]any {
	dir, maxBytes := a.dir, engineLogMaxBytes
	if a.logFile != nil {
		dir, maxBytes = a.logFile.dir, a.logFile.maxBytes
	}
	files := make([]logFileStat, 0, engineLogBackups+1)
	var total int64
	for _, name := range rotateNames() {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			files = append(files, logFileStat{Name: name})
			continue
		}
		total += info.Size()
		files = append(files, logFileStat{Name: name, Bytes: info.Size(), Present: true})
	}
	return map[string]any{
		"dir":         dir,
		"files":       files,
		"total_bytes": total,
		"max_bytes":   maxBytes,
		"backups":     engineLogBackups,
		"level":       logLevelName(a.engineLogLevel()),
	}
}

func logLevelName(level blog.Level) string {
	return blog.FormatLevel(level)
}

// engineLogLevel reports the level gate currently applied to the file.
func (a *App) engineLogLevel() blog.Level {
	if a.logFile != nil {
		return a.logFile.Level()
	}
	return a.fileLogLevel()
}

// encodeLogLine renders the same {time,level,message} shape the HTTP API uses.
func encodeLogLine(entry Log) []byte {
	line, err := json.Marshal(entry)
	if err != nil {
		return nil
	}
	return append(line, '\n')
}
