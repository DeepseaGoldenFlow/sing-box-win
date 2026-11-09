package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestDeepLinkImportAndOfflineGroups(t *testing.T) {
	dir := t.TempDir()
	if e := os.Mkdir(filepath.Join(dir, "profiles"), 0700); e != nil {
		t.Fatal(e)
	}
	a := &App{dir: dir}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"outbounds":[{"type":"direct","tag":"direct"},{"type":"selector","tag":"select","outbounds":["direct"],"default":"direct"}]}`))
	}))
	defer server.Close()
	if e := a.importLink("singboxwin://install-config?url=" + url.QueryEscape(server.URL+"/?token=private&x=1")); e != nil {
		t.Fatal(e)
	}
	if len(a.settings.Profiles) != 1 || !a.settings.Profiles[0].Enabled {
		t.Fatal("import did not select initial profile")
	}
	nodes, e := a.nodeList()
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(nodes)
	var list struct {
		Proxies map[string]struct {
			All []string
			Now string
		}
	}
	json.Unmarshal(b, &list)
	if len(list.Proxies["select"].All) != 1 || list.Proxies["select"].Now != "direct" {
		t.Fatalf("group members missing: %s", b)
	}
	if e = a.importLink("https://install-config?url=" + url.QueryEscape(server.URL)); e == nil {
		t.Fatal("accepted foreign scheme")
	}
}
func TestFailedStartPreservesOriginalError(t *testing.T) {
	a := &App{dir: t.TempDir(), settings: Settings{Profiles: []Profile{{ID: "missing", Enabled: true}}}}
	r := httptest.NewRequest("POST", "/api/core/start", nil)
	w := httptest.NewRecorder()
	a.api(w, r)
	if w.Code != 400 || a.lastError == "" || a.lastError == "启动失败" {
		t.Fatal("missing original error")
	}
}
