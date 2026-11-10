package platform

import (
	"os"
	"runtime"
	"testing"
)

// The registry round trip touches the real HKCU of whoever runs the tests, so it
// only runs when explicitly requested; scripts/test-windows-associations.ps1 is
// the normal end-to-end check. Without the opt-in this still asserts the
// platform contract: non-Windows claims no keys, Windows declares all of them.
func TestHandlerPaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		if HandlerPaths() != nil {
			t.Fatal("non-Windows must not claim registry paths")
		}
		if err := RegisterProtocol(true); err != nil {
			t.Fatalf("register on a registry-free platform: %v", err)
		}
		if err := RegisterFileAssociations(true); err != nil {
			t.Fatalf("associate on a registry-free platform: %v", err)
		}
		return
	}
	paths := HandlerPaths()
	if len(paths) != 6 {
		t.Fatalf("unexpected handler paths: %v", paths)
	}
}

func TestRegistrationRoundTrip(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("registry only exists on Windows")
	}
	if os.Getenv("SINGBOXWIN_REGISTRY_TEST") != "1" {
		t.Skip("set SINGBOXWIN_REGISTRY_TEST=1 to exercise the real HKCU registry")
	}
	if err := RegisterProtocol(true); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := RegisterFileAssociations(true); err != nil {
		t.Fatalf("associate: %v", err)
	}
	// Re-registering must be idempotent, and must keep the original snapshot.
	if err := RegisterFileAssociations(true); err != nil {
		t.Fatalf("re-associate: %v", err)
	}
	if err := RegisterFileAssociations(false); err != nil {
		t.Fatalf("unassociate: %v", err)
	}
	if err := RegisterProtocol(false); err != nil {
		t.Fatalf("unregister: %v", err)
	}
}
