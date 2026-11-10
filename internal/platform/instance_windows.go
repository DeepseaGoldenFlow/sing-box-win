package platform

import (
	"errors"
	"golang.org/x/sys/windows"
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
