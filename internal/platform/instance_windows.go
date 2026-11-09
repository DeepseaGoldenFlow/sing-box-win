package platform

import (
	"errors"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"os"
)

func SingleInstance(dir string) (func(), bool, error) {
	name, _ := windows.UTF16PtrFromString(`Local\sing-box-win-` + os.Getenv("USERNAME"))
	h, err := windows.CreateMutex(nil, false, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return func() {}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return func() { windows.CloseHandle(h) }, true, nil
}
func RegisterProtocol(on bool) error {
	const path = `Software\Classes\singboxwin`
	if !on {
		for _, p := range []string{path + `\shell\open\command`, path + `\shell\open`, path + `\shell`, path} {
			if e := registry.DeleteKey(registry.CURRENT_USER, p); e != nil && e != registry.ErrNotExist {
				return e
			}
		}
		return nil
	}
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	k, _, e := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if e != nil {
		return e
	}
	defer k.Close()
	if e = k.SetStringValue("", "URL:sing-box-win"); e != nil {
		return e
	}
	if e = k.SetStringValue("URL Protocol", ""); e != nil {
		return e
	}
	c, _, e := registry.CreateKey(registry.CURRENT_USER, path+`\shell\open\command`, registry.SET_VALUE)
	if e != nil {
		return e
	}
	defer c.Close()
	return c.SetStringValue("", `"`+exe+`" "%1"`)
}
