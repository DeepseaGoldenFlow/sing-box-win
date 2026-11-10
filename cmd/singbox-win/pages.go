package main

// Native page endpoints are isolated from the existing state/traffic/profile contracts.
import (
	"context"
	"encoding/json"
	"fmt"
	blog "github.com/sagernet/sing-box/log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type NativeOptions struct {
	LogLevel      string `json:"log_level"`
	MemoryLimitMB int64  `json:"memory_limit_mb"`
}

func (a *App) nativeOptions() NativeOptions {
	n := NativeOptions{LogLevel: "info"}
	b, _ := os.ReadFile(filepath.Join(a.dir, "native-core.json"))
	_ = json.Unmarshal(b, &n)
	if n.LogLevel == "" {
		n.LogLevel = "info"
	}
	return n
}

type GroupMember struct {
	Tag   string  `json:"tag"`
	Type  string  `json:"type"`
	Delay float64 `json:"delay"`
}
type Group struct {
	Tag        string        `json:"tag"`
	Type       string        `json:"type"`
	Selected   string        `json:"selected"`
	Selectable bool          `json:"selectable"`
	Delay      float64       `json:"delay"`
	Members    []GroupMember `json:"members"`
}

func (a *App) groups() ([]Group, error) {
	profile, e := a.profileRouting()
	if e != nil {
		return nil, e
	}
	value, e := a.clashCall("GET", "/proxies")
	if e != nil {
		return nil, e
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid Clash proxies response")
	}
	proxies, ok := root["proxies"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing Clash proxies")
	}
	delay := func(p map[string]any) float64 {
		h, _ := p["history"].([]any)
		if len(h) > 0 {
			if last, ok := h[len(h)-1].(map[string]any); ok {
				d, _ := last["delay"].(float64)
				return d
			}
		}
		return 0
	}
	out := make([]Group, 0)
	for _, tag := range configuredGroupOrder(profile) {
		p, ok := proxies[tag].(map[string]any)
		if !ok {
			continue
		}
		typ, _ := p["type"].(string)
		// Clash only exposes groups with members through `all`: Selector, URLTest, Fallback,
		// LoadBalance, Relay. Only Selector groups accept a manual pick, the rest are automatic.
		all, _ := p["all"].([]any)
		if len(all) == 0 {
			continue
		}
		selected, _ := p["now"].(string)
		g := Group{Tag: tag, Type: typ, Selected: selected, Selectable: strings.EqualFold(typ, "Selector"), Members: []GroupMember{}}
		for _, item := range all {
			name, ok := item.(string)
			if !ok {
				continue
			}
			m, _ := proxies[name].(map[string]any)
			t, _ := m["type"].(string)
			d := delay(m)
			g.Members = append(g.Members, GroupMember{name, t, d})
			if name == selected {
				g.Delay = d
			}
		}
		out = append(out, g)
	}
	return out, nil
}
func (a *App) unavailable(w http.ResponseWriter, err error) {
	a.WriteMessage(blog.LevelWarn, "Native page API: "+err.Error())
	http.Error(w, err.Error(), http.StatusNotImplemented)
}
func (a *App) pageAPI(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	if path == "/api/logs" && r.Method == "GET" && r.URL.Query().Get("wait") == "1" {
		// Wait outside App.mu: a slow reader must never block start/stop or other pages.
		deadline := time.NewTimer(20 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		var after Log
		hasAfter := json.Unmarshal([]byte(r.URL.Query().Get("after")), &after) == nil
		for {
			a.logMu.Lock()
			count := len(a.nativeLogs)
			changed := count > 0 && (!hasAfter || a.nativeLogs[count-1] != after)
			a.logMu.Unlock()
			if changed {
				break
			}
			select {
			case <-r.Context().Done():
				return true
			case <-deadline.C:
				goto ready
			case <-tick.C:
			}
		}
	ready:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 3000 {
			limit = 1000
		}
		level := r.URL.Query().Get("level")
		threshold := logRank(level)
		if level == "" {
			threshold = 5
		}
		a.logMu.Lock()
		out := make([]Log, 0)
		for _, entry := range a.nativeLogs {
			if logRank(entry.Level) <= threshold {
				out = append(out, entry)
			}
		}
		a.logMu.Unlock()
		if len(out) > limit {
			out = out[len(out)-limit:]
		}
		reply(w, out)
		return true
	}
	if path != "/api/groups" && !strings.HasPrefix(path, "/api/groups/") && path != "/api/native/core" && path != "/api/native/status" && path != "/api/native/profile" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case path == "/api/groups" && r.Method == "GET":
		groups, e := a.groups()
		if e != nil {
			a.unavailable(w, e)
		} else {
			reply(w, groups)
		}
	case path == "/api/groups/urltest" && r.Method == "POST":
		groups, e := a.groups()
		if e == nil {
			for _, g := range groups {
				_, e = a.clashCall("GET", "/group/"+url.PathEscape(g.Tag)+"/delay?timeout=5000&url="+url.QueryEscape("https://www.gstatic.com/generate_204"))
				if e != nil {
					break
				}
			}
		}
		if e != nil {
			a.unavailable(w, e)
		} else {
			reply(w, map[string]bool{"ok": true})
		}
	case strings.HasSuffix(path, "/select") && r.Method == "PUT":
		tag := strings.TrimSuffix(strings.TrimPrefix(path, "/api/groups/"), "/select")
		var b struct {
			Outbound string `json:"outbound"`
		}
		if e := decode(r, &b); e != nil || b.Outbound == "" {
			http.Error(w, "outbound required", 400)
			break
		}
		groups, e := a.groups()
		if e != nil {
			a.unavailable(w, e)
			break
		}
		var target *Group
		for i := range groups {
			if groups[i].Tag == tag {
				target = &groups[i]
				break
			}
		}
		if target == nil {
			http.Error(w, "group not found", 404)
			break
		}
		if !target.Selectable {
			http.Error(w, fmt.Sprintf("该组是自动策略组（%s），不支持手动选择节点；只有 Selector 组可以选。", target.Type), http.StatusConflict)
			break
		}
		found := false
		for _, m := range target.Members {
			if m.Tag == b.Outbound {
				found = true
				break
			}
		}
		if !found {
			http.Error(w, "unknown group member", 400)
			break
		}
		if e = a.selectNode(tag, b.Outbound); e != nil {
			a.unavailable(w, e)
		} else {
			reply(w, map[string]bool{"ok": true})
		}
	case path == "/api/native/status" && r.Method == "GET":
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		exe, _ := os.Executable()
		inCount, outCount := 0, 0
		if a.core != nil {
			if result, e := a.clashCall("GET", "/connections"); e == nil {
				if root, ok := result.(map[string]any); ok {
					if connections, ok := root["connections"].([]any); ok {
						inCount = len(connections)
						for _, c := range connections {
							if item, ok := c.(map[string]any); ok {
								if chain, ok := item["chains"].([]any); ok {
									outCount += len(chain)
								}
							}
						}
					}
				}
			}
		}
		reply(w, map[string]any{"memory": m.Alloc, "goroutines": runtime.NumGoroutine(), "core_path": exe, "connections_in": inCount, "connections_out": outCount, "tun_available": false})
	case path == "/api/native/core" && r.Method == "GET":
		reply(w, a.nativeOptions())
	case path == "/api/native/core" && r.Method == "PUT":
		n := a.nativeOptions()
		if e := decode(r, &n); e != nil {
			http.Error(w, e.Error(), 400)
			break
		}
		if !strings.Contains("|error|warn|info|debug|trace|", "|"+n.LogLevel+"|") || n.MemoryLimitMB < 0 {
			http.Error(w, "invalid core options", 400)
			break
		}
		b, _ := json.MarshalIndent(n, "", "  ")
		if e := os.WriteFile(filepath.Join(a.dir, "native-core.json"), b, 0600); e != nil {
			http.Error(w, e.Error(), 500)
			break
		}
		if a.core != nil {
			if e := a.stop(); e != nil {
				http.Error(w, e.Error(), 500)
				break
			}
			if e := a.start(); e != nil {
				http.Error(w, e.Error(), 500)
				break
			}
		}
		reply(w, n)
	case path == "/api/native/profile" && r.Method == "PUT":
		var b struct {
			ID string `json:"id"`
		}
		if e := decode(r, &b); e != nil {
			http.Error(w, e.Error(), 400)
			break
		}
		idx := -1
		for i, p := range a.settings.Profiles {
			if p.ID == b.ID {
				idx = i
			}
		}
		if idx < 0 {
			http.Error(w, "profile not found", 404)
			break
		}
		running := a.core != nil
		proxy := a.proxy
		if running {
			if e := a.stop(); e != nil {
				http.Error(w, e.Error(), 500)
				break
			}
		}
		for i := range a.settings.Profiles {
			a.settings.Profiles[i].Enabled = i == idx
		}
		e := a.save()
		if e == nil && running {
			e = a.start()
			if e == nil && proxy {
				e = a.setProxy(true)
			}
		}
		if e != nil {
			http.Error(w, e.Error(), 500)
		} else {
			reply(w, map[string]bool{"ok": true})
		}
	default:
		http.Error(w, "method not allowed", 405)
	}
	return true
}

