package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func profileRequest(t *testing.T, a *App, method, path, body string, want int) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	a.api(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	if w.Code != want {
		t.Fatalf("%s %s status=%d body=%s", method, path, w.Code, w.Body.String())
	}
	return w
}
func TestProfileManagementAndRecoveryCopies(t *testing.T) {
	a := &App{dir: t.TempDir(), settings: Settings{Port: 0}}
	if err := os.MkdirAll(filepath.Join(a.dir, "profiles"), 0700); err != nil {
		t.Fatal(err)
	}
	source := `{"outbounds":[{"type":"direct","tag":"direct"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(source)) }))
	defer server.Close()
	profileRequest(t, a, "POST", "/api/profiles/import", `{"name":"first","url":"`+server.URL+`"}`, 200)
	id := a.settings.Profiles[0].ID
	path := "/api/profiles/" + id
	w := profileRequest(t, a, "GET", path, "", 200)
	var detail struct{ Content string }
	json.Unmarshal(w.Body.Bytes(), &detail)
	if detail.Content != source {
		t.Fatal("content readback differs")
	}
	profileRequest(t, a, "PUT", path, `{"name":"renamed","auto_update":true,"update_minutes":5}`, 200)
	if a.settings.Profiles[0].Name != "renamed" || a.updateInterval(a.settings.Profiles[0]) != 5*time.Minute {
		t.Fatal("metadata not saved")
	}
	profileRequest(t, a, "PUT", path, `{"url":"file:///tmp/private"}`, 400)
	profileRequest(t, a, "PUT", path, `{"name":"bad","content":"not-json"}`, 400)
	if a.settings.Profiles[0].Name != "renamed" {
		t.Fatal("invalid content partially saved metadata")
	}
	profileRequest(t, a, "PUT", path, `{"content":"{\"outbounds\":[{\"type\":\"direct\",\"tag\":\"changed\"}]}"}`, 200)
	backups, _ := filepath.Glob(filepath.Join(a.dir, "profile-history", "*.json"))
	if len(backups) != 1 {
		t.Fatal("missing edit recovery copy")
	}
	profileRequest(t, a, "POST", path+"/update", "", 200)
	if a.settings.Profiles[0].Updated.IsZero() {
		t.Fatal("update timestamp missing")
	}
	if err := a.start(); err != nil {
		t.Fatal(err)
	}
	defer a.stop()
	source = `{"outbounds":[{"type":"direct","tag":"reloaded"}]}`
	previous := a.core
	profileRequest(t, a, "POST", path+"/update", "", 200)
	if a.core == nil || a.core == previous {
		t.Fatal("active profile was not reloaded")
	}
	savedSource := source
	source = `{"outbounds":[{"type":"direct","tag":"new"}],"route":{"final":"missing-outbound"}}`
	profileRequest(t, a, "POST", path+"/update", "", 400)
	restored, err := os.ReadFile(a.profilePath(id))
	if err != nil || string(restored) != savedSource || a.core == nil {
		t.Fatal("failed runtime update did not restore previous working profile")
	}
	profileRequest(t, a, "DELETE", path, "", 400)
	if _, err := os.Stat(a.profilePath(id)); err != nil {
		t.Fatal("active profile deleted")
	}
	a.stop()
	profileRequest(t, a, "POST", "/api/profiles/import", `{"name":"second","content":"{}"}`, 200)
	profileRequest(t, a, "DELETE", path, "", 200)
	if len(a.settings.Profiles) != 1 || !a.settings.Profiles[0].Enabled {
		t.Fatal("remaining profile not selected")
	}
	trash, _ := filepath.Glob(filepath.Join(a.dir, "profile-trash", "*.json"))
	if len(trash) != 1 {
		t.Fatal("missing deletion recovery copy")
	}
	profileRequest(t, a, "GET", path, "", 404)
}
