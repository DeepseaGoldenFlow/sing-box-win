package platform

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SingleInstance keeps the one-engine-per-user rule. The previous mutex was
// named by USERNAME and had no DACL: the name is guessable and any process
// could create or probe it, so it was never an authentication boundary
// (docs/proposal-tun-windows.md §0.1). Here the name carries the user SID and
// the mutex has an explicit DACL limited to that user and SYSTEM. It is still
// only a mutex — requests are authenticated separately by engine.token.
func SingleInstance(dir string) (func(), bool, error) {
	sid, err := CurrentUserSID()
	if err != nil {
		return nil, false, err
	}
	// D: explicit DACL; GA = GENERIC_ALL. SYSTEM is needed for recovery
	// diagnostics; the interactive user owns normal operation.
	sd, err := windows.SecurityDescriptorFromString("D:(A;;GA;;;SY)(A;;GA;;;" + sid + ")")
	if err != nil {
		return nil, false, err
	}
	sa := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
	}
	name, err := windows.UTF16PtrFromString(`Local\sing-box-win-` + sid)
	if err != nil {
		return nil, false, err
	}
	h, err := windows.CreateMutex(sa, false, name)
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
