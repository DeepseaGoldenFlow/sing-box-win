package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
)

type tailscaleState struct {
	Enabled        bool     `json:"enabled"`
	Registered     bool     `json:"registered"`
	StateDirExists bool     `json:"state_dir_exists"`
	Hostname       string   `json:"hostname"`
	IPs            []string `json:"ips"`
	KeyExpiry      string   `json:"key_expiry"`
	LastSync       string   `json:"last_sync"`
}

func (a *App) tailscaleStatus() tailscaleState {
	s := tailscaleState{IPs: []string{}}
	for _, p := range a.settings.Profiles {
		if !p.Enabled {
			continue
		}
		b, err := os.ReadFile(a.profilePath(p.ID))
		var cfg struct {
			Endpoints []struct {
				Type string `json:"type"`
			} `json:"endpoints"`
		}
		if err == nil && json.Unmarshal(b, &cfg) == nil {
			for _, ep := range cfg.Endpoints {
				if ep.Type == "tailscale" {
					s.Enabled = true
				}
			}
		}
		break
	}
	dir := filepath.Join(a.dir, "tailscale")
	st, err := os.Stat(dir)
	s.StateDirExists = err == nil && st.IsDir()
	// Read only cached public node metadata; never expose tailscaled.state secrets.
	stateBytes, err := os.ReadFile(filepath.Join(dir, "tailscaled.state"))
	var state map[string][]byte
	if err != nil || json.Unmarshal(stateBytes, &state) != nil {
		return s
	}
	files, _ := filepath.Glob(filepath.Join(dir, "profile-data", "*", "netmap-cache", "73656c66"))
	if current := strings.TrimPrefix(string(state["_current-profile"]), "profile-"); current != "" {
		selected := files[:0]
		for _, path := range files {
			if filepath.Base(filepath.Dir(filepath.Dir(path))) == current {
				selected = append(selected, path)
			}
		}
		files = selected
	}
	var newest time.Time
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil || !info.ModTime().After(newest) {
			continue
		}
		b, err := os.ReadFile(path)
		var cached struct {
			Node *struct {
				Name      string
				Addresses []string
				KeyExpiry string
			}
		}
		if err != nil || json.Unmarshal(b, &cached) != nil || cached.Node == nil || cached.Node.Name == "" {
			continue
		}
		ips := []string{}
		for _, addr := range cached.Node.Addresses {
			if prefix, err := netip.ParsePrefix(addr); err == nil {
				ips = append(ips, prefix.Addr().String())
			} else if ip, err := netip.ParseAddr(addr); err == nil {
				ips = append(ips, ip.String())
			}
		}
		newest = info.ModTime()
		s.Registered = true
		s.Hostname = strings.TrimRight(cached.Node.Name, ".")
		s.IPs = ips
		s.KeyExpiry = cached.Node.KeyExpiry
		s.LastSync = newest.Format(time.RFC3339)
	}
	return s
}

type connectivityResult struct {
	OK     bool   `json:"ok"`
	Status int    `json:"status"`
	MS     int64  `json:"ms"`
	Error  string `json:"error"`
}

func (a *App) testTailscale() connectivityResult {
	result := connectivityResult{}
	if a.core == nil {
		result.Error = "core not running"
		return result
	}
	port := a.settings.Port
	if port == 0 {
		port = 2085
	}
	proxy, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	transport := &http.Transport{Proxy: http.ProxyURL(proxy)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 6 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	start := time.Now()
	resp, err := client.Get("http://100.68.38.67/")
	result.MS = time.Since(start).Milliseconds()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()
	result.Status = resp.StatusCode
	result.OK = resp.StatusCode == 200 || resp.StatusCode == 301 || resp.StatusCode == 302
	if !result.OK {
		result.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return result
}
func (a *App) toolsInfo() any {
	coreVersion := "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path == "github.com/sagernet/sing-box" {
				coreVersion = dep.Version
			}
		}
	}
	return map[string]any{"version": version, "core_version": coreVersion, "mode": "headless", "data_dir": a.dir, "log_path": filepath.Join(a.dir, "engine.log")}
}
