package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/JARVIS/sing-box-win/internal/config"
	"github.com/JARVIS/sing-box-win/internal/platform"
	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	blog "github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/service"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"
)

const version = "0.6.0"

type Profile struct {
	AutoUpdate    *bool     `json:"auto_update,omitempty"`
	UpdateMinutes int       `json:"update_minutes,omitempty"`
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Type          string    `json:"type"`
	URL           string    `json:"url,omitempty"`
	Updated       time.Time `json:"updated"`
	Enabled       bool      `json:"enabled"`
}
type Settings struct {
	AutoStart   bool                  `json:"auto_start"`
	AutoProxy   *bool                 `json:"auto_proxy"`
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
	connectionManager adapter.ConnectionManager
	endpointMu        sync.RWMutex
	endpointStates    map[string]endpointSnapshot
	endpointCancel    context.CancelFunc
	logStreamCancel   context.CancelFunc
	nativeLogs        []Log
	mu                sync.Mutex
	logMu             sync.Mutex
	dir               string
	settings          Settings
	core              *box.Box
	logs              []Log
	lastError         string
	clash             string
	secret            string
	proxy             bool
	quit              chan struct{}
	auth              *engineAuth
	runDir            string
	trafficTime       time.Time
	uploadTotal       float64
	downloadTotal     float64
}

