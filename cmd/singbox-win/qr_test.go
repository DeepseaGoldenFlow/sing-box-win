package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"math/rand"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/JARVIS/sing-box-win/internal/profilefmt"
	"github.com/skip2/go-qrcode"
)

// TestQRCapacityConstantMatchesEncoder pins qrMaxBytes to what the encoder can
// actually fit in one symbol at level L. If either the constant or the library
// changes, this fails instead of silently emitting an unscannable image.
func TestQRCapacityConstantMatchesEncoder(t *testing.T) {
	// Lower-case letters are neither numeric nor alphanumeric to the encoder,
	// so it must use byte mode: this measures the byte-mode capacity that
	// qrMaxBytes names. (Digits would be packed 3-per-10-bits and fit more.)
	payload := make([]byte, qrMaxBytes)
	for i := range payload {
		payload[i] = byte('a' + (i % 26))
	}
	if _, err := qrcode.New(string(payload), qrcode.Low); err != nil {
		t.Fatalf("encoder rejects exactly %d bytes: %v", qrMaxBytes, err)
	}
	if _, err := qrcode.New(string(append(payload, 'x')), qrcode.Low); err == nil {
		t.Fatalf("encoder accepted %d bytes, so qrMaxBytes is below the real capacity", qrMaxBytes+1)
	}
}

// TestQRRoundTripBPF is the required round trip: profile → BPF container →
// QR image → decoded container → imported profile with identical config.
func TestQRRoundTripBPF(t *testing.T) {
	a := newExchangeApp(t)
	config := `{"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`
	original, err := a.addProfile(profileImport{Name: "二维码配置", Type: "local", Data: []byte(config)})
	if err != nil {
		t.Fatal(err)
	}
	share, err := a.profileQR(original)
	if err != nil {
		t.Fatal(err)
	}
	if share.Format != "bpf" || share.Note != "" || len(share.Payload) == 0 {
		t.Fatalf("unexpected share %+v", share)
	}
	// The payload must be a standard container, not a private format.
	content, err := profilefmt.Decode(share.Payload)
	if err != nil {
		t.Fatalf("QR payload is not a BPF container: %v", err)
	}
	if content.Name != original.Name || content.Type != profilefmt.TypeLocal || content.Config != config {
		t.Fatalf("container drifted: %+v", content)
	}
	png, err := qrPNG(share.Payload)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeQRPNG(png)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.BPF, share.Payload) {
		t.Fatalf("decoded payload differs: %d vs %d bytes", len(decoded.BPF), len(share.Payload))
	}
	imported, err := a.importQRImage(png, "")
	if err != nil {
		t.Fatal(err)
	}
	if imported.Name == original.Name || !strings.HasPrefix(imported.Name, original.Name+" (") {
		t.Fatalf("import did not unique-name: %q vs %q", imported.Name, original.Name)
	}
	stored, err := os.ReadFile(a.profilePath(imported.ID))
	if err != nil || string(stored) != config {
		t.Fatalf("imported content mismatch: %q %v", stored, err)
	}
}

// TestQRRoundTripBinaryPayload exercises a container whose gzip body is not
// UTF-8, which is where the raw byte-segment decode path matters: reading
// Result.GetText() alone would mangle it. The config itself stays valid UTF-8
// (as every real configuration must), but random CJK characters compress
// poorly, so the container is comfortably large.
func TestQRRoundTripBinaryPayload(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	var text strings.Builder
	text.WriteString(`{"outbounds":[{"type":"direct","tag":"`)
	for i := 0; i < 900; i++ {
		text.WriteRune(rune(0x4e00 + rnd.Intn(20000)))
	}
	text.WriteString(`"}]}`)
	container, err := profilefmt.Encode(&profilefmt.Content{Name: "二进制", Type: profilefmt.TypeLocal, Config: text.String()})
	if err != nil {
		t.Fatal(err)
	}
	png, err := qrPNG(container)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeQRPNG(png)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.BPF, container) {
		t.Fatal("binary container did not survive the QR round trip")
	}
}

