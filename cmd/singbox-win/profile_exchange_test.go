package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JARVIS/sing-box-win/internal/profilefmt"
)

const sampleConfig = `{"outbounds":[{"type":"direct","tag":"direct"}]}`

func newExchangeApp(t *testing.T) *App {
	t.Helper()
	a := &App{dir: t.TempDir(), settings: Settings{Port: 0}}
	if err := os.MkdirAll(filepath.Join(a.dir, "profiles"), 0700); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestImportBPFLocal(t *testing.T) {
	a := newExchangeApp(t)
	container, err := profilefmt.Encode(&profilefmt.Content{Name: "本地配置", Type: profilefmt.TypeLocal, Config: sampleConfig})
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.importBPF(container)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "本地配置" || p.Type != "local" || !p.Enabled {
		t.Fatalf("unexpected profile %+v", p)
	}
	stored, err := os.ReadFile(a.profilePath(p.ID))
	if err != nil || string(stored) != sampleConfig {
		t.Fatalf("stored content mismatch: %q %v", stored, err)
	}
	if len(a.settings.Profiles) != 1 {
		t.Fatal("profile not registered")
	}
}

func TestImportBPFRemoteWithoutInlineConfigDownloads(t *testing.T) {
	a := newExchangeApp(t)
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(sampleConfig))
	}))
	defer server.Close()
	container, err := profilefmt.Encode(&profilefmt.Content{
		Name:               "远程",
		Type:               profilefmt.TypeRemote,
		RemotePath:         server.URL,
		AutoUpdate:         true,
		AutoUpdateInterval: 30,
		LastUpdated:        1700000000,
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.importBPF(container)
	if err != nil {
		t.Fatal(err)
	}
	if hits != 1 || p.Type != "remote" || p.URL != server.URL || p.UpdateMinutes != 30 || p.AutoUpdate == nil || !*p.AutoUpdate {
		t.Fatalf("unexpected import: hits=%d profile=%+v", hits, p)
	}
}

func TestImportBPFRejectsGarbageWithoutSideEffects(t *testing.T) {
	a := newExchangeApp(t)
	cases := []struct {
		name string
		data []byte
	}{
		{"json passed as bpf", []byte(sampleConfig)},
		{"truncated", []byte{3, 1, 0x1f}},
		{"bomb-like empty config", mustEncode(t, &profilefmt.Content{Name: "x", Type: profilefmt.TypeLocal})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := a.importBPF(c.data); err == nil {
				t.Fatal("accepted invalid BPF")
			}
			if len(a.settings.Profiles) != 0 {
				t.Fatal("rejected import left a profile behind")
			}
			entries, _ := os.ReadDir(filepath.Join(a.dir, "profiles"))
			if len(entries) != 0 {
				t.Fatalf("rejected import wrote files: %v", entries)
			}
		})
	}
}

func TestImportBPFInvalidConfigIsRejected(t *testing.T) {
	a := newExchangeApp(t)
	// Decoding alone does not reject unknown protocols; config.Parse must.
	container := mustEncode(t, &profilefmt.Content{Name: "bad", Type: profilefmt.TypeLocal, Config: `{"outbounds":[{"type":"not-a-protocol","tag":"x"}]}`})
	if _, err := a.importBPF(container); err == nil {
		t.Fatal("accepted an invalid configuration")
	}
	if len(a.settings.Profiles) != 0 {
		t.Fatal("invalid config was stored")
	}
}

func TestImportProfileFileByContent(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "pasted.json")
	if err := os.WriteFile(jsonPath, []byte(sampleConfig), 0600); err != nil {
		t.Fatal(err)
	}
	// BPF content under a .json extension must still be detected by content.
	bpfPath := filepath.Join(dir, "actually-bpf.json")
	if err := os.WriteFile(bpfPath, mustEncode(t, &profilefmt.Content{Name: "bpf", Type: profilefmt.TypeLocal, Config: sampleConfig}), 0600); err != nil {
		t.Fatal(err)
	}
	a := newExchangeApp(t)
	jsonProfile, err := a.importProfileFile(jsonPath)
	if err != nil || jsonProfile.Name != "pasted" || jsonProfile.Type != "local" {
		t.Fatalf("%+v %v", jsonProfile, err)
	}
	bpfProfile, err := a.importProfileFile(bpfPath)
	if err != nil || bpfProfile.Name != "bpf" {
		t.Fatalf("%+v %v", bpfProfile, err)
	}
	if _, err = a.importProfileFile(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("accepted a missing file")
	}
	garbage := filepath.Join(dir, "garbage.json")
	os.WriteFile(garbage, []byte("not json or bpf"), 0600)
	if _, err = a.importProfileFile(garbage); err == nil || !strings.Contains(err.Error(), "无法识别") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestImportDoesNotOverwriteExistingNames(t *testing.T) {
	a := newExchangeApp(t)
	first, err := a.importProfileFile(writeTemp(t, "cfg.json", sampleConfig))
	if err != nil {
		t.Fatal(err)
	}
	container := mustEncode(t, &profilefmt.Content{Name: first.Name, Type: profilefmt.TypeLocal, Config: sampleConfig})
	second, err := a.importBPF(container)
	if err != nil {
		t.Fatal(err)
	}
	if second.Name == first.Name {
		t.Fatalf("duplicate name reused: %q", second.Name)
	}
	if !strings.HasPrefix(second.Name, first.Name+" (") && !strings.HasPrefix(second.Name, "cfg (") {
		t.Fatalf("unexpected unique name %q", second.Name)
	}
	if first.ID == second.ID {
		t.Fatal("duplicate id")
	}
	if len(a.settings.Profiles) != 2 {
		t.Fatal("profile count")
	}
	// The first profile is untouched.
	stored, _ := os.ReadFile(a.profilePath(first.ID))
	if string(stored) != sampleConfig {
		t.Fatal("existing profile overwritten")
	}
}

