package platform

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// Registry layout (HKCU, per-user; no administrator rights needed):
//
//	Software\Classes\sing-box                      standard sing-box:// scheme
//	Software\Classes\singboxwin                    legacy scheme, kept for old links
//	Software\Classes\SingBoxWin.Profile            ProgID for profile files
//	Software\Classes\.bpf                          default handler for .bpf
//	Software\Classes\.json\OpenWithProgids         "Open with" entry only
//	Software\sing-box-win\Backup                   what we replaced, for clean removal
//
// .json is deliberately not taken over as the default handler: claiming every
// JSON file on the machine would hijack unrelated documents. Registering the
// ProgID under OpenWithProgids puts the client in Explorer's "Open with" list
// and lets the user opt in. .bpf is unambiguous, so it is claimed.
//
// Every mutation records the previous state under backupKey, so removal puts the
// machine back instead of blanket-deleting keys that may hold other
// applications' registrations.
const (
	schemeStandard = `Software\Classes\sing-box`
	schemeLegacy   = `Software\Classes\singboxwin`
	progID         = `Software\Classes\SingBoxWin.Profile`
	extBPF         = `Software\Classes\.bpf`
	extJSONOpen    = `Software\Classes\.json\OpenWithProgids`
	backupKey      = `Software\sing-box-win\Backup`
	progIDName     = `SingBoxWin.Profile`

	valueRecorded   = "recorded"
	valueBPFHad     = "bpf_had_default"
	valueBPFDefault = "bpf_default"
	valueBPFKeyHad  = "bpf_key_existed"
	valueJSONKeyHad = "json_key_existed"
)

// RegisterProtocol registers or removes both URL schemes. It is idempotent.
func RegisterProtocol(on bool) error {
	if !on {
		for _, scheme := range []string{schemeStandard, schemeLegacy} {
			if e := deleteTree(scheme); e != nil {
				return e
			}
		}
		return nil
	}
	exe, e := engineExecutable()
	if e != nil {
		return e
	}
	// The registered command must be the headless engine: it owns the profile
	// store and forwards to the running instance. The native client ships it next
	// to the UI executable (scripts/build-native.ps1 step 3).
	for _, scheme := range []struct{ path, label string }{
		{schemeStandard, "URL:sing-box"},
		{schemeLegacy, "URL:sing-box-win"},
	} {
		if e = registerScheme(scheme.path, scheme.label, exe); e != nil {
			return e
		}
	}
	return nil
}

func registerScheme(path, label, exe string) error {
	k, _, e := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if e != nil {
		return e
	}
	defer k.Close()
	if e = k.SetStringValue("", label); e != nil {
		return e
	}
	if e = k.SetStringValue("URL Protocol", ""); e != nil {
		return e
	}
	// Friendly name shown by the Windows "How do you want to open this?" dialog.
	_ = k.SetStringValue("FriendlyTypeName", label)
	c, _, e := registry.CreateKey(registry.CURRENT_USER, path+`\shell\open\command`, registry.SET_VALUE)
	if e != nil {
		return e
	}
	defer c.Close()
	return c.SetStringValue("", `"`+exe+`" "%1"`)
}

// RegisterFileAssociations registers or removes the .bpf default handler and
// the .json "Open with" entry. Both point at the same command as the schemes.
func RegisterFileAssociations(on bool) error {
	if !on {
		return removeFileAssociations()
	}
	exe, e := engineExecutable()
	if e != nil {
		return e
	}
	if e = rememberState(); e != nil {
		return e
	}
	// ProgID describes the file type and how to open it.
	k, _, e := registry.CreateKey(registry.CURRENT_USER, progID, registry.SET_VALUE)
	if e != nil {
		return e
	}
	if e = k.SetStringValue("", "sing-box Profile"); e != nil {
		k.Close()
		return e
	}
	_ = k.SetStringValue("FriendlyTypeName", "sing-box 配置文件")
	k.Close()
	c, _, e := registry.CreateKey(registry.CURRENT_USER, progID+`\shell\open\command`, registry.SET_VALUE)
	if e != nil {
		return e
	}
	if e = c.SetStringValue("", `"`+exe+`" "%1"`); e != nil {
		c.Close()
		return e
	}
	c.Close()
	if e = writeDefault(extBPF, progIDName); e != nil {
		return e
	}
	// .json: add only to OpenWithProgids so the default handler is untouched.
	o, _, e := registry.CreateKey(registry.CURRENT_USER, extJSONOpen, registry.SET_VALUE)
	if e != nil {
		return e
	}
	defer o.Close()
	return o.SetStringValue(progIDName, "")
}

