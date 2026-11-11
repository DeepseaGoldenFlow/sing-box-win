package main

// Profile exchange: BPF containers, JSON files, deep links and export.
//
// Format handling lives in internal/profilefmt and mirrors upstream
// experimental/libbox/profile_import.go; this file wires it into the existing
// profile store (profiles/<id>.json + settings.Profiles) without changing the
// shape of the settings contract consumed by the native client.

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/JARVIS/sing-box-win/internal/config"
	"github.com/JARVIS/sing-box-win/internal/profilefmt"
)

// importLimit bounds what an import may read from disk or the network.
const importLimit = 16 << 20

// filenamePattern strips characters Windows rejects in file names.
var filenamePattern = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]+`)

// safeFilename turns a profile name plus timestamp into a Windows-safe export
// name, e.g. "我的订阅-20261111-153012".
func exportFilename(name string, format string, now time.Time) string {
	base := filenamePattern.ReplaceAllString(strings.TrimSpace(name), "-")
	base = strings.Trim(base, "-. ")
	if base == "" {
		base = "profile"
	}
	if runes := []rune(base); len(runes) > 60 {
		base = string(runes[:60])
	}
	ext := ".json"
	if format == "bpf" {
		ext = ".bpf"
	}
	return fmt.Sprintf("%s-%s%s", base, now.Format("20060102-150405"), ext)
}

// truncateError keeps engine/import errors to a UI-sized string.
func truncateError(err error) string {
	text := err.Error()
	if runes := []rune(text); len(runes) > 500 {
		text = string(runes[:500])
	}
	return text
}

// asciiFilename degrades an export name to ASCII for the legacy
// Content-Disposition filename parameter. The UTF-8 name is sent alongside it in
// filename*, which every current client honours.
func asciiFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r >= 0x20 && r < 0x7f && r != '"' && r != '\\' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "profile"
	}
	return b.String()
}

// uniqueProfileName returns base, or "base (2)", "base (3)"… so an import never
// silently overwrites or shadows an existing profile. SFM does the same with
// its uniqueName helper (SFM ProfileManager).
func (a *App) uniqueProfileName(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "导入的配置"
	}
	taken := func(candidate string) bool {
		for _, p := range a.settings.Profiles {
			if strings.EqualFold(strings.TrimSpace(p.Name), candidate) {
				return true
			}
		}
		return false
	}
	if !taken(base) {
		return base
	}
	for i := 2; i < 10000; i++ {
		candidate := fmt.Sprintf("%s (%d)", base, i)
		if !taken(candidate) {
			return candidate
		}
	}
	return fmt.Sprintf("%s (%d)", base, time.Now().UnixNano())
}

// profileImport is a validated profile ready to be appended to the store.
type profileImport struct {
	Name          string
	Type          string
	URL           string
	Data          []byte
	AutoUpdate    *bool
	UpdateMinutes int
	Updated       time.Time
}

// addProfile validates and persists an imported profile. The configuration is
// parsed before anything is written so a rejected import leaves the store
// untouched.
func (a *App) addProfile(imp profileImport) (Profile, error) {
	if _, err := config.Parse(imp.Data); err != nil {
		return Profile{}, fmt.Errorf("配置内容无效：%w", err)
	}
	if imp.Type != "local" && imp.Type != "remote" {
		imp.Type = "local"
	}
	if imp.Type == "remote" && !validProfileURL(imp.URL) {
		return Profile{}, errors.New("远程配置的 URL 必须是有效的 HTTP/HTTPS 地址")
	}
	name := a.uniqueProfileName(imp.Name)
	updated := imp.Updated
	if updated.IsZero() {
		updated = time.Now()
	}
	p := Profile{
		AutoUpdate:    imp.AutoUpdate,
		UpdateMinutes: imp.UpdateMinutes,
		ID:            id(),
		Name:          name,
		Type:          imp.Type,
		URL:           imp.URL,
		Updated:       updated,
		Enabled:       len(a.settings.Profiles) == 0,
	}
	if err := os.MkdirAll(filepath.Join(a.dir, "profiles"), 0700); err != nil {
		return Profile{}, err
	}
	if err := os.WriteFile(a.profilePath(p.ID), imp.Data, 0600); err != nil {
		return Profile{}, err
	}
	a.settings.Profiles = append(a.settings.Profiles, p)
	if err := a.save(); err != nil {
		a.settings.Profiles = a.settings.Profiles[:len(a.settings.Profiles)-1]
		_ = os.Remove(a.profilePath(p.ID))
		return Profile{}, err
	}
	return p, nil
}

// importDeepLink imports the subscription named by a sing-box:// or
// singboxwin:// link. The remote host default name follows upstream
// ParseRemoteProfileImportLink (libbox/remote_profile.go:32-35).
func (a *App) importDeepLink(link string) (Profile, error) {
	return a.importDeepLinkAs(link, "")
}

// importDeepLinkAs is importDeepLink with a caller-supplied name override,
// mirroring importBPFAs. An empty name keeps the name carried by the link.
func (a *App) importDeepLinkAs(link, name string) (Profile, error) {
	parsed, err := parseDeepLink(link)
	if err != nil {
		return Profile{}, err
	}
	if strings.TrimSpace(name) != "" {
		parsed.Name = name
	}
	data, err := fetch(parsed.URL)
	if err != nil {
		return Profile{}, fmt.Errorf("下载订阅失败：%w", err)
	}
	auto := true
	return a.addProfile(profileImport{
		Name:          parsed.Name,
		Type:          "remote",
		URL:           parsed.URL,
		Data:          data,
		AutoUpdate:    &auto,
		UpdateMinutes: 60,
	})
}

// importBPF decodes a BPF container and imports the profile it carries.
//
// Upstream ProfileContent may hold a remote URL without inline config (the URL
// is the update source). That case is downloaded here so the profile always
// starts with usable bytes, matching how the client stores remote profiles.
func (a *App) importBPF(data []byte) (Profile, error) {
	return a.importBPFAs(data, "")
}

// importBPFAs is importBPF with a caller-supplied name override. An empty name
// keeps the name stored in the container.
func (a *App) importBPFAs(data []byte, name string) (Profile, error) {
	content, err := profilefmt.Decode(data)
	if err != nil {
		return Profile{}, err
	}
	if strings.TrimSpace(name) == "" {
		name = content.Name
	}
	imp := profileImport{
		Name:    name,
		Type:    "local",
		Data:    []byte(content.Config),
		Updated: time.Unix(content.LastUpdated, 0),
	}
	if content.LastUpdated <= 0 {
		imp.Updated = time.Time{}
	}
	if content.IsRemote() {
		if !validProfileURL(content.RemotePath) {
			return Profile{}, errors.New("BPF 中的订阅 URL 无效")
		}
		imp.Type = "remote"
		imp.URL = content.RemotePath
		auto := content.AutoUpdate
		imp.AutoUpdate = &auto
		imp.UpdateMinutes = int(content.AutoUpdateInterval)
		if len(bytes.TrimSpace(imp.Data)) == 0 {
			fetched, err := fetch(content.RemotePath)
			if err != nil {
				return Profile{}, fmt.Errorf("下载 BPF 订阅失败：%w", err)
			}
			imp.Data = fetched
			imp.Updated = time.Time{}
		}
	}
	if len(bytes.TrimSpace(imp.Data)) == 0 {
		return Profile{}, errors.New("BPF 中没有配置内容")
	}
	if _, err := config.Parse(imp.Data); err != nil {
		return Profile{}, fmt.Errorf("BPF 中的配置无效：%w", err)
	}
	return a.addProfile(imp)
}

// importProfileFile dispatches on file content, not extension: a JSON file is
// imported as-is, a BPF container is decoded. Extension is only a hint, because
// users routinely rename downloads.
func (a *App) importProfileFile(path string) (Profile, error) {
	return a.importProfilePath(path, "")
}

// importProfilePath imports a file, optionally overriding the profile name.
// Callers that hand the user a name field (the native import dialog) pass it
// here; association/double-click imports leave it empty and let the container
// or file name decide.
func (a *App) importProfilePath(path, name string) (Profile, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Profile{}, fmt.Errorf("读取配置文件失败：%w", err)
	}
	if info.IsDir() {
		return Profile{}, errors.New("这是一个文件夹，不是配置文件")
	}
	if info.Size() > importLimit {
		return Profile{}, errors.New("配置文件超过 16 MiB")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, fmt.Errorf("读取配置文件失败：%w", err)
	}
	// For JSON the file name is the only name available; BPF containers carry
	// their own name, which wins unless the user typed one.
	if strings.TrimSpace(name) == "" && isJSONDocument(data) {
		name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return a.importProfileBytes(data, name)
}

// importProfileBytes imports container bytes that did not come from a path (the
// native file picker hands over a broker handle, so it posts bytes instead).
// name is the user's choice when they gave one; otherwise a JSON config falls
// back to addProfile's default and a BPF container keeps the name it carries.
func (a *App) importProfileBytes(data []byte, name string) (Profile, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return Profile{}, errors.New("配置文件为空")
	}
	if len(data) > importLimit {
		return Profile{}, errors.New("配置文件超过 16 MiB")
	}
	// The cap applies to decoded bytes: the base64 body is larger, but the UI check
	// and this one bound the same thing, and a hand-crafted request cannot bypass it.
	if isJSONDocument(data) {
		return a.addProfile(profileImport{Name: name, Type: "local", Data: data})
	}
	if isBPFContainer(data) {
		return a.importBPFAs(data, name)
	}
	return Profile{}, errors.New("无法识别的配置文件：既不是 JSON，也不是 sing-box BPF 配置")
}

func isJSONDocument(data []byte) bool {
	trimmed := bytes.TrimSpace(data)
	return len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[')
}

func isBPFContainer(data []byte) bool {
	return profilefmt.LooksLike(data)
}

// exportProfile renders a stored profile as JSON or BPF bytes.
//
// JSON export is the raw stored configuration (byte-for-byte, preserving the
// user's formatting). BPF export wraps the same content in the upstream
// container so SFM and other clients can read it.
func (a *App) exportProfile(p Profile, format string) ([]byte, string, error) {
	data, err := os.ReadFile(a.profilePath(p.ID))
	if err != nil {
		return nil, "", err
	}
	format = strings.ToLower(strings.TrimSpace(format))
	switch format {
	case "", "json":
		return data, exportFilename(p.Name, "json", time.Now()), nil
	case "bpf":
		content := &profilefmt.Content{Name: p.Name, Type: profilefmt.TypeLocal, Config: string(data)}
		if p.Type == "remote" {
			content.Type = profilefmt.TypeRemote
			content.RemotePath = p.URL
			content.AutoUpdate = p.AutoUpdate == nil || *p.AutoUpdate
			content.AutoUpdateInterval = int32(p.UpdateMinutes)
			if content.AutoUpdateInterval <= 0 {
				content.AutoUpdateInterval = 60
			}
			content.LastUpdated = p.Updated.Unix()
			if content.LastUpdated < 0 {
				content.LastUpdated = 0
			}
		}
		encoded, err := profilefmt.Encode(content)
		if err != nil {
			return nil, "", err
		}
		return encoded, exportFilename(p.Name, "bpf", time.Now()), nil
	default:
		return nil, "", fmt.Errorf("不支持的导出格式：%s（可选 json 或 bpf）", format)
	}
}

// generateFmtDeepLink builds the standard sing-box import link for a remote
// profile, mirroring upstream GenerateRemoteProfileImportLink
// (libbox/remote_profile.go:7-15). It is used by the native client to let a
// user copy/share a subscription link.
func generateFmtDeepLink(name, remoteURL string) string {
	link := &url.URL{
		Scheme:   "sing-box",
		Host:     "import-remote-profile",
		RawQuery: url.Values{"url": []string{remoteURL}}.Encode(),
		Fragment: name,
	}
	return link.String()
}
