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

func ReadProxy() (ProxyBackup, error) {
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