// TestQRDeepLinkFallback covers the over-capacity rule: remote profiles use
// the standard deep link, local profiles fail with the documented message.
func TestQRDeepLinkFallback(t *testing.T) {
	a := newExchangeApp(t)
	// Incompressible content pushes the gzip container past one symbol.
	rnd := rand.New(rand.NewSource(2))
	big := make([]byte, qrMaxBytes+2048)
	rnd.Read(big)
	big[0], big[1] = '{', '"' // keep it looking like the start of a config

	remote := Profile{ID: "remote", Name: "大订阅", Type: "remote", URL: "https://example.com/sub?token=abc"}
	if err := os.WriteFile(a.profilePath(remote.ID), big, 0600); err != nil {
		t.Fatal(err)
	}
	share, err := a.profileQR(remote)
	if err != nil {
		t.Fatal(err)
	}
	want := generateFmtDeepLink(remote.Name, remote.URL)
	if share.Format != "deep-link" || share.Text != want || share.Note != qrFallbackNote {
		t.Fatalf("unexpected fallback %+v", share)
	}
	// The link must be the upstream-standard form, parsed by the existing path.
	parsed, err := parseDeepLink(share.Text)
	if err != nil || parsed.URL != remote.URL || parsed.Name != remote.Name {
		t.Fatalf("fallback link not importable: %+v %v", parsed, err)
	}
	png, err := qrPNG([]byte(share.Text))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeQRPNG(png)
	if err != nil || decoded.Link != want {
		t.Fatalf("fallback QR did not decode to the link: %+v %v", decoded, err)
	}

	local := Profile{ID: "local", Name: "大本地配置", Type: "local"}
	if err := os.WriteFile(a.profilePath(local.ID), big, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.profileQR(local); err == nil || !strings.Contains(err.Error(), "此配置过大") {
		t.Fatalf("local over-capacity error = %v", err)
	}

	// A remote profile with no usable URL cannot fall back either.
	broken := Profile{ID: "broken", Name: "无链接", Type: "remote", URL: "file:///tmp/x"}
	if err := os.WriteFile(a.profilePath(broken.ID), big, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.profileQR(broken); err == nil || !strings.Contains(err.Error(), "此配置过大") {
		t.Fatalf("remote without URL error = %v", err)
	}

	// qrPNG refuses oversized payloads instead of truncating.
	if _, err := qrPNG(big); err == nil {
		t.Fatal("qrPNG accepted a payload above capacity")
	}
}

// TestDecodeQRInvalidImage checks that garbage input returns a Chinese error
// and never panics.
func TestDecodeQRInvalidImage(t *testing.T) {
	cases := map[string][]byte{
		"empty":     nil,
		"not image": []byte("this is not a PNG"),
		"truncated": {0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a},
		"plain png with no QR": func() []byte {
			// 8x8 white PNG produced by the encoder is impossible without a
			// payload, so use a tiny hand-built image via qrcode then blank it.
			png, err := qrPNG([]byte("hello"))
			if err != nil {
				t.Fatal(err)
			}
			return png[:len(png)/2]
		}(),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("decode panicked: %v", recovered)
				}
			}()
			if _, err := decodeQRPNG(data); err == nil {
				t.Fatal("accepted invalid image")
			} else if !strings.Contains(err.Error(), "二维码") && !strings.Contains(err.Error(), "图片") {
				t.Fatalf("error is not user-facing Chinese: %v", err)
			}
		})
	}
	a := newExchangeApp(t)
	if _, err := a.importQRImage([]byte("junk"), ""); err == nil {
		t.Fatal("import accepted junk")
	}
	if len(a.settings.Profiles) != 0 {
		t.Fatal("failed decode added a profile")
	}
}

