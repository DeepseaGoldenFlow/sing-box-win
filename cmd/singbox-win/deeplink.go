package main

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// Deep link handling.
//
// SFM and the other official clients accept the standard sing-box scheme:
//
//	sing-box://import-remote-profile?url=<urlEncodedURL>#<name>
//
// as specified by upstream docs/clients/general.md:35 and built by
// experimental/libbox/remote_profile.go:7-40. SFW historically only understood
// its own scheme:
//
//	singboxwin://install-config?url=<url>&name=<name>
//
// Both are parsed here, and parsing is a pure function so it can be unit tested
// without an App or a running engine.

var errNotDeepLink = errors.New("不是深链")

// deepLink is the parsed form of a sing-box/singboxwin import URL.
type deepLink struct {
	Name string
	URL  string
}

// parseDeepLink understands every URL scheme this client registers.
//
// Name resolution follows upstream ParseRemoteProfileImportLink: an explicit
// fragment wins, otherwise the remote host is used. The legacy query "name" is
// honoured before the fragment so existing SFW links keep their label.
func parseDeepLink(link string) (deepLink, error) {
	link = strings.TrimSpace(link)
	if link == "" {
		return deepLink{}, errNotDeepLink
	}
	u, err := url.Parse(link)
	if err != nil {
		return deepLink{}, fmt.Errorf("深链格式无效：%v", err)
	}
	// Go puts the first path segment of an opaque URL into Opaque, so
	// "sing-box:import-remote-profile?..." and "sing-box://import-remote-profile?..."
	// both need checking. Upstream only guarantees the latter.
	action := strings.ToLower(u.Host)
	if action == "" {
		action = strings.ToLower(u.Opaque)
	}
	var name string
	switch strings.ToLower(u.Scheme) {
	case "sing-box":
		if action != "import-remote-profile" {
			return deepLink{}, fmt.Errorf("不支持的 sing-box 深链：%s（仅支持 import-remote-profile）", action)
		}
		name = strings.TrimSpace(u.Fragment)
	case "singboxwin":
		if action != "install-config" {
			return deepLink{}, fmt.Errorf("不支持的 singboxwin 深链：%s（仅支持 install-config）", action)
		}
		name = strings.TrimSpace(u.Query().Get("name"))
		if name == "" {
			name = strings.TrimSpace(u.Fragment)
		}
	default:
		return deepLink{}, errNotDeepLink
	}
	remote := strings.TrimSpace(u.Query().Get("url"))
	if remote == "" {
		return deepLink{}, errors.New("深链缺少 url 参数")
	}
	if !validProfileURL(remote) {
		return deepLink{}, errors.New("深链中的订阅 URL 必须是有效的 HTTP/HTTPS 地址")
	}
	if name == "" {
		parsed, _ := url.Parse(remote)
		name = parsed.Hostname()
	}
	return deepLink{Name: name, URL: remote}, nil
}

// isDeepLinkArg reports whether a command line argument should be handled as a
// deep link rather than a file path.
func isDeepLinkArg(arg string) bool {
	lower := strings.ToLower(strings.TrimSpace(arg))
	return strings.HasPrefix(lower, "sing-box:") || strings.HasPrefix(lower, "singboxwin:")
}

// fileAssociationArg reports whether an argument looks like a profile file that
// Windows shell association handed to us. It only filters obvious non-files so
// a stray switch is not read as a path; importProfileFile still validates.
func fileAssociationArg(arg string) bool {
	arg = strings.TrimSpace(arg)
	if arg == "" || strings.HasPrefix(arg, "-") || isDeepLinkArg(arg) {
		return false
	}
	switch strings.ToLower(filepath.Ext(arg)) {
	case ".json", ".bpf":
		return true
	default:
		return false
	}
}
