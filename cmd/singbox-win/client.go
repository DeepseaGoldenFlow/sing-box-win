package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/JARVIS/sing-box-win/internal/platform"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// proxyRecord is the per-user HKCU proxy backup. It never contains the engine
// token; only internal/platform token paths do.
type proxyRecord struct {
	Backup platform.ProxyBackup `json:"backup"`
	Server string               `json:"server"`
}

func (a *App) saveProxyBackup() error {
	b, e := json.Marshal(proxyRecord{*a.settings.ProxyBackup, fmt.Sprintf("127.0.0.1:%d", a.settings.Port)})
	if e != nil {
		return e
	}
	p := filepath.Join(a.dir, "proxy-backup.json")
	if e = os.WriteFile(p+".tmp", b, 0600); e != nil {
		return e
	}
	return os.Rename(p+".tmp", p)
}
func (a *App) restoreProxy() error {
	current, e := platform.ReadProxy()
	if e != nil {
		return e
	}
	server := fmt.Sprintf("127.0.0.1:%d", a.settings.Port)
	if b, e := os.ReadFile(filepath.Join(a.dir, "proxy-backup.json")); e == nil {
		var rec proxyRecord
		if json.Unmarshal(b, &rec) == nil {
			server = rec.Server
		}
	}
	if current.Enabled == 1 && current.Server == server {
		return platform.WriteProxy(*a.settings.ProxyBackup)
	}
	return nil
}

// importLink imports a deep link. Both the standard sing-box scheme and the
// legacy singboxwin scheme are accepted; see deeplink.go.
func (a *App) importLink(link string) error {
	_, e := a.importDeepLink(link)
	return e
}
func (a *App) nodeList() (any, error) {
	if a.core != nil {
		return a.clashCall("GET", "/proxies")
	}
	proxies := map[string]any{}
	for _, p := range a.settings.Profiles {
		if !p.Enabled {
			continue
		}
		b, e := os.ReadFile(a.profilePath(p.ID))
		if e != nil {
			return nil, e
		}
		var raw struct {
			Outbounds []struct {
				Tag, Type, Default string
				Outbounds          []string `json:"outbounds"`
			}
		}
		if e = json.Unmarshal(b, &raw); e != nil {
			return nil, e
		}
		for _, n := range raw.Outbounds {
			proxies[n.Tag] = map[string]any{"type": n.Type, "all": n.Outbounds, "now": n.Default, "history": []any{}}
		}
	}
	return map[string]any{"proxies": proxies}, nil
}
func (a *App) selectNode(group, name string) error {
	if a.core == nil {
		return errors.New("core is stopped")
	}
	b, _ := json.Marshal(map[string]string{"name": name})
	r, e := http.NewRequest("PUT", a.clash+"/proxies/"+url.PathEscape(group), bytes.NewReader(b))
	if e != nil {
		return e
	}
	r.Header.Set("Authorization", "Bearer "+a.secret)
	r.Header.Set("Content-Type", "application/json")
	c := http.Client{Timeout: 12 * time.Second}
	resp, e := c.Do(r)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
		return fmt.Errorf("Clash API %d: %s", resp.StatusCode, b)
	}
	return nil
}
