package main

import (
	"context"
	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCloseConnectionAndClearBothLogBuffers(t *testing.T) {
	calls := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.WriteHeader(204)
	}))
	defer server.Close()
	a := &App{core: &box.Box{}, clash: server.URL, logs: []Log{{Message: "legacy"}}, nativeLogs: []Log{{Message: "visible"}}}
	profileRequest(t, a, "DELETE", "/api/connections/owned-test-id", "", 200)
	profileRequest(t, a, "DELETE", "/api/connections", "", 200)
	if len(calls) != 2 || calls[0] != "DELETE /connections/owned-test-id" || calls[1] != "DELETE /connections" {
		t.Fatalf("bad forwarding: %v", calls)
	}
	profileRequest(t, a, "DELETE", "/api/logs", "", 200)
	if len(a.logs) != 0 || len(a.nativeLogs) != 0 {
		t.Fatal("visible logs were not cleared")
	}
}
func TestProfileSwitchRollbackAndPreserveDeclaredMode(t *testing.T) {
	a := &App{dir: t.TempDir(), settings: Settings{Port: 0, Profiles: []Profile{{ID: "working", Enabled: true}, {ID: "bad"}, {ID: "good"}}}}
	t.Chdir(a.dir)
	os.MkdirAll(filepath.Join(a.dir, "profiles"), 0700)
	os.WriteFile(a.profilePath("working"), []byte(`{"experimental":{"clash_api":{"default_mode":"direct"}},"outbounds":[{"type":"direct","tag":"direct"}]}`), 0600)
	os.WriteFile(a.profilePath("bad"), []byte(`{"route":{"final":"missing"}}`), 0600)
	os.WriteFile(a.profilePath("good"), []byte(`{"outbounds":[{"type":"direct","tag":"ok"}]}`), 0600)
	if err := a.start(); err != nil {
		t.Fatal(err)
	}
	defer a.stop()
	if a.connectionManager == nil {
		t.Fatal("core connection manager unavailable")
	}
	configs, err := a.clashCall("GET", "/configs")
	if err != nil || configs.(map[string]any)["mode"] != "direct" {
		t.Fatalf("profile mode was overwritten: %v %v", configs, err)
	}
	if err = a.setRoutingMode("direct"); err != nil {
		t.Fatal(err)
	}
	if err = a.setRoutingMode("unavailable-mode"); err == nil {
		t.Fatal("accepted a mode not advertised by the profile")
	}
	if err = a.switchProfile(1); err == nil {
		t.Fatal("bad profile was accepted")
	}
	if !a.settings.Profiles[0].Enabled || a.core == nil {
		t.Fatal("working session not restored")
	}
	if err = a.switchProfile(2); err != nil {
		t.Fatal(err)
	}
	if !a.settings.Profiles[2].Enabled || a.proxy {
		t.Fatal("switch did not preserve proxy-off state")
	}
}
func TestLiveEndpointProjectionIsExplicitAndBounded(t *testing.T) {
	a := &App{dir: t.TempDir(), settings: Settings{Profiles: []Profile{{ID: "x", Enabled: true}}}}
	os.MkdirAll(filepath.Join(a.dir, "profiles"), 0700)
	os.WriteFile(a.profilePath("x"), []byte(`{"endpoints":[{"tag":"tail","type":"tailscale"}]}`), 0600)
	items, err := a.liveEndpoints()
	if err != nil || len(items) != 1 || items[0]["source"] != "unavailable" {
		t.Fatal("missing live data masquerades as live")
	}
	a.endpointStates = map[string]endpointSnapshot{"tail": {Updated: time.Now(), Status: &adapter.TailscaleEndpointStatus{BackendState: "Running", AuthURL: "PRIVATE_AUTH_URL", Self: &adapter.TailscalePeer{HostName: "test", Online: true}}}}
	items, err = a.liveEndpoints()
	if err != nil || items[0]["backend_state"] != "Running" || items[0]["needs_login"] != true {
		t.Fatal("live status not projected")
	}
	if _, leaked := items[0]["auth_url"]; leaked {
		t.Fatal("auth URL should not be emitted by status endpoint")
	}
	// Stop cancels the subscription before clearing snapshots.
	ctx, cancel := context.WithCancel(context.Background())
	a.endpointCancel = cancel
	a.stopEndpointStatus()
	if ctx.Err() == nil || len(a.endpointStates) != 0 {
		t.Fatal("stale live status survived stop")
	}
}
