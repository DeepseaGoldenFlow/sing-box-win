package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JARVIS/sing-box-win/internal/platform"
)

const testToken = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"

func authTestApp(t *testing.T) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "profiles"), 0700); err != nil {
		t.Fatal(err)
	}
	a := &App{dir: dir, settings: Settings{Profiles: []Profile{}}, quit: make(chan struct{})}
	a.auth = newEngineAuth(testToken, strings.Repeat("a", 32))
	return a, dir
}

// newTestServer mounts the same handler chain as main() so tests cover host,
// origin and the bearer gate instead of calling a.api directly. The handler is
// installed before Start: assigning Config.Handler after the server is serving
// would race with net/http reading it.
func newTestServer(t *testing.T, a *App) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", a.api)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "not found", 404) })
	srv := httptest.NewUnstartedServer(nil)
	listener := srv.Listener
	host := listener.Addr().String()
	base := "http://" + host
	srv.Config.Handler = a.httpHandler(mux, host, base)
	srv.Start()
	return srv
}

func doRequest(t *testing.T, method, url, auth, instance string, body string) *http.Response {
	t.Helper()
	var r *http.Request
	var err error
	if body == "" {
		r, err = http.NewRequest(method, url, nil)
	} else {
		r, err = http.NewRequest(method, url, strings.NewReader(body))
	}
	if err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	if instance != "" {
		r.Header.Set(instanceHeader, instance)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestEngineAPIRequiresBearerToken(t *testing.T) {
	a, _ := authTestApp(t)
	srv := newTestServer(t, a)
	defer srv.Close()

	cases := []struct {
		name   string
		auth   string
		status int
	}{
		{"missing", "", 401},
		{"wrong", "Bearer definitely-wrong", 401},
		{"empty", "Bearer ", 401},
		{"wrong scheme", "Basic " + testToken, 401},
		{"extra scheme", "Bearer Bearer " + testToken, 401},
		{"correct", "Bearer " + testToken, 200},
	}
	for _, c := range cases {
		resp := doRequest(t, "GET", srv.URL+"/api/state", c.auth, "", "")
		if resp.StatusCode != c.status {
			t.Fatalf("%s: status = %d, want %d", c.name, resp.StatusCode, c.status)
		}
		resp.Body.Close()
	}
}

// Write endpoints must be protected by the same gate; a read-only check would
// miss the case §0.2.4 exists for.
func TestEngineWriteAPIsRequireBearerToken(t *testing.T) {
	a, _ := authTestApp(t)
	srv := newTestServer(t, a)
	defer srv.Close()

	// One entry per write route family, including the ones added later, so a new
	// unauthenticated route cannot slip in behind an older passing check.
	writes := []struct{ method, path, body string }{
		{"POST", "/api/core/start", ""},
		{"POST", "/api/core/stop", ""},
		{"POST", "/api/protocol", `{"enabled":false}`},
		{"POST", "/api/proxy", `{"enabled":false}`},
		{"POST", "/api/quit", ""},
		{"PUT", "/api/node/select", `{"group":"g","name":"n"}`},
		{"DELETE", "/api/logs", ""},
		{"POST", "/api/profiles", `{"name":"n","url":"https://example.com/p"}`},
		{"POST", "/api/profiles/import", `{"name":"n","url":"https://example.com/p"}`},
		{"POST", "/api/profiles/import/file", `{"name":"n","data":""}`},
		{"POST", "/api/profiles/import/bpf", `{"data":""}`},
		{"POST", "/api/profiles/import/qr", `{"name":"n","data":""}`},
		{"POST", "/api/profiles/00000000/update", ""},
		{"PUT", "/api/profiles/00000000", `{"name":"n"}`},
		{"DELETE", "/api/profiles/00000000", ""},
		{"POST", "/api/settings", `{}`},
		{"POST", "/api/routing", `{}`},
		{"POST", "/api/node/delay", `{"group":"g","name":"n"}`},
		{"POST", "/api/tools/connectivity", `{}`},
		{"POST", "/api/tools/tailscale/test", `{}`},
		{"POST", "/api/tools/tailscale/exit-node", `{}`},
		{"POST", "/api/groups/urltest", `{}`},
		{"PUT", "/api/native/core", `{}`},
		{"PUT", "/api/native/profile", `{}`},
	}
	for _, w := range writes {
		resp := doRequest(t, w.method, srv.URL+w.path, "", "", w.body)
		if resp.StatusCode != 401 {
			t.Fatalf("%s %s without token: status %d, want 401", w.method, w.path, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestActivateRequiresBearerAndInstance(t *testing.T) {
	a, _ := authTestApp(t)
	srv := newTestServer(t, a)
	defer srv.Close()

	body := `{"link":"sing-box://import-remote-profile?url=https%3A%2F%2Fexample.com%2Fp"}`
	resp := doRequest(t, "POST", srv.URL+"/api/activate", "", "", body)
	if resp.StatusCode != 401 {
		t.Fatalf("activate without token: %d", resp.StatusCode)
	}
	resp.Body.Close()

	// A wrong token is 401 even when the instance header is right.
	resp = doRequest(t, "POST", srv.URL+"/api/activate", "Bearer definitely-wrong", a.auth.instanceID, body)
	if resp.StatusCode != 401 {
		t.Fatalf("activate with wrong token: %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Correct token but no instance binding must also fail §0.2.5.
	resp = doRequest(t, "POST", srv.URL+"/api/activate", "Bearer "+testToken, "", `{}`)
	if resp.StatusCode != 401 {
		t.Fatalf("activate without instance header: %d", resp.StatusCode)
	}
	resp.Body.Close()

	// A stale instance (previous engine) must not reach this engine either.
	resp = doRequest(t, "POST", srv.URL+"/api/activate", "Bearer "+testToken, strings.Repeat("b", 32), `{}`)
	if resp.StatusCode != 401 {
		t.Fatalf("activate with stale instance: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// A correct token must be accepted by the same write path that rejected the
// unauthenticated request, so the gate is not simply rejecting everything.
func TestAuthenticatedWriteSucceeds(t *testing.T) {
	a, _ := authTestApp(t)
	srv := newTestServer(t, a)
	defer srv.Close()

	resp := doRequest(t, "DELETE", srv.URL+"/api/logs", "Bearer "+testToken, "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authenticated DELETE /api/logs: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestActivateEmptyPayloadDoesNotImport(t *testing.T) {
	a, _ := authTestApp(t)
	srv := newTestServer(t, a)
	defer srv.Close()

	resp := doRequest(t, "POST", srv.URL+"/api/activate", "Bearer "+testToken, a.auth.instanceID, `{}`)
	if resp.StatusCode != 200 {
		t.Fatalf("activate empty payload: %d", resp.StatusCode)
	}
	var result activationResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if result.Activated || len(a.settings.Profiles) != 0 {
		t.Fatal("empty activation must only activate the window, not import")
	}
}

func TestTokenFromAuthorizationStrictness(t *testing.T) {
	if _, ok := tokenFromAuthorization("Bearer"); ok {
		t.Fatal("bare scheme accepted")
	}
	if _, ok := tokenFromAuthorization("Bearer  " + testToken); ok {
		t.Fatal("double space accepted")
	}
	if _, ok := tokenFromAuthorization("Bearer " + testToken + " extra"); ok {
		t.Fatal("trailing token accepted")
	}
	if v, ok := tokenFromAuthorization("bearer " + testToken); !ok || v != testToken {
		t.Fatal("scheme should be case-insensitive")
	}
}

func TestValidRuntimeURL(t *testing.T) {
	good := []string{"http://127.0.0.1:2085", "http://127.0.0.1:12345/", "http://[::1]:2085"}
	for _, raw := range good {
		if _, err := validRuntimeURL(raw); err != nil {
			t.Fatalf("%s rejected: %v", raw, err)
		}
	}
	bad := []string{
		"https://127.0.0.1:2085",
		"http://example.com:2085",
		"http://user:pass@127.0.0.1:2085",
		"http://127.0.0.1:2085/api",
		"http://127.0.0.1:2085/?x=1",
		"http://10.0.0.1:2085",
	}
	for _, raw := range bad {
		if _, err := validRuntimeURL(raw); err == nil {
			t.Fatalf("%s accepted", raw)
		}
	}
}

func TestActivationFromArgsIgnoresUnknown(t *testing.T) {
	p := activationFromArgs([]string{"--headless", "/tmp/a.txt", "singboxwin://install-config?url=https%3A%2F%2Fexample.com"})
	if p.Path != "" || p.Link == "" {
		t.Fatalf("unexpected payload %+v", p)
	}
}

func TestRuntimeTokenRoundTrip(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	instance, err := platform.NewInstanceID()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := platform.CreateRunDir(instance)
	if err != nil {
		t.Fatal(err)
	}
	token, err := platform.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	if err = platform.WriteToken(dir, token); err != nil {
		t.Fatal(err)
	}
	got, err := platform.ReadToken(dir)
	if err != nil || got != token {
		t.Fatalf("read token: %q %v", got, err)
	}
	// A malformed or quoted token file must be rejected, not sent as a header.
	if err = os.WriteFile(filepath.Join(dir, "engine.token"), []byte("Bearer "+token), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = platform.ReadToken(dir); err == nil {
		t.Fatal("quoted token file accepted")
	}
	info := platform.RuntimeInfo{URL: "http://127.0.0.1:2085", PID: os.Getpid(), InstanceID: instance, ProtocolVersion: platform.RuntimeProtocolVersion}
	if err = platform.WriteRuntime(dir, info); err != nil {
		t.Fatal(err)
	}
	read, err := platform.ReadRuntime(dir)
	if err != nil || read.URL != info.URL || read.PID != os.Getpid() {
		t.Fatalf("read runtime: %+v %v", read, err)
	}
}

func TestRuntimeVersionAndInstanceValidation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	dir, err := platform.CreateRunDir(strings.Repeat("b", 32))
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"url":"http://127.0.0.1:1","pid":1,"instance_id":"` + strings.Repeat("b", 32) + `","protocol_version":99}`
	if err = os.WriteFile(filepath.Join(dir, "runtime.json"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = platform.ReadRuntime(dir); err == nil {
		t.Fatal("unknown protocol version accepted")
	}
}

// runtime.json must never contain the token; guard against a future field leak.
func TestRuntimeJSONHasNoToken(t *testing.T) {
	a, _ := authTestApp(t)
	a.runDir = t.TempDir()
	info := platform.RuntimeInfo{URL: "http://127.0.0.1:1", PID: os.Getpid(), InstanceID: a.auth.instanceID}
	if err := platform.WriteRuntime(a.runDir, info); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(a.runDir, "runtime.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), testToken) {
		t.Fatal("runtime.json contains the engine token")
	}
}