func logRank(level string) int {
	switch strings.ToLower(level) {
	case "panic", "fatal":
		return 0
	case "error":
		return 1
	case "warn":
		return 2
	case "info":
		return 3
	case "debug":
		return 4
	case "trace":
		return 5
	}
	return 3
}

// Clash provides newline-delimited JSON over plain HTTP; the native client long-polls a bounded ring.
func (a *App) startLogStream() {
	ctx, cancel := context.WithCancel(context.Background())
	a.logStreamCancel = cancel
	endpoint, secret := a.clash, a.secret
	a.logMu.Lock()
	a.nativeLogs = nil
	a.logMu.Unlock()
	go func() {
		for ctx.Err() == nil {
			req, e := http.NewRequestWithContext(ctx, "GET", endpoint+"/logs?level=trace", nil)
			if e != nil {
				return
			}
			req.Header.Set("Authorization", "Bearer "+secret)
			transport := &http.Transport{Proxy: nil, ResponseHeaderTimeout: 5 * time.Second}
			client := &http.Client{Transport: transport}
			resp, e := client.Do(req)
			if e == nil && resp.StatusCode == 200 {
				decoder := json.NewDecoder(resp.Body)
				for {
					var entry struct {
						Type    string `json:"type"`
						Payload string `json:"payload"`
					}
					if e = decoder.Decode(&entry); e != nil {
						break
					}
					a.logMu.Lock()
					a.nativeLogs = append(a.nativeLogs, Log{Time: time.Now().Format(time.RFC3339Nano), Level: entry.Type, Message: entry.Payload})
					if len(a.nativeLogs) > 3000 {
						a.nativeLogs = append([]Log(nil), a.nativeLogs[len(a.nativeLogs)-3000:]...)
					}
					a.logMu.Unlock()
				}
			}
			if resp != nil {
				resp.Body.Close()
			}
			transport.CloseIdleConnections()
			if ctx.Err() != nil {
				return
			}
			a.WriteMessage(blog.LevelWarn, "Clash log stream unavailable; retrying")
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}()
}
