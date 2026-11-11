package main

// Engine lifecycle record, token ownership and second-instance forwarding.
// See docs/proposal-tun-windows.md §0.2. The run directory under
// %LOCALAPPDATA%\SFW\run\<instance-id> is ACL-protected by internal/platform;
// the token never appears in logs, errors, arguments or runtime.json.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JARVIS/sing-box-win/internal/platform"
)

// activationPayload is what a second engine process (or the native client)
// forwards to the running engine: either a deep link or a file opened via
// association. An empty payload only asks the running instance to accept the
// activation; it must not import anything (§0.2.5).
type activationPayload struct {
	Link string `json:"link"`
	Path string `json:"path"`
}

// activationResult is the minimal response of /api/activate. It contains no
// token or user data.
type activationResult struct {
	Activated bool `json:"activated"`
}

type pendingActivation struct {
	link string
	path string
}

// prepareRuntime mints this process's token and publishes the protected run
// directory. The old single-instance lock is held before this runs, so pruning
// cannot race another engine of the same user.
func (a *App) prepareRuntime() error {
	instanceID, err := platform.NewInstanceID()
	if err != nil {
		return err
	}
	runDir, err := platform.CreateRunDir(instanceID)
	if err != nil {
		return err
	}
	root, err := platform.RuntimeRoot()
	if err == nil {
		_ = platform.PruneRunDirs(root, instanceID)
	}
	token, err := platform.GenerateToken()
	if err != nil {
		return err
	}
	if err = platform.WriteToken(runDir, token); err != nil {
		return err
	}
	a.auth = newEngineAuth(token, instanceID)
	if a.auth == nil {
		return errors.New("engine token initialization failed")
	}
	a.runDir = runDir
	return nil
}

// publishRuntime writes runtime.json once the listener address is known. It
// must be called before serving requests so the UI can find the engine.
func (a *App) publishRuntime(base string) error {
	instanceID := ""
	if a.auth != nil {
		instanceID = a.auth.instanceID
	}
	// §0.2.3 requires the record to name the engine's start time, user and
	// session. If any of them cannot be determined, fail instead of publishing
	// an incomplete record that clients would have to guess about.
	start, err := platform.ProcessStartTime(os.Getpid())
	if err != nil {
		return err
	}
	session, err := platform.CurrentSessionID()
	if err != nil {
		return err
	}
	sid, err := platform.CurrentUserSID()
	if err != nil {
		return err
	}
	info := platform.RuntimeInfo{
		URL:              base,
		PID:              os.Getpid(),
		ProcessStartTime: start,
		InstanceID:       instanceID,
		UserSID:          sid,
		SessionID:        session,
		ProtocolVersion:  platform.RuntimeProtocolVersion,
	}
	return platform.WriteRuntime(a.runDir, info)
}

// unpublishRuntime removes this instance's public record on clean shutdown. The
// token stays until the directory is pruned by the next primary, so a client
// that read runtime.json just before exit still fails closed instead of
// connecting to another engine with the wrong token.
func (a *App) unpublishRuntime() {
	if a.runDir == "" {
		return
	}
	_ = os.Remove(filepath.Join(a.runDir, "runtime.json"))
}

// activationFromArgs extracts the deep link or profile path this process was
// invoked with. Nothing else is forwarded: command lines are not commands to
// the running engine.
func activationFromArgs(args []string) activationPayload {
	payload := activationPayload{}
	for _, arg := range args {
		switch {
		case isDeepLinkArg(arg):
			if payload.Link == "" {
				payload.Link = arg
			}
		case fileAssociationArg(arg):
			if payload.Path == "" {
				payload.Path = arg
			}
		}
	}
	return payload
}

// validRuntimeURL accepts only loopback HTTP URLs without credentials, query,
// fragment or extra path. runtime.json is public to the same user, so a
// tampered record must not be able to redirect the client elsewhere.
func validRuntimeURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("runtime URL is not a plain loopback HTTP origin")
	}
	host := u.Hostname()
	if host != "127.0.0.1" && host != "::1" {
		return nil, errors.New("runtime URL is not loopback")
	}
	return u, nil
}

// findRunningEngine scans the protected run root for a live engine of this
// user, newest first, and returns its record plus token. A PID whose start time
// no longer matches the record is treated as stale (§0.2.3).
func findRunningEngine() (platform.RuntimeInfo, string, string, error) {
	var lastErr error = errors.New("no running engine")
	root, err := platform.RuntimeRoot()
	if err != nil {
		return platform.RuntimeInfo{}, "", "", err
	}
	dirs, err := platform.RuntimeDirs(root)
	if err != nil {
		return platform.RuntimeInfo{}, "", "", err
	}
	for _, dir := range dirs {
		info, err := platform.ReadRuntime(dir)
		if err != nil {
			lastErr = err
			continue
		}
		if _, err = validRuntimeURL(info.URL); err != nil {
			lastErr = err
			continue
		}
		if !processMatches(info) {
			lastErr = errors.New("runtime record is stale")
			continue
		}
		token, err := platform.ReadToken(dir)
		if err != nil {
			lastErr = err
			continue
		}
		return info, token, dir, nil
	}
	return platform.RuntimeInfo{}, "", "", lastErr
}

// processMatches verifies the recorded PID is alive and still the same process
// that wrote the record. An empty start time (non-Windows) cannot be checked.
func processMatches(info platform.RuntimeInfo) bool {
	if info.ProcessStartTime != "" {
		start, err := platform.ProcessStartTime(info.PID)
		if err != nil || start != info.ProcessStartTime {
			return false
		}
	}
	return true
}

// activateExisting forwards a second launch to the running engine. Requests are
// authenticated with the token from the ACL-protected run directory and name
// the instance, so a forwarded link cannot land in a different engine.
func activateExisting() error {
	payload := activationFromArgs(os.Args[1:])
	if payload.Link == "" && payload.Path == "" {
		// A plain second launch only needs the running instance to come
		// forward; it must not import a deep link or profile (§0.2.5).
		return nil
	}
	var last error
	for i := 0; i < 600; i++ {
		info, token, _, err := findRunningEngine()
		if err != nil {
			last = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		body, _ := json.Marshal(payload)
		req, err := http.NewRequest("POST", info.URL+"/api/activate", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set(instanceHeader, info.InstanceID)
		c := http.Client{Timeout: 60 * time.Second}
		resp, err := c.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
			return errors.New(strings.TrimSpace(string(msg)))
		}
		last = err
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("activate existing instance: %w", last)
}