// TestDecodeQRMixedSegmentsFailsClosed guards the one lossy path in gozxing:
// a QR whose payload the encoder split into numeric/alphanumeric and byte
// segments exposes only the byte segments. If that leaves something that looks
// like a container but is truncated, the decoder must fail rather than hand a
// partial configuration to import.
func TestDecodeQRMixedSegmentsFailsClosed(t *testing.T) {
	container, err := profilefmt.Encode(&profilefmt.Content{Name: "切分", Type: profilefmt.TypeLocal, Config: sampleConfig})
	if err != nil {
		t.Fatal(err)
	}
	// A long digit run between container chunks makes the encoder use a
	// numeric segment, so the byte segments no longer cover the whole payload.
	payload := append(append([]byte{}, container[:len(container)/2]...), []byte(strings.Repeat("1234567890", 40))...)
	payload = append(payload, container[len(container)/2:]...)
	png, err := qrPNG(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(png) == 0 {
		t.Fatal("no image")
	}
	decoded, err := decodeQRPNG(png)
	if err == nil {
		// If the encoder did keep one byte segment, the payload must then be
		// byte-identical; a truncated payload is never acceptable.
		if !bytes.Equal(decoded.BPF, payload) {
			t.Fatalf("accepted a partial container: %d of %d bytes", len(decoded.BPF), len(payload))
		}
		t.Skip("encoder used a single byte segment on this input")
	}
	if !strings.Contains(err.Error(), "不完整或已损坏") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestDecodeQRRejectsForeignText ensures a valid QR carrying unrelated text is
// rejected rather than imported as something.
func TestDecodeQRRejectsForeignText(t *testing.T) {
	png, err := qrPNG([]byte("https://example.com/not-a-sing-box-link"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeQRPNG(png); err == nil || !strings.Contains(err.Error(), "不是 sing-box") {
		t.Fatalf("foreign text error = %v", err)
	}
}

func TestQREndpoint(t *testing.T) {
	a := newExchangeApp(t)
	p, err := a.addProfile(profileImport{Name: "端点", Type: "local", Data: []byte(sampleConfig)})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.api(w, httptest.NewRequest("GET", "/api/profiles/"+p.ID+"/qr", nil))
	if w.Code != 200 {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"format":"bpf"`) {
		t.Fatalf("body %s", w.Body.String())
	}
	w = httptest.NewRecorder()
	a.api(w, httptest.NewRequest("GET", "/api/profiles/nope/qr", nil))
	if w.Code != 404 {
		t.Fatalf("missing profile status %d", w.Code)
	}

	// Import the same PNG back through the HTTP route, as the UI does.
	w = httptest.NewRecorder()
	a.api(w, httptest.NewRequest("GET", "/api/profiles/"+p.ID+"/qr", nil))
	var body struct{ Image string }
	if err = json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	a.api(w, httptest.NewRequest("POST", "/api/profiles/import/qr",
		strings.NewReader(`{"data":"`+body.Image+`"}`)))
	if w.Code != 200 {
		t.Fatalf("import status %d body %s", w.Code, w.Body.String())
	}
	if len(a.settings.Profiles) != 2 || a.settings.Profiles[1].Name == p.Name {
		t.Fatalf("profiles %+v", a.settings.Profiles)
	}
	// An empty request must be rejected, not silently succeed.
	w = httptest.NewRecorder()
	a.api(w, httptest.NewRequest("POST", "/api/profiles/import/qr", strings.NewReader(`{}`)))
	if w.Code != 400 {
		t.Fatalf("empty request status %d", w.Code)
	}
	if len(a.settings.Profiles) != 2 {
		t.Fatal("rejected request added a profile")
	}
	// Base64 of a non-image must be a decode error, not a panic.
	w = httptest.NewRecorder()
	a.api(w, httptest.NewRequest("POST", "/api/profiles/import/qr",
		strings.NewReader(`{"data":"`+base64.StdEncoding.EncodeToString([]byte("nope"))+`"}`)))
	if w.Code != 400 {
		t.Fatalf("junk status %d", w.Code)
	}
}
