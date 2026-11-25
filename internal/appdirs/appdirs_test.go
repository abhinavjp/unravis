package appdirs

import (
	"path/filepath"
	"testing"
)

func TestRunDir(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for _, c := range []struct {
		name string
		env  map[string]string
		want string
		bad  bool
	}{
		{"override", map[string]string{"UNRAVIS_RUN_DIR": "/r", "AUDIT_CONFIG_DIR": "/c", "LOCALAPPDATA": "/l"}, "/r", false},
		{"config dir", map[string]string{"AUDIT_CONFIG_DIR": "/c", "LOCALAPPDATA": "/l"}, filepath.Join("/c", "run"), false},
		{"local app data", map[string]string{"LOCALAPPDATA": "/l"}, filepath.Join("/l", "Unravis", "run"), false},
		{"nothing", map[string]string{}, "", true},
	} {
		got, err := RunDir(env(c.env))
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("%s: got %q, %v", c.name, got, err)
		}
	}
	if _, err := RunDir(nil); err == nil {
		t.Error("nil getenv accepted")
	}
}

func TestMCPDir(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for _, c := range []struct {
		name string
		env  map[string]string
		want string
		bad  bool
	}{
		{"override", map[string]string{"UNRAVIS_MCP_DIR": "/m", "AUDIT_CONFIG_DIR": "/c", "LOCALAPPDATA": "/l"}, "/m", false},
		{"config dir", map[string]string{"AUDIT_CONFIG_DIR": "/c", "LOCALAPPDATA": "/l"}, filepath.Join("/c", "mcp"), false},
		{"local app data", map[string]string{"LOCALAPPDATA": "/l"}, filepath.Join("/l", "Unravis", "mcp"), false},
		{"nothing", map[string]string{}, "", true},
	} {
		got, err := MCPDir(env(c.env))
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("%s: got %q, %v", c.name, got, err)
		}
	}
	if _, err := MCPDir(nil); err == nil {
		t.Error("nil getenv accepted")
	}
}

func TestCrashDir(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for _, c := range []struct {
		name string
		env  map[string]string
		want string
		bad  bool
	}{
		{"override", map[string]string{"UNRAVIS_CRASH_DIR": "/x", "AUDIT_CONFIG_DIR": "/c", "LOCALAPPDATA": "/l"}, "/x", false},
		{"config dir", map[string]string{"AUDIT_CONFIG_DIR": "/c", "LOCALAPPDATA": "/l"}, filepath.Join("/c", "crashes"), false},
		{"local app data", map[string]string{"LOCALAPPDATA": "/l"}, filepath.Join("/l", "Unravis", "crashes"), false},
		{"nothing", map[string]string{}, "", true},
	} {
		got, err := CrashDir(env(c.env))
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("%s: got %q, %v", c.name, got, err)
		}
	}
	if _, err := CrashDir(nil); err == nil {
		t.Error("nil getenv accepted")
	}
}
