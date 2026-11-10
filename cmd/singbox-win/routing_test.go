package main

import (
	box "github.com/sagernet/sing-box"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestGroupsUseProfileOrderAndExcludeSyntheticGlobal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/configs" {
			w.Write([]byte(`{"mode":"rule"}`))
			return
		}
		w.Write([]byte(`{"proxies":{"GLOBAL":{"type":"Fallback","all":["manual","auto"],"now":"manual"},"manual":{"type":"Selector","all":["auto"],"now":"auto"},"auto":{"type":"URLTest","all":["direct"],"now":"direct"},"direct":{"type":"Direct"}}}`))
	}))
	defer server.Close()
	a := &App{dir: t.TempDir(), core: &box.Box{}, clash: server.URL, settings: Settings{Profiles: []Profile{{ID: "test", Enabled: true}}}}
	os.MkdirAll(filepath.Join(a.dir, "profiles"), 0700)
	original := `{"outbounds":[{"tag":"manual","type":"selector"},{"tag":"auto","type":"urltest"}],"route":{"final":"manual","rules":[{"outbound":"direct"}]}}`
	os.WriteFile(a.profilePath("test"), []byte(original), 0600)
	groups, err := a.groups()
	if err != nil || len(groups) != 2 || groups[0].Tag != "manual" || groups[1].Tag != "auto" {
		t.Fatalf("wrong group list: %+v %v", groups, err)
	}
	if !groups[0].Selectable || groups[1].Selectable {
		t.Fatal("selector capability changed")
	}
	info, err := a.routingInfo()
	if err != nil || info.(map[string]any)["mode"] != "rule" {
		t.Fatal("routing mode not read from live core")
	}
	os.WriteFile(a.profilePath("test"), []byte(`{"outbounds":[{"tag":"GLOBAL","type":"selector"},{"tag":"manual","type":"selector"}]}`), 0600)
	groups, err = a.groups()
	if err != nil || len(groups) != 2 || groups[0].Tag != "GLOBAL" {
		t.Fatal("legitimate profile group called GLOBAL was hidden")
	}
}