// rememberState snapshots what the registry looked like before the first
// registration of this installation, so removeFileAssociations can undo exactly
// our changes. Re-registering (toggling the setting on twice) must not overwrite
// the original snapshot with values we wrote ourselves.
func rememberState() error {
	b, _, e := registry.CreateKey(registry.CURRENT_USER, backupKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if e != nil {
		return e
	}
	defer b.Close()
	if _, _, err := b.GetIntegerValue(valueRecorded); err == nil {
		return nil
	}
	// .bpf: remember the key's existence and the handler it pointed at. An empty
	// value means there was no handler; existing == progIDName means a previous
	// registration was never cleaned up.
	if _, err := registry.OpenKey(registry.CURRENT_USER, extBPF, registry.READ); err == nil {
		if e = b.SetDWordValue(valueBPFKeyHad, 1); e != nil {
			return e
		}
	} else if e = b.SetDWordValue(valueBPFKeyHad, 0); e != nil {
		return e
	}
	existing := ""
	if k, err := registry.OpenKey(registry.CURRENT_USER, extBPF, registry.QUERY_VALUE); err == nil {
		existing, _, _ = k.GetStringValue("")
		k.Close()
	}
	if existing != "" && existing != progIDName {
		if e = b.SetStringValue(valueBPFDefault, existing); e != nil {
			return e
		}
		if e = b.SetDWordValue(valueBPFHad, 1); e != nil {
			return e
		}
	} else if e = b.SetDWordValue(valueBPFHad, 0); e != nil {
		return e
	}
	// .json: remember only whether the key existed. Our value there is always ours
	// to delete, because it is literally our ProgID name.
	if _, err := registry.OpenKey(registry.CURRENT_USER, extJSONOpen, registry.READ); err == nil {
		if e = b.SetDWordValue(valueJSONKeyHad, 1); e != nil {
			return e
		}
	} else if e = b.SetDWordValue(valueJSONKeyHad, 0); e != nil {
		return e
	}
	return b.SetDWordValue(valueRecorded, 1)
}

func removeFileAssociations() error {
	// Defaults apply when there is no snapshot at all (e.g. the user deleted it):
	// assume the keys are not ours to delete and only strip our own values.
	jsonKeyHad := uint64(1)
	bpfHad, bpfKeyHad := uint64(0), uint64(1)
	previousBPF := ""
	b, e := registry.OpenKey(registry.CURRENT_USER, backupKey, registry.QUERY_VALUE)
	if e == nil {
		bpfHad, _, _ = b.GetIntegerValue(valueBPFHad)
		previousBPF, _, _ = b.GetStringValue(valueBPFDefault)
		bpfKeyHad, _, _ = b.GetIntegerValue(valueBPFKeyHad)
		jsonKeyHad, _, _ = b.GetIntegerValue(valueJSONKeyHad)
		b.Close()
	} else if !errors.Is(e, registry.ErrNotExist) {
		return e
	}
	if e = unregisterJSONExtension(jsonKeyHad); e != nil {
		return e
	}
	if e = unregisterBPFExtension(bpfHad, bpfKeyHad, previousBPF); e != nil {
		return e
	}
	if e = deleteTree(progID); e != nil {
		return e
	}
	return deleteTree(backupKey)
}

// unregisterJSONExtension removes only the value this client added. Other
// applications' "Open with" entries, and the key itself, are left as they were.
func unregisterJSONExtension(keyExisted uint64) error {
	o, e := registry.OpenKey(registry.CURRENT_USER, extJSONOpen, registry.SET_VALUE|registry.QUERY_VALUE)
	if errors.Is(e, registry.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	if e = o.DeleteValue(progIDName); e != nil && !errors.Is(e, registry.ErrNotExist) {
		o.Close()
		return e
	}
	if keyExisted != 0 {
		return o.Close()
	}
	// We created the key: drop it if nothing else has been added meanwhile.
	values, err := o.ReadValueNames(-1)
	if err == nil {
		var subkeys []string
		subkeys, err = o.ReadSubKeyNames(-1)
		values = append(values, subkeys...)
	}
	o.Close()
	if err != nil {
		return err
	}
	if len(values) == 0 {
		if err = registry.DeleteKey(registry.CURRENT_USER, extJSONOpen); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
	}
	return nil
}

// unregisterBPFExtension restores whatever handler .bpf pointed at before we
// claimed it, and removes the key only if we created it and it is now empty.
func unregisterBPFExtension(hadDefault, keyExisted uint64, previous string) error {
	if hadDefault != 0 && previous != "" {
		return writeDefault(extBPF, previous)
	}
	if e := clearDefaultIfOurs(); e != nil {
		return e
	}
	if keyExisted != 0 {
		return nil
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, extBPF, registry.READ)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	values, _ := k.ReadValueNames(-1)
	subkeys, _ := k.ReadSubKeyNames(-1)
	k.Close()
	if len(values) > 0 || len(subkeys) > 0 {
		return nil
	}
	if err = registry.DeleteKey(registry.CURRENT_USER, extBPF); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

func clearDefaultIfOurs() error {
	k, e := registry.OpenKey(registry.CURRENT_USER, extBPF, registry.QUERY_VALUE|registry.SET_VALUE)
	if errors.Is(e, registry.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	defer k.Close()
	if current, _, err := k.GetStringValue(""); err == nil && current == progIDName {
		if err = k.DeleteValue(""); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
	}
	return nil
}

func writeDefault(path, value string) error {
	k, _, e := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if e != nil {
		return e
	}
	defer k.Close()
	return k.SetStringValue("", value)
}

// deleteTree removes a key and its children in leaf-first order. Registry keys
// must be empty before deletion, and x/sys has no recursive delete.
func deleteTree(path string) error {
	k, e := registry.OpenKey(registry.CURRENT_USER, path, registry.READ)
	if errors.Is(e, registry.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	subkeys, _ := k.ReadSubKeyNames(-1)
	values, _ := k.ReadValueNames(-1)
	k.Close()
	for _, sub := range subkeys {
		if e = deleteTree(path + `\` + sub); e != nil {
			return e
		}
	}
	// A key with values can still be opened for deletion; wipe leftover values
	// so the final DeleteKey does not fail on a partially removed tree.
	if len(values) > 0 {
		if w, err := registry.OpenKey(registry.CURRENT_USER, path, registry.SET_VALUE); err == nil {
			for _, name := range values {
				_ = w.DeleteValue(name)
			}
			w.Close()
		}
	}
	if e = registry.DeleteKey(registry.CURRENT_USER, path); e != nil && !errors.Is(e, registry.ErrNotExist) {
		return e
	}
	return nil
}

// HandlerPaths lists the registry keys this package may create; the validation
// script and the docs use it as the single source of truth.
func HandlerPaths() []string {
	return []string{schemeStandard, schemeLegacy, progID, extBPF, extJSONOpen, backupKey}
}

// engineExecutable resolves the engine binary that should own the association
// command. It prefers the sibling sing-box-win.exe so registering from the
// native UI, or from an engine unpacked elsewhere, still points at the process
// that actually owns the profile store.
func engineExecutable() (string, error) {
	exe, e := os.Executable()
	if e != nil {
		return "", e
	}
	candidate := filepath.Join(filepath.Dir(exe), "sing-box-win.exe")
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	return exe, nil
}