func TestUniqueProfileNameIgnoresCaseAndTrims(t *testing.T) {
	a := newExchangeApp(t)
	a.settings.Profiles = []Profile{{ID: "a", Name: "Sub"}, {ID: "b", Name: "sub (2)"}}
	if got := a.uniqueProfileName(" sub "); got != "sub (3)" {
		t.Fatalf("got %q", got)
	}
	if got := a.uniqueProfileName(""); got != "导入的配置" {
		t.Fatalf("got %q", got)
	}
}

func TestExportFilename(t *testing.T) {
	now := time.Date(2026, 11, 11, 15, 30, 12, 0, time.UTC)
	cases := map[string]string{
		"我的订阅":           "我的订阅-20261111-153012.json",
		`a/b:c*d?"e<>|f`: "a-b-c-d-e-f-20261111-153012.json",
		"   ":            "profile-20261111-153012.json",
		"..":             "profile-20261111-153012.json",
	}
	for name, want := range cases {
		if got := exportFilename(name, "json", now); got != want {
			t.Fatalf("%q -> %q, want %q", name, got, want)
		}
	}
	if got := exportFilename("x", "bpf", now); got != "x-20261111-153012.bpf" {
		t.Fatalf("bpf name %q", got)
	}
	long := strings.Repeat("长", 100)
	if got := exportFilename(long, "json", now); len([]rune(got)) != 60+len("-20261111-153012.json") {
		t.Fatalf("long name not truncated: %d runes", len([]rune(got)))
	}
}

func TestExportProfileJSONAndBPF(t *testing.T) {
	a := newExchangeApp(t)
	remote := true
	p := Profile{ID: "p1", Name: "订阅", Type: "remote", URL: "https://example.com/a", UpdateMinutes: 60, AutoUpdate: &remote, Updated: time.Unix(1700000000, 0)}
	if err := os.WriteFile(a.profilePath(p.ID), []byte(sampleConfig), 0600); err != nil {
		t.Fatal(err)
	}
	data, name, err := a.exportProfile(p, "json")
	if err != nil || string(data) != sampleConfig || !strings.HasSuffix(name, ".json") || !strings.HasPrefix(name, "订阅-") {
		t.Fatalf("json export: %q %q %v", data, name, err)
	}
	data, name, err = a.exportProfile(p, "bpf")
	if err != nil || !strings.HasSuffix(name, ".bpf") {
		t.Fatalf("bpf export: %q %v", name, err)
	}
	content, err := profilefmt.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if content.Name != "订阅" || content.Type != profilefmt.TypeRemote || content.RemotePath != p.URL || content.Config != sampleConfig || !content.AutoUpdate || content.AutoUpdateInterval != 60 || content.LastUpdated != 1700000000 {
		t.Fatalf("bpf content %+v", content)
	}
	if _, _, err = a.exportProfile(p, "yaml"); err == nil {
		t.Fatal("accepted unknown format")
	}
	if _, _, err = a.exportProfile(Profile{ID: "missing"}, "json"); err == nil {
		t.Fatal("exported a missing profile")
	}
}

