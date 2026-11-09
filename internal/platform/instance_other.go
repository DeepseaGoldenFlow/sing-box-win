//go:build !windows

package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func SingleInstance(dir string) (func(), bool, error) {
	f, e := os.OpenFile(filepath.Join(dir, "instance.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, false, e
	}
	e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if e == syscall.EWOULDBLOCK {
		f.Close()
		return func() {}, false, nil
	}
	if e != nil {
		f.Close()
		return nil, false, e
	}
	return func() { f.Close() }, true, nil
}
func RegisterProtocol(bool) error { return fmt.Errorf("protocol registration requires Windows") }
