package main

import (
	"encoding/json"
	"fmt"
	box "github.com/sagernet/sing-box"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTailscaleStatus(t *testing.T) {
	a := &App{dir: t.TempDir(), settings: Settings{Profiles: []Profile{{ID: "x", Enabled: true}}}}
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(a.profilePath("x"), `{"endpoints":[{"type":"tailscale"}]}`)
	s := a.tailscaleStatus()
	if !s.Enabled || s.Registered || s.IPs == nil {
		t.Fatalf("missing state: %+v", s)
	}
	write(filepath.Join(a.dir, "tailscale", "tailscaled.state"), `{}`)
	path := filepath.Join(a.dir, "tailscale", "profile-data", "x", "netmap-cache", "73656c66")
	write(path, `{"Node":{"Name":"phone-singbox.taild08fe0.ts.net.","Addresses":["100.77.177.75/32"],"KeyExpiry":"2027-05-08T16:37:58Z"}}`)
	s = a.tailscaleStatus()
	if !s.Registered || s.Hostname != "phone-singbox.taild08fe0.ts.net" || len(s.IPs) != 1 || s.IPs[0] != "100.77.177.75" || s.LastSync == "" || s.KeyExpiry != "2027-05-08T16:37:58Z" {
		t.Fatalf("cached state: %+v", s)
	}
	write(path, `{invalid`)
	r := httptest.NewRecorder()
	a.api(r, httptest.NewRequest("GET", "/api/tools/tailscale", nil))
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &s) != nil || !s.Enabled || s.Registered {
		t.Fatalf("invalid cache: %s", r.Body.String())
	}
}
func TestTailscaleProxyTest(t *testing.T) {
	a := &App{}
	if got := a.testTailscale(); got.OK || got.Error != "core not running" {
		t.Fatalf("%+v", got)
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.String() != "http://100.68.38.67/" {
			t.Errorf("unexpected target %s", r.URL)
		}
		w.Header().Set("Location", "http://unreachable.invalid/")
		w.WriteHeader(302)
	}))
	defer proxy.Close()
	var port int
	_, err := fmt.Sscanf(strings.TrimPrefix(proxy.URL, "http://127.0.0.1:"), "%d", &port)
	if err != nil {
		t.Fatal(err)
	}
	a.core = &box.Box{}
	a.settings.Port = port
	if got := a.testTailscale(); !got.OK || got.Status != 302 {
		t.Fatalf("%+v", got)
	}
}
