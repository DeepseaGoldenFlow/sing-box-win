package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/JARVIS/sing-box-win/internal/config"
	"github.com/JARVIS/sing-box-win/internal/platform"
	box "github.com/sagernet/sing-box"
	blog "github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed web
var web embed.FS

const version = "0.1.0"

type Profile struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Type    string    `json:"type"`
	URL     string    `json:"url,omitempty"`
	Updated time.Time `json:"updated"`
	Enabled bool      `json:"enabled"`
}
type Settings struct {
	AutoStart   bool                  `json:"auto_start"`
	AutoConnect bool                  `json:"auto_connect"`
	UpdateHours int                   `json:"update_hours"`
	Port        int                   `json:"port"`
	AllowLAN    bool                  `json:"allow_lan"`
	Theme       string                `json:"theme"`
	Language    string                `json:"language"`
	Profiles    []Profile             `json:"profiles"`
	ProxyBackup *platform.ProxyBackup `json:"proxy_backup,omitempty"`
}
type Log struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}
type App struct {
	mu            sync.Mutex
	logMu         sync.Mutex
	dir           string
	settings      Settings
	core          *box.Box
	logs          []Log
	lastError     string
	clash         string
	secret        string
	proxy         bool
	quit          chan struct{}
	trafficTime   time.Time
	uploadTotal   float64
	downloadTotal float64
}

