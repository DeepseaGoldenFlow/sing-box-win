package platform

import (
	"fmt"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"os"
	"path/filepath"
)

const internet = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

type ProxyBackup struct {
	Enabled    uint64 `json:"enabled"`
	Server     string `json:"server"`
	HadEnabled bool   `json:"had_enabled"`
	HadServer  bool   `json:"had_server"`
}

// ReadProxy reads only the current process user's HKCU. The proxy owner rule
// (docs/proposal-tun-windows.md §0.3) depends on this: a SYSTEM process calling
// it reads SYSTEM's hive, so the broker must never call it on a user's behalf.
func ReadProxy() (ProxyBackup, error) {
	if err := requireInteractiveUser("read system proxy"); err != nil {
		return ProxyBackup{}, err
	}
	k, e := registry.OpenKey(registry.CURRENT_USER, internet, registry.QUERY_VALUE)
	if e != nil {
		return ProxyBackup{}, e
	}
	defer k.Close()
	v, _, a := k.GetIntegerValue("ProxyEnable")
	s, _, b := k.GetStringValue("ProxyServer")
	return ProxyBackup{v, s, a == nil, b == nil}, nil
}
func WriteProxy(b ProxyBackup) error {
	if err := requireInteractiveUser("write system proxy"); err != nil {
		return err
	}
	k, e := registry.OpenKey(registry.CURRENT_USER, internet, registry.SET_VALUE)
	if e != nil {
		return e
	}
	defer k.Close()
	if b.HadEnabled {
		e = k.SetDWordValue("ProxyEnable", uint32(b.Enabled))
	} else {
		e = k.DeleteValue("ProxyEnable")
		if e == registry.ErrNotExist {
			e = nil
		}
	}
	if e != nil {
		return e
	}
	if b.HadServer {
		e = k.SetStringValue("ProxyServer", b.Server)
	} else {
		e = k.DeleteValue("ProxyServer")
		if e == registry.ErrNotExist {
			e = nil
		}
	}
	if e != nil {
		return e
	}
	p := windows.NewLazySystemDLL("wininet.dll").NewProc("InternetSetOptionW")
	for _, n := range []uintptr{39, 37} {
		r, _, err := p.Call(0, n, 0, 0)
		if r == 0 {
			return fmt.Errorf("InternetSetOption: %v", err)
		}
	}
	return nil
}
func AutoStart(on bool) error {
	if err := requireInteractiveUser("configure autostart"); err != nil {
		return err
	}
	k, _, e := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if e != nil {
		return e
	}
	defer k.Close()
	if !on {
		e = k.DeleteValue("sing-box-win")
		if e == registry.ErrNotExist {
			return nil
		}
		return e
	}
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	native := filepath.Join(filepath.Dir(exe), "SingBoxWin.Native.exe")
	if _, err := os.Stat(native); err == nil {
		return k.SetStringValue("sing-box-win", `"`+native+`" --background`)
	}
	return k.SetStringValue("sing-box-win", `"`+exe+`"`)
}

// requireInteractiveUser rejects service/system identities from per-user HKCU
// operations. LocalSystem's HKCU belongs to SYSTEM, never to the logged-in
// user; §0.3 forbids the broker from writing it or loading a user hive in its
// place. The interactive user's own engine process is the only writer.
func requireInteractiveUser(action string) error {
	info, err := CurrentTokenInfo()
	if err != nil {
		return err
	}
	if info.IsLocalSystem {
		return fmt.Errorf("%s is not allowed for the SYSTEM identity; the per-user engine owns HKCU", action)
	}
	// A service account other than LocalSystem would likewise read its own
	// profile hive, not the logged-in user's. Only an interactive session-owner
	// process may write the user proxy (§0.3).
	if info.SessionID == 0 {
		return fmt.Errorf("%s is not allowed from session 0; the interactive per-user engine owns HKCU", action)
	}
	// An elevated administrator still has their own HKCU, so proxy work
	// remains legal; elevation is not a reason to refuse. The broker never
	// runs as the user.
	return nil
}