func (a *App) WriteMessage(level blog.Level, msg string) {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	entry := Log{time.Now().Format(time.RFC3339), blog.FormatLevel(level), msg}
	a.logs = append(a.logs, entry)
	if a.dir != "" {
		if f, err := os.OpenFile(filepath.Join(a.dir, "engine.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); err == nil {
			_ = json.NewEncoder(f).Encode(entry)
			_ = f.Close()
		}
	}
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
	a.stopEndpointStatus()
	if a.logStreamCancel != nil {
		a.logStreamCancel()
		a.logStreamCancel = nil
	}
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
	if opt.Experimental.ClashAPI == nil {
		opt.Experimental.ClashAPI = &option.ClashAPIOptions{}
	}
	opt.Experimental.ClashAPI.ExternalController = strings.TrimPrefix(a.clash, "http://")
	opt.Experimental.ClashAPI.Secret = a.secret
	native := a.nativeOptions()
	if native.MemoryLimitMB > 0 {
		debug.SetMemoryLimit(native.MemoryLimitMB * 1024 * 1024)
	} else {
		debug.SetMemoryLimit(math.MaxInt64)
	}
	opt.Log = &option.LogOptions{Level: native.LogLevel, Timestamp: true, DisableColor: false}
	coreContext := config.Context()
	instance, e := box.New(box.Options{Options: opt, Context: coreContext, PlatformLogWriter: a})
	if e != nil {
		return e
	}
	if e = instance.Start(); e != nil {
		instance.Close()
		return e
	}
	a.core = instance
	a.connectionManager = service.FromContext[adapter.ConnectionManager](coreContext)
	a.startEndpointStatus()
	a.startLogStream()
	a.trafficTime = time.Time{}
	a.lastError = ""
	// Missing preference on existing installs defaults to browser-ready starts.
	if runtime.GOOS == "windows" && (a.settings.AutoProxy == nil || *a.settings.AutoProxy) {
		if err := a.setProxy(true); err != nil {
			_ = a.stop()
			a.lastError = "System proxy: " + err.Error()
			return errors.New(a.lastError)
		}
	}
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
		if e = a.saveProxyBackup(); e != nil {
			return e
		}
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
		if e := a.restoreProxy(); e != nil {
			return e
		}
	}
	a.proxy = false
	a.settings.ProxyBackup = nil
	os.Remove(filepath.Join(a.dir, "proxy-backup.json"))
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
	return a.replaceProfile(p, b)
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
	if a.pageAPI(w, r) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var result any = map[string]bool{"ok": true}
	var err error
	switch {
	case r.URL.Path == "/api/activate" && r.Method == "POST":
		// Activation is already authenticated by the bearer gate. The instance
		// header additionally proves the caller read this engine's runtime.json,
		// so a forwarding process cannot pour a link into a newer engine (§0.2.5).
		if !a.auth.activationInstanceMatches(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var b activationPayload
		err = decode(r, &b)
		activated := false
		if err == nil && b.Link != "" {
			err = a.importLink(b.Link)
			activated = err == nil
		} else if err == nil && b.Path != "" {
			_, err = a.importProfileFile(b.Path)
			activated = err == nil
		}
		if err != nil {
			// The second process just shows this error and exits; record it so the
			// already-running UI explains what went wrong with the clicked link/file.
			a.lastError = truncateError(err)
		}
		result = activationResult{Activated: activated}
	case r.URL.Path == "/api/protocol" && r.Method == "POST":
		var b struct{ Enabled bool }
		if err = decode(r, &b); err == nil {
			err = platform.RegisterProtocol(b.Enabled)
		}
		if err == nil {
			err = platform.RegisterFileAssociations(b.Enabled)
		}
	case (r.URL.Path == "/api/profiles/import/file" || r.URL.Path == "/api/profiles/import/bpf") && r.Method == "POST":
		// The native client cannot hand the engine a path to a file it opened
		// inside the FileOpenPicker (that is a broker handle), so it reads bytes
		// and posts them base64-encoded. Content decides the format, as for paths,
		// and "name" overrides the name derived from the file.
		var b struct {
			Name string `json:"name"`
			Path string `json:"path"`
			Data string `json:"data"` // base64 container bytes
		}
		if err = decode(r, &b); err != nil {
			break
		}
		var data []byte
		if strings.TrimSpace(b.Data) != "" {
			data, err = base64.StdEncoding.DecodeString(strings.TrimSpace(b.Data))
		} else if strings.TrimSpace(b.Path) != "" {
			result, err = a.importProfileFile(b.Path)
			break
		} else {
			err = errors.New("请选择要导入的文件")
		}
		if err == nil {
			result, err = a.importProfileBytes(data, b.Name)
		}
	case r.URL.Path == "/api/profiles/import/qr" && r.Method == "POST":
		// A QR image chosen in the file picker arrives as bytes for the same
		// broker-handle reason as a profile file. Decoding happens in qr.go and
		// the result re-enters importProfileBytes/importDeepLink, so the normal
		// size, decompression and unique-name checks still apply.
		var b struct {
			Name string `json:"name"`
			Path string `json:"path"`
			Data string `json:"data"` // base64 image bytes
		}
		if err = decode(r, &b); err != nil {
			break
		}
		var data []byte
		switch {
		case strings.TrimSpace(b.Data) != "":
			data, err = base64.StdEncoding.DecodeString(strings.TrimSpace(b.Data))
		case strings.TrimSpace(b.Path) != "":
			var info os.FileInfo
			if info, err = os.Stat(b.Path); err == nil && info.Size() > qrImageLimit {
				err = errors.New("二维码图片超过 8 MiB")
			} else if err == nil {
				data, err = os.ReadFile(b.Path)
			}
		default:
			err = errors.New("请选择要导入的二维码图片")
		}
		if err == nil {
			result, err = a.importQRImage(data, b.Name)
		}
	case r.URL.Path == "/api/logs" && r.Method == "DELETE":
		a.logMu.Lock()
		a.logs = nil
		a.nativeLogs = nil
		a.logMu.Unlock()
	case r.URL.Path == "/api/node/list" && r.Method == "GET":
		result, err = a.nodeList()
	case r.URL.Path == "/api/node/select" && r.Method == "PUT":
		var b struct{ Group, Name string }
		if err = decode(r, &b); err == nil {
			err = a.selectNode(b.Group, b.Name)
		}
	case r.URL.Path == "/api/tools/endpoints" && r.Method == "GET":
		result, err = a.liveEndpoints()
	case r.URL.Path == "/api/tools/tailscale/exit-node" && r.Method == "POST":
		var b struct {
			Tag string `json:"tag"`
			ID  string `json:"id"`
		}
		if err = decode(r, &b); err == nil {
			err = a.setEndpointExit(b.Tag, b.ID)
		}
	case r.URL.Path == "/api/tools/tailscale" && r.Method == "GET":
		result = a.tailscaleStatus()
	case r.URL.Path == "/api/tools/tailscale/test" && r.Method == "POST":
		result = a.testTailscale()
	case r.URL.Path == "/api/tools/connectivity" && r.Method == "POST":
		result = a.testConnectivity("https://www.google.com/generate_204")
	case r.URL.Path == "/api/tools/info" && r.Method == "GET":
		result = a.toolsInfo()
	case r.URL.Path == "/api/routing" && r.Method == "GET":
		result, err = a.routingInfo()
	case r.URL.Path == "/api/routing" && r.Method == "POST":
		var b struct {
			Mode string `json:"mode"`
		}
		if err = decode(r, &b); err == nil {
			err = a.setRoutingMode(b.Mode)
		}
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
			proxy := a.proxy
			if err = a.stop(); err == nil {
				err = a.start()
				if err == nil {
					err = a.setProxy(proxy)
				}
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
		// A path may point at JSON or BPF; content decides, matching file import.
		// A supplied name wins over the file name, and uniqueProfileName keeps it
		// from colliding with an existing profile.
		if b.URL == "" && b.Path != "" {
			result, err = a.importProfilePath(b.Path, b.Name)
			break
		}
		var data []byte
		typ := "local"
		switch {
		case b.URL != "":
			data, err = fetch(b.URL)
			typ = "remote"
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
		imp := profileImport{Name: b.Name, Type: typ, URL: b.URL, Data: data}
		if typ == "remote" {
			enabled := true
			imp.AutoUpdate = &enabled
			imp.UpdateMinutes = 60
		}
		result, err = a.addProfile(imp)
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
		case len(parts) == 2 && parts[1] == "export" && r.Method == "GET":
			var data []byte
			var name string
			data, name, err = a.exportProfile(*p, r.URL.Query().Get("format"))
			if err != nil {
				break
			}
			// Profile names are usually Chinese; RFC 5987 carries them, and the ASCII
			// fallback keeps older clients happy. The native client picks its own name.
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", `attachment; filename="`+asciiFilename(name)+`"; filename*=UTF-8''`+url.PathEscape(name))
			w.Header().Set("X-Content-Type-Options", "nosniff")
			_, err = w.Write(data)
			return
		case len(parts) == 2 && parts[1] == "qr" && r.Method == "GET":
			// The engine renders the PNG (and decides BPF vs deep-link
			// fallback) so the UI only displays or saves bytes it is given.
			var share qrShare
			share, err = a.profileQR(*p)
			if err != nil {
				break
			}
			payload := share.Payload
			if share.Format == "deep-link" {
				payload = []byte(share.Text)
			}
			var png []byte
			png, err = qrPNG(payload)
			if err != nil {
				break
			}
			result = map[string]any{
				"format": share.Format,
				"note":   share.Note,
				"size":   share.Size,
				"image":  base64.StdEncoding.EncodeToString(png),
			}
		case len(parts) == 2 && parts[1] == "update" && r.Method == "POST":
			err = a.refresh(p)
		case len(parts) == 1 && r.Method == "GET":
			var data []byte
			data, err = os.ReadFile(a.profilePath(p.ID))
			if err == nil {
				result = map[string]any{"profile": p, "content": string(data)}
			}
		case len(parts) == 1 && r.Method == "DELETE":
			err = a.deleteProfile(idx)
		case len(parts) == 1 && r.Method == "PUT":
			var b profileEdit
			if err = decode(r, &b); err == nil {
				err = a.editProfile(idx, b)
			}
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
	case strings.HasPrefix(r.URL.Path, "/api/connections") && r.Method == "DELETE":
		suffix := strings.TrimPrefix(r.URL.Path, "/api/connections")
		if suffix != "" {
			suffix = "/" + url.PathEscape(strings.TrimPrefix(suffix, "/"))
		}
		result, err = a.clashCall("DELETE", "/connections"+suffix)
	case strings.HasPrefix(r.URL.Path, "/api/connections") && r.Method == "GET":
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
		if strings.HasPrefix(r.URL.Path, "/api/core/") {
			a.lastError = truncateError(err)
		}
		http.Error(w, err.Error(), 400)
		return
	}
	reply(w, result)
}
func main() {
	var port int
	flag.Bool("headless", false, "Accepted for compatibility; the engine always runs headless")
	flag.Bool("no-ui", false, "Alias for --headless (accepted for compatibility)")
	flag.IntVar(&port, "port", 0, "API port (0 selects a random port)")
	flag.Parse()
	if port < 0 || port > 65535 {
		fmt.Fprintln(os.Stderr, "invalid port")
		os.Exit(2)
	}

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
	release, primary, e := platform.SingleInstance(dir)
	if e != nil {
		panic(e)
	}
	if !primary {
		// A second launch authenticates to the primary with the token from its
		// protected run directory; it never opens another engine (§0.2.5).
		if e = activateExisting(); e != nil {
			panic(e)
		}
		return
	}
	defer release()
	a := &App{dir: dir, settings: Settings{Port: 2085, Theme: "dark", Language: "zh-CN", Profiles: []Profile{}}, quit: make(chan struct{})}
	if b, e := os.ReadFile(filepath.Join(dir, "settings.json")); e == nil {
		if e = json.Unmarshal(b, &a.settings); e != nil {
			panic(e)
		}
	}
	if b, e := os.ReadFile(filepath.Join(dir, "proxy-backup.json")); e == nil {
		var backup proxyRecord
		if json.Unmarshal(b, &backup) == nil {
			a.settings.ProxyBackup = &backup.Backup
		}
	}
	if a.settings.ProxyBackup != nil {
		if e := a.restoreProxy(); e != nil {
			panic(e)
		}
		a.settings.ProxyBackup = nil
		os.Remove(filepath.Join(dir, "proxy-backup.json"))
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
	for _, arg := range os.Args[1:] {
		switch {
		case isDeepLinkArg(arg):
			if e := a.importLink(arg); e != nil {
				a.lastError = e.Error()
			}
		case fileAssociationArg(arg):
			if _, e := a.importProfileFile(arg); e != nil {
				a.lastError = e.Error()
			}
		}
	}
	a.save()
	if e := a.prepareRuntime(); e != nil {
		panic(e)
	}
	l, e := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if e != nil {
		panic(e)
	}
	base := "http://" + l.Addr().String()
	// runtime.json is published only after the token is in place, so a client can
	// never observe an endpoint it is unable to authenticate to.
	if e := a.publishRuntime(base); e != nil {
		panic(e)
	}
	defer a.unpublishRuntime()
	fmt.Println(base)
	a.WriteMessage(blog.LevelInfo, "HTTP API listening at "+base)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", a.api)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "not found", 404) })
	srv := &http.Server{Handler: a.httpHandler(mux, l.Addr().String(), base), ReadHeaderTimeout: 5 * time.Second}
	go srv.Serve(l)
	if a.settings.AutoConnect {
		a.mu.Lock()
		if e := a.start(); e != nil {
			a.lastError = e.Error()
		}
		a.mu.Unlock()
	}
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-a.quit:
				return
			case <-t.C:
				a.mu.Lock()
				for i := range a.settings.Profiles {
					p := &a.settings.Profiles[i]
					if interval := a.updateInterval(*p); interval > 0 && time.Since(p.Updated) >= interval {
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