func (a *App) WriteMessage(level blog.Level, msg string) {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	a.logs = append(a.logs, Log{time.Now().Format(time.RFC3339), blog.FormatLevel(level), msg})
	if len(a.logs) > 5000 {
		a.logs = append([]Log(nil), a.logs[len(a.logs)-5000:]...)
	}
}
func (a *App) DisableColors() bool { return true }
func (a *App) save() error {
	b, e := json.MarshalIndent(a.settings, "", "  ")
	if e != nil {
		return e
	}
	p := filepath.Join(a.dir, "settings.json")
	if e = os.WriteFile(p+".tmp", b, 0600); e != nil {
		return e
	}
	return os.Rename(p+".tmp", p)
}
func (a *App) profilePath(id string) string { return filepath.Join(a.dir, "profiles", id+".json") }
func id() string                            { var b [16]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }
func (a *App) stop() error {
	if a.proxy || a.settings.ProxyBackup != nil {
		if e := a.setProxy(false); e != nil {
			return e
		}
	}
	if a.core != nil {
		e := a.core.Close()
		a.core = nil
		return e
	}
	return nil
}
func (a *App) start() error {
	if a.core != nil {
		return nil
	}
	var p *Profile
	for i := range a.settings.Profiles {
		if a.settings.Profiles[i].Enabled {
			p = &a.settings.Profiles[i]
			break
		}
	}
	if p == nil {
		return errors.New("请先启用一个配置")
	}
	b, e := os.ReadFile(a.profilePath(p.ID))
	if e != nil {
		return e
	}
	opt, e := config.Parse(b)
	if e != nil {
		return e
	}
	// TUN is deliberately deferred; preserve endpoints, outbounds and route options.
	ins := make([]option.Inbound, 0, len(opt.Inbounds))
	for _, in := range opt.Inbounds {
		if in.Type != "tun" {
			if wrapper, ok := in.Options.(option.ListenOptionsWrapper); ok && !a.settings.AllowLAN {
				options := wrapper.TakeListenOptions()
				addr := badoption.Addr(netip.MustParseAddr("127.0.0.1"))
				options.Listen = &addr
				wrapper.ReplaceListenOptions(options)
			}
			if mixed, ok := in.Options.(*option.HTTPMixedInboundOptions); ok {
				mixed.SetSystemProxy = false
			}
			ins = append(ins, in)
		}
	}
	opt.Inbounds = ins
	// Dedicated mixed inbound follows client settings without rewriting subscription inbounds.
	raw := fmt.Sprintf(`{"type":"mixed","tag":"singbox-win-local","listen":"%s","listen_port":%d}`, map[bool]string{true: "0.0.0.0", false: "127.0.0.1"}[a.settings.AllowLAN], a.settings.Port)
	var in option.Inbound
	if e = in.UnmarshalJSONContext(config.Context(), []byte(raw)); e != nil {
		return e
	}
	opt.Inbounds = append(opt.Inbounds, in)
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return e
	}
	a.clash = "http://" + l.Addr().String()
	l.Close()
	a.secret = id()
	if opt.Experimental == nil {
		opt.Experimental = &option.ExperimentalOptions{}
	}
	opt.Experimental.ClashAPI = &option.ClashAPIOptions{ExternalController: strings.TrimPrefix(a.clash, "http://"), Secret: a.secret}
	opt.Log = &option.LogOptions{Level: "info", Timestamp: true, DisableColor: true}
	instance, e := box.New(box.Options{Options: opt, Context: config.Context(), PlatformLogWriter: a})
	if e != nil {
		return e
	}
	if e = instance.Start(); e != nil {
		instance.Close()
		return e
	}
	a.core = instance
	a.trafficTime = time.Time{}
	a.lastError = ""
	return nil
}
func (a *App) setProxy(on bool) error {
	if on == a.proxy && (on || a.settings.ProxyBackup == nil) {
		return nil
	}
	if on {
		if a.core == nil {
			return errors.New("请先启动内核")
		}
		b, e := platform.ReadProxy()
		if e != nil {
			return e
		}
		a.settings.ProxyBackup = &b
		if e = a.save(); e != nil {
			return e
		}
		if e = platform.WriteProxy(platform.ProxyBackup{Enabled: 1, Server: fmt.Sprintf("127.0.0.1:%d", a.settings.Port), HadEnabled: true, HadServer: true}); e != nil {
			return e
		}
		a.proxy = true
		return nil
	}
	if a.settings.ProxyBackup != nil {
		if e := platform.WriteProxy(*a.settings.ProxyBackup); e != nil {
			return e
		}
	}
	a.proxy = false
	a.settings.ProxyBackup = nil
	return a.save()
}
func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
func decode(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 16<<20)).Decode(v)
}
func fetch(u string) ([]byte, error) {
	p, e := url.Parse(u)
	if e != nil || p.Host == "" || (p.Scheme != "https" && p.Scheme != "http") {
		return nil, errors.New("订阅 URL 必须是 http/https")
	}
	c := http.Client{Timeout: 45 * time.Second}
	r, e := c.Get(u)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("订阅 HTTP %d", r.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(r.Body, 16<<20+1))
	if len(b) > 16<<20 {
		return nil, errors.New("配置超过 16 MiB")
	}
	return b, e
}
func (a *App) refresh(p *Profile) error {
	if p.URL == "" {
		return errors.New("本地配置没有订阅 URL")
	}
	b, e := fetch(p.URL)
	if e != nil {
		return e
	}
	if _, e = config.Parse(b); e != nil {
		return e
	}
	if e = os.WriteFile(a.profilePath(p.ID), b, 0600); e != nil {
		return e
	}
	p.Updated = time.Now()
	return a.save()
}
func (a *App) clashCall(method, path string) (any, error) {
	if a.core == nil {
		return nil, errors.New("内核尚未运行")
	}
	req, e := http.NewRequest(method, a.clash+path, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+a.secret)
	c := http.Client{Timeout: 12 * time.Second}
	r, e := c.Do(req)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	if r.StatusCode >= 300 {
		return nil, fmt.Errorf("Clash API HTTP %d", r.StatusCode)
	}
	var v any
	if r.StatusCode != 204 {
		e = json.NewDecoder(r.Body).Decode(&v)
	}
	return v, e
}
func (a *App) api(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var result any = map[string]bool{"ok": true}
	var err error
	switch {
	case r.URL.Path == "/api/state" && r.Method == "GET":
		name := ""
		for _, p := range a.settings.Profiles {
			if p.Enabled {
				name = p.Name
			}
		}
		result = map[string]any{"running": a.core != nil, "profile": name, "system_proxy": a.proxy, "tun": false, "version": version, "error": a.lastError}
	case strings.HasPrefix(r.URL.Path, "/api/core/") && r.Method == "POST":
		switch strings.TrimPrefix(r.URL.Path, "/api/core/") {
		case "start":
			err = a.start()
		case "stop":
			err = a.stop()
		case "restart":
			if err = a.stop(); err == nil {
				err = a.start()
			}
		default:
			err = errors.New("未知操作")
		}
	case r.URL.Path == "/api/proxy" && r.Method == "POST":
		var b struct {
			Enabled bool `json:"enabled"`
		}
		if err = decode(r, &b); err == nil {
			err = a.setProxy(b.Enabled)
		}
	case r.URL.Path == "/api/profiles" && r.Method == "GET":
		result = a.settings.Profiles
	case (r.URL.Path == "/api/profiles/import" || r.URL.Path == "/api/profiles") && r.Method == "POST":
		var b struct{ Name, URL, Content, Path string }
		if err = decode(r, &b); err != nil {
			break
		}
		var data []byte
		typ := "local"
		switch {
		case b.URL != "":
			data, err = fetch(b.URL)
			typ = "remote"
		case b.Path != "":
			data, err = os.ReadFile(b.Path)
		default:
			data = []byte(b.Content)
		}
		if err != nil {
			break
		}
		if _, err = config.Parse(data); err != nil {
			break
		}
		if strings.TrimSpace(b.Name) == "" {
			b.Name = "新配置"
		}
		p := Profile{ID: id(), Name: b.Name, URL: b.URL, Type: typ, Updated: time.Now(), Enabled: len(a.settings.Profiles) == 0}
		if err = os.WriteFile(a.profilePath(p.ID), data, 0600); err != nil {
			break
		}
		a.settings.Profiles = append(a.settings.Profiles, p)
		err = a.save()
		result = p
	case strings.HasPrefix(r.URL.Path, "/api/profiles/"):
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/profiles/"), "/")
		idx := -1
		for i, p := range a.settings.Profiles {
			if p.ID == parts[0] {
				idx = i
				break
			}
		}
		if idx < 0 {
			http.Error(w, "配置不存在", 404)
			return
		}
		p := &a.settings.Profiles[idx]
		switch {
		case len(parts) == 2 && parts[1] == "update" && r.Method == "POST":
			err = a.refresh(p)
		case len(parts) == 1 && r.Method == "DELETE":
			if p.Enabled && a.core != nil {
				err = errors.New("请先停止内核")
				break
			}
			if err = os.Remove(a.profilePath(p.ID)); err == nil {
				a.settings.Profiles = append(a.settings.Profiles[:idx], a.settings.Profiles[idx+1:]...)
				err = a.save()
			}
		case len(parts) == 1 && r.Method == "PUT":
			var b struct {
				Name    *string `json:"name"`
				Enabled *bool   `json:"enabled"`
			}
			if err = decode(r, &b); err != nil {
				break
			}
			if b.Enabled != nil && a.core != nil {
				err = errors.New("请先停止内核再切换配置")
				break
			}
			if b.Name != nil && strings.TrimSpace(*b.Name) != "" {
				p.Name = *b.Name
			}
			if b.Enabled != nil {
				if *b.Enabled {
					for i := range a.settings.Profiles {
						a.settings.Profiles[i].Enabled = false
					}
				}
				p.Enabled = *b.Enabled
			}
			err = a.save()
		default:
			http.Error(w, "method not allowed", 405)
			return
		}
	case r.URL.Path == "/api/settings" && r.Method == "GET":
		s := a.settings
		s.Profiles = nil
		s.ProxyBackup = nil
		result = s
	case r.URL.Path == "/api/settings" && r.Method == "POST":
		s := a.settings
		if err = decode(r, &s); err != nil {
			break
		}
		if s.Port < 1 || s.Port > 65535 || s.UpdateHours < 0 {
			err = errors.New("端口或更新间隔无效")
			break
		}
		if a.core != nil && (s.Port != a.settings.Port || s.AllowLAN != a.settings.AllowLAN) {
			err = errors.New("请先停止内核再修改监听设置")
			break
		}
		if err = platform.AutoStart(s.AutoStart); err != nil {
			break
		}
		s.Profiles = a.settings.Profiles
		s.ProxyBackup = a.settings.ProxyBackup
		a.settings = s
		err = a.save()
	case r.URL.Path == "/api/logs" && r.Method == "GET":
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 5000 {
			limit = 500
		}
		a.logMu.Lock()
		out := make([]Log, 0)
		for _, l := range a.logs {
			if (r.URL.Query().Get("level") == "" || l.Level == r.URL.Query().Get("level")) && strings.Contains(strings.ToLower(l.Message), strings.ToLower(r.URL.Query().Get("search"))) {
				out = append(out, l)
			}
		}
		a.logMu.Unlock()
		if len(out) > limit {
			out = out[len(out)-limit:]
		}
		result = out
	case r.URL.Path == "/api/traffic" && r.Method == "GET":
		var v any
		v, err = a.clashCall("GET", "/connections")
		if err == nil {
			m := v.(map[string]any)
			connections, _ := m["connections"].([]any)
			up, _ := m["uploadTotal"].(float64)
			down, _ := m["downloadTotal"].(float64)
			now := time.Now()
			upSpeed, downSpeed := 0.0, 0.0
			if !a.trafficTime.IsZero() {
				seconds := now.Sub(a.trafficTime).Seconds()
				upSpeed = max(0, (up-a.uploadTotal)/seconds)
				downSpeed = max(0, (down-a.downloadTotal)/seconds)
			}
			a.trafficTime, a.uploadTotal, a.downloadTotal = now, up, down
			result = map[string]any{"upload": upSpeed, "download": downSpeed, "upload_total": up, "download_total": down, "connections": len(connections)}
		}
	case strings.HasPrefix(r.URL.Path, "/api/connections") && (r.Method == "GET" || r.Method == "DELETE"):
		suffix := strings.TrimPrefix(r.URL.Path, "/api/connections")
		result, err = a.clashCall(r.Method, "/connections"+suffix)
	case r.URL.Path == "/api/nodes" && r.Method == "GET":
		result, err = a.clashCall("GET", "/proxies")
	case r.URL.Path == "/api/node/delay" && r.Method == "POST":
		var b struct {
			Tag string `json:"tag"`
		}
		if err = decode(r, &b); err == nil {
			result, err = a.clashCall("GET", "/proxies/"+url.PathEscape(b.Tag)+"/delay?timeout=5000&url="+url.QueryEscape("https://www.gstatic.com/generate_204"))
		}
	case r.URL.Path == "/api/quit" && r.Method == "POST":
		if err = a.stop(); err == nil {
			select {
			case <-a.quit:
			default:
				close(a.quit)
			}
		}
	default:
		http.Error(w, "not found", 404)
		return
	}
	if err != nil {
		a.lastError = err.Error()
		http.Error(w, err.Error(), 400)
		return
	}
	reply(w, result)
}
func main() {
	dir := os.Getenv("APPDATA")
	if dir == "" {
		dir, _ = os.UserConfigDir()
	}
	dir = filepath.Join(dir, "sing-box-win")
	if e := os.MkdirAll(filepath.Join(dir, "profiles"), 0700); e != nil {
		panic(e)
	}
	if e := os.Chdir(dir); e != nil {
		panic(e)
	}
	a := &App{dir: dir, settings: Settings{Port: 2085, Theme: "dark", Language: "zh-CN", Profiles: []Profile{}}, quit: make(chan struct{})}
	if b, e := os.ReadFile(filepath.Join(dir, "settings.json")); e == nil {
		if e = json.Unmarshal(b, &a.settings); e != nil {
			panic(e)
		}
	}
	if a.settings.ProxyBackup != nil {
		if e := platform.WriteProxy(*a.settings.ProxyBackup); e != nil {
			panic(e)
		}
		a.settings.ProxyBackup = nil
		a.save()
	}
	// Discover manually provisioned profiles as well as UI imports.
	files, _ := filepath.Glob(filepath.Join(dir, "profiles", "*.json"))
	for _, f := range files {
		id := strings.TrimSuffix(filepath.Base(f), ".json")
		found := false
		for _, p := range a.settings.Profiles {
			if p.ID == id {
				found = true
			}
		}
		if !found {
			a.settings.Profiles = append(a.settings.Profiles, Profile{ID: id, Name: id, Type: "local", Updated: time.Now(), Enabled: len(a.settings.Profiles) == 0})
		}
	}
	a.save()
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		panic(e)
	}
	base := "http://" + l.Addr().String()
	os.WriteFile(filepath.Join(dir, "runtime.json"), []byte(fmt.Sprintf(`{"url":%q,"pid":%d}`, base, os.Getpid())), 0600)
	defer os.Remove(filepath.Join(dir, "runtime.json"))
	fmt.Println(base)
	root, _ := fs.Sub(web, "web")
	ui := http.FileServer(http.FS(root))
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", a.api)
	mux.Handle("/", ui)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != l.Addr().String() {
			http.Error(w, "invalid host", 403)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != base {
			http.Error(w, "invalid origin", 403)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; frame-ancestors 'none'")
		mux.ServeHTTP(w, r)
	}), ReadHeaderTimeout: 5 * time.Second}
	go srv.Serve(l)
	if a.settings.AutoConnect {
		a.mu.Lock()
		if e := a.start(); e != nil {
			a.lastError = e.Error()
		}
		a.mu.Unlock()
	}
	if os.Getenv("SINGBOX_NO_BROWSER") == "" {
		if e := platform.OpenUI(base); e != nil {
			a.WriteMessage(blog.LevelWarn, "打开浏览器失败: "+e.Error())
		}
	}
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-a.quit:
				return
			case <-t.C:
				a.mu.Lock()
				for i := range a.settings.Profiles {
					p := &a.settings.Profiles[i]
					if a.settings.UpdateHours > 0 && p.URL != "" && time.Since(p.Updated) >= time.Duration(a.settings.UpdateHours)*time.Hour {
						if e := a.refresh(p); e != nil {
							a.WriteMessage(blog.LevelError, e.Error())
						}
					}
				}
				a.mu.Unlock()
			}
		}
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	select {
	case <-sig:
	case <-a.quit:
	}
	a.mu.Lock()
	a.stop()
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}
