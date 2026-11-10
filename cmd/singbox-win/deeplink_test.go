package main

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestParseDeepLink(t *testing.T) {
	cases := []struct {
		name     string
		link     string
		wantName string
		wantURL  string
	}{
		// Upstream format: sing-box://import-remote-profile?url=...#name
		// (docs/clients/general.md:35, libbox/remote_profile.go:7-15)
		{
			name:     "standard with fragment",
			link:     "sing-box://import-remote-profile?url=" + escapeQuery("https://example.com/sub?token=abc") + "#My%20Sub",
			wantName: "My Sub",
			wantURL:  "https://example.com/sub?token=abc",
		},
		{
			name:     "standard without fragment uses host",
			link:     "sing-box://import-remote-profile?url=" + escapeQuery("https://sub.example.com/a"),
			wantName: "sub.example.com",
			wantURL:  "https://sub.example.com/a",
		},
		{
			name:     "standard opaque form",
			link:     "sing-box:import-remote-profile?url=" + escapeQuery("https://example.com/x"),
			wantName: "example.com",
			wantURL:  "https://example.com/x",
		},
		{
			name:     "legacy scheme keeps query name",
			link:     "singboxwin://install-config?name=Legacy&url=" + escapeQuery("https://example.com/l"),
			wantName: "Legacy",
			wantURL:  "https://example.com/l",
		},
		{
			name:     "legacy scheme without name uses host",
			link:     "singboxwin://install-config?url=" + escapeQuery("https://example.com/l"),
			wantName: "example.com",
			wantURL:  "https://example.com/l",
		},
		{
			name:     "surrounding whitespace tolerated",
			link:     "  sing-box://import-remote-profile?url=" + escapeQuery("https://example.com/a") + "#N  ",
			wantName: "N",
			wantURL:  "https://example.com/a",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseDeepLink(c.link)
			if err != nil {
				t.Fatal(err)
			}
			if got.Name != c.wantName || got.URL != c.wantURL {
				t.Fatalf("got %+v, want name=%q url=%q", got, c.wantName, c.wantURL)
			}
		})
	}
}

func TestParseDeepLinkRejectsBadInput(t *testing.T) {
	cases := []struct {
		name string
		link string
		want error
	}{
		{"empty", "", errNotDeepLink},
		{"foreign scheme", "https://install-config?url=" + escapeQuery("https://example.com"), errNotDeepLink},
		{"unknown sing-box action", "sing-box://import-local-profile?url=" + escapeQuery("https://example.com"), nil},
		{"unknown legacy action", "singboxwin://other?url=" + escapeQuery("https://example.com"), nil},
		{"missing url", "sing-box://import-remote-profile#name", nil},
		{"non-http url", "sing-box://import-remote-profile?url=file%3A%2F%2F%2Fetc%2Fpasswd", nil},
		{"userinfo url", "sing-box://import-remote-profile?url=" + escapeQuery("https://user:pass@example.com/a"), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseDeepLink(c.link)
			if err == nil {
				t.Fatal("accepted invalid link")
			}
			if c.want != nil && !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
		})
	}
}

func TestDeepLinkArgDetection(t *testing.T) {
	for _, arg := range []string{"sing-box://import-remote-profile?url=x", "SING-BOX://a", "singboxwin://install-config?url=x"} {
		if !isDeepLinkArg(arg) {
			t.Fatalf("%q not detected", arg)
		}
	}
	for _, arg := range []string{"--headless", "C:\\x\\a.json", "", "sing-boxx://a"} {
		if isDeepLinkArg(arg) {
			t.Fatalf("%q wrongly detected", arg)
		}
	}
}

func TestFileAssociationArg(t *testing.T) {
	for _, arg := range []string{"C:\\Users\\me\\config.json", "x.BPF", "/tmp/a.json"} {
		if !fileAssociationArg(arg) {
			t.Fatalf("%q not detected", arg)
		}
	}
	for _, arg := range []string{"--headless", "sing-box://a", "config.txt", ""} {
		if fileAssociationArg(arg) {
			t.Fatalf("%q wrongly detected", arg)
		}
	}
}

func TestGenerateDeepLinkRoundTrips(t *testing.T) {
	link := generateFmtDeepLink("我的 订阅", "https://example.com/a?x=1&y=2")
	if !strings.HasPrefix(link, "sing-box://import-remote-profile?url=") {
		t.Fatalf("unexpected link %q", link)
	}
	got, err := parseDeepLink(link)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "我的 订阅" || got.URL != "https://example.com/a?x=1&y=2" {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

// escapeQuery is url.QueryEscape, which is what upstream
// GenerateRemoteProfileImportLink uses for the url parameter.
func escapeQuery(value string) string { return url.QueryEscape(value) }