func TestExportEndpoint(t *testing.T) {
	a := newExchangeApp(t)
	if _, err := a.addProfile(profileImport{Name: "e", Type: "local", Data: []byte(sampleConfig)}); err != nil {
		t.Fatal(err)
	}
	p := a.settings.Profiles[0]
	w := httptest.NewRecorder()
	a.api(w, httptest.NewRequest("GET", "/api/profiles/"+p.ID+"/export?format=bpf", nil))
	if w.Code != 200 {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("content type %q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "filename*=UTF-8''") || !strings.Contains(cd, ".bpf") {
		t.Fatalf("content disposition %q", cd)
	}
	if _, err := profilefmt.Decode(w.Body.Bytes()); err != nil {
		t.Fatalf("exported bytes are not BPF: %v", err)
	}
	w = httptest.NewRecorder()
	a.api(w, httptest.NewRequest("GET", "/api/profiles/"+p.ID+"/export", nil))
	if w.Code != 200 || w.Body.String() != sampleConfig {
		t.Fatalf("json export %d %q", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	a.api(w, httptest.NewRequest("GET", "/api/profiles/nope/export", nil))
	if w.Code != 404 {
		t.Fatalf("missing profile status %d", w.Code)
	}
}

func TestImportFileEndpoint(t *testing.T) {
	a := newExchangeApp(t)
	path := writeTemp(t, "from-disk.json", sampleConfig)
	w := httptest.NewRecorder()
	a.api(w, httptest.NewRequest("POST", "/api/profiles/import/file", strings.NewReader(`{"path":`+jsonString(path)+`}`)))
	if w.Code != 200 {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if len(a.settings.Profiles) != 1 || a.settings.Profiles[0].Name != "from-disk" {
		t.Fatalf("profiles %+v", a.settings.Profiles)
	}
	w = httptest.NewRecorder()
	a.api(w, httptest.NewRequest("POST", "/api/profiles/import/file", strings.NewReader(`{"path":""}`)))
	if w.Code != 400 {
		t.Fatalf("empty path status %d", w.Code)
	}
}

// The native file picker posts bytes (a broker handle has no usable path), so
// the endpoint must accept base64 for both BPF and JSON payloads.
func TestImportBytesEndpoint(t *testing.T) {
	a := newExchangeApp(t)
	container := mustEncode(t, &profilefmt.Content{Name: "from-bytes", Type: profilefmt.TypeLocal, Config: sampleConfig})
	body := `{"name":"改名","data":"` + base64.StdEncoding.EncodeToString(container) + `"}`
	w := httptest.NewRecorder()
	a.api(w, httptest.NewRequest("POST", "/api/profiles/import/bpf", strings.NewReader(body)))
	if w.Code != 200 {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if len(a.settings.Profiles) != 1 || a.settings.Profiles[0].Name != "改名" {
		t.Fatalf("profiles %+v", a.settings.Profiles)
	}
	// A .json file chosen in the picker also arrives here; content decides.
	w = httptest.NewRecorder()
	jsonBody := `{"name":"picked","data":"` + base64.StdEncoding.EncodeToString([]byte(sampleConfig)) + `"}`
	a.api(w, httptest.NewRequest("POST", "/api/profiles/import/file", strings.NewReader(jsonBody)))
	if w.Code != 200 {
		t.Fatalf("json status %d body %s", w.Code, w.Body.String())
	}
	if len(a.settings.Profiles) != 2 || a.settings.Profiles[1].Name != "picked" {
		t.Fatalf("profiles %+v", a.settings.Profiles)
	}
	w = httptest.NewRecorder()
	a.api(w, httptest.NewRequest("POST", "/api/profiles/import/bpf", strings.NewReader(`{"data":""}`)))
	if w.Code != 400 {
		t.Fatalf("empty payload status %d", w.Code)
	}
	w = httptest.NewRecorder()
	a.api(w, httptest.NewRequest("POST", "/api/profiles/import/bpf", strings.NewReader(`{"data":"not base64!"}`)))
	if w.Code != 400 {
		t.Fatalf("bad base64 status %d", w.Code)
	}
	if len(a.settings.Profiles) != 2 {
		t.Fatal("failed byte import added a profile")
	}
}

func TestActivateEndpointReportsFailure(t *testing.T) {
	a := newExchangeApp(t)
	w := httptest.NewRecorder()
	a.api(w, httptest.NewRequest("POST", "/api/activate", strings.NewReader(`{"path":"C:\\nope\\missing.bpf"}`)))
	if w.Code == 200 {
		t.Fatal("missing association file accepted")
	}
	if a.lastError == "" || !strings.Contains(a.lastError, "读取配置文件失败") {
		t.Fatalf("failure not recorded for the UI: %q", a.lastError)
	}
}

func TestActivateEndpointAcceptsPath(t *testing.T) {
	a := newExchangeApp(t)
	path := writeTemp(t, "activated.json", sampleConfig)
	w := httptest.NewRecorder()
	a.api(w, httptest.NewRequest("POST", "/api/activate", strings.NewReader(`{"path":`+jsonString(path)+`}`)))
	if w.Code != 200 {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if len(a.settings.Profiles) != 1 || a.settings.Profiles[0].Name != "activated" {
		t.Fatalf("profiles %+v", a.settings.Profiles)
	}
}

func TestImportDeepLinkStandardScheme(t *testing.T) {
	a := newExchangeApp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sampleConfig))
	}))
	defer server.Close()
	p, err := a.importDeepLink(generateFmtDeepLink("Standard", server.URL))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Standard" || p.Type != "remote" || p.URL != server.URL || p.UpdateMinutes != 60 {
		t.Fatalf("%+v", p)
	}
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer failing.Close()
	if _, err = a.importDeepLink("sing-box://import-remote-profile?url=" + escapeQuery(failing.URL)); err == nil {
		t.Fatal("failing subscription accepted")
	}
	if len(a.settings.Profiles) != 1 {
		t.Fatal("failed download added a profile")
	}
}

// mustEncode encodes a container or fails the test.
func mustEncode(t *testing.T, c *profilefmt.Content) []byte {
	t.Helper()
	data, err := profilefmt.Encode(c)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func jsonString(value string) string {
	b, _ := json.Marshal(value)
	return string(b)
}
