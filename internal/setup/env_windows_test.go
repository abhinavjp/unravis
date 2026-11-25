//go:build windows

package setup

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// scratchEnv is an osEnv whose PATH lives in a throwaway key under HKCU, so the real registry code runs and the PATH of this PC is not
// touched. initial is the PATH it starts with ("" and typ 0: no PATH value yet).
func scratchEnv(t *testing.T, initial string, typ uint32) (osEnv, registry.Key) {
	t.Helper()
	const base = `Software\UnravisSetupTest\Env-`
	path := base + strings.ReplaceAll(t.Name(), "/", "-")
	_ = registry.DeleteKey(registry.CURRENT_USER, path)
	k, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.ALL_ACCESS)
	if err != nil {
		t.Skipf("no scratch registry key: %v", err)
	}
	t.Cleanup(func() {
		k.Close()
		_ = registry.DeleteKey(registry.CURRENT_USER, path)
	})
	switch typ {
	case registry.SZ:
		_ = k.SetStringValue(EnvPathName, initial)
	case registry.EXPAND_SZ:
		_ = k.SetExpandStringValue(EnvPathName, initial)
	}
	return osEnv{where: func(Root) (registry.Key, string) { return registry.CURRENT_USER, path }}, k
}

func readScratchPath(t *testing.T, k registry.Key) (string, uint32) {
	t.Helper()
	v, typ, err := k.GetStringValue(EnvPathName)
	if err != nil {
		t.Fatalf("read PATH: %v", err)
	}
	return v, typ
}

const scratchEntry = `C:\Program Files\Unravis\bin`

func TestOSEnvAddKeepsOthersAndType(t *testing.T) {
	env, k := scratchEnv(t, `%SystemRoot%\system32;C:\Tools`, registry.EXPAND_SZ)
	added, err := env.AddPathEntry(HKCU, scratchEntry)
	if err != nil || !added {
		t.Fatalf("add: %v %v", added, err)
	}
	v, typ := readScratchPath(t, k)
	if v != `%SystemRoot%\system32;C:\Tools;`+scratchEntry || typ != registry.EXPAND_SZ {
		t.Fatalf("PATH %q type %d", v, typ)
	}
	if has, err := env.HasPathEntry(HKCU, scratchEntry); err != nil || !has {
		t.Fatalf("has: %v %v", has, err)
	}
}

func TestOSEnvAddTwiceIsOneEntry(t *testing.T) {
	env, k := scratchEnv(t, `C:\Tools`, registry.SZ)
	if added, _ := env.AddPathEntry(HKCU, scratchEntry); !added {
		t.Fatal("first add should add")
	}
	if added, err := env.AddPathEntry(HKCU, strings.ToLower(scratchEntry)+`\`); err != nil || added {
		t.Fatalf("second add (other case, trailing slash): %v %v", added, err)
	}
	v, typ := readScratchPath(t, k)
	if strings.Count(strings.ToLower(v), strings.ToLower(scratchEntry)) != 1 || typ != registry.SZ {
		t.Fatalf("PATH %q type %d", v, typ)
	}
}

func TestOSEnvRemoveTakesOnlyOurEntry(t *testing.T) {
	env, k := scratchEnv(t, `C:\Tools;`+scratchEntry+`;%SystemRoot%`, registry.EXPAND_SZ)
	removed, err := env.RemovePathEntry(HKCU, scratchEntry)
	if err != nil || !removed {
		t.Fatalf("remove: %v %v", removed, err)
	}
	v, _ := readScratchPath(t, k)
	if v != `C:\Tools;%SystemRoot%` {
		t.Fatalf("PATH %q", v)
	}
	if removed, _ := env.RemovePathEntry(HKCU, scratchEntry); removed {
		t.Fatal("a second remove found the entry again")
	}
}

func TestOSEnvMissingKeyAndValue(t *testing.T) {
	env := osEnv{where: func(Root) (registry.Key, string) {
		return registry.CURRENT_USER, `Software\UnravisSetupTest\no-such-key`
	}}
	if has, err := env.HasPathEntry(HKCU, scratchEntry); err != nil || has {
		t.Fatalf("has on a missing key: %v %v", has, err)
	}
	if removed, err := env.RemovePathEntry(HKCU, scratchEntry); err != nil || removed {
		t.Fatalf("remove on a missing key: %v %v", removed, err)
	}
	// A key with no PATH value: add creates it and reads as expandable.
	env2, k := scratchEnv(t, "", 0)
	if added, err := env2.AddPathEntry(HKCU, scratchEntry); err != nil || !added {
		t.Fatalf("add with no PATH value: %v %v", added, err)
	}
	if v, typ := readScratchPath(t, k); v != scratchEntry || typ != registry.EXPAND_SZ {
		t.Fatalf("PATH %q type %d", v, typ)
	}
}

func TestOSEnvRefusesAPathThatIsNotText(t *testing.T) {
	env, k := scratchEnv(t, "", 0)
	if err := k.SetDWordValue(EnvPathName, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := env.AddPathEntry(HKCU, scratchEntry); err == nil {
		t.Fatal("a PATH that is a number was overwritten")
	}
	if n, _, err := k.GetIntegerValue(EnvPathName); err != nil || n != 7 {
		t.Fatalf("the value changed: %d %v", n, err)
	}
}

func TestOSEnvTooLongIsRefusedAndNothingWritten(t *testing.T) {
	long := strings.Repeat(`C:\a;`, MaxPathValue/5-2)
	env, k := scratchEnv(t, long, registry.SZ)
	if _, err := env.AddPathEntry(HKCU, `C:\`+strings.Repeat("b", 300)); err == nil {
		t.Fatal("an over-long PATH was written")
	}
	if v, _ := readScratchPath(t, k); v != long {
		t.Fatal("the PATH changed on a refusal")
	}
}
