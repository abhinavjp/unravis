package blackbox

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestReadKeyFormatAndUniqueness(t *testing.T) {
	re := regexp.MustCompile(`^ubk-([a-z2-7]{4}-){7}[a-z2-7]{4}$`)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		k, err := NewReadKey()
		if err != nil {
			t.Fatal(err)
		}
		if !re.MatchString(k) {
			t.Fatalf("format: %s", k)
		}
		if seen[k] {
			t.Fatal("duplicate key")
		}
		seen[k] = true
	}
}

func TestReadKeyNormalizedCheck(t *testing.T) {
	k, _ := NewReadKey()
	h, ok := HashKey(k)
	if !ok {
		t.Fatal("hash")
	}
	kc, err := NewKeyCheck(h)
	if err != nil {
		t.Fatal(err)
	}
	if !kc.Check(k) {
		t.Fatal("exact key refused")
	}
	// Typed with other case, spaces instead of dashes, and trailing whitespace.
	typed := strings.ToUpper(strings.ReplaceAll(k, "-", " ")) + "  \n"
	if !kc.Check(typed) {
		t.Fatalf("typed form refused: %q", typed)
	}
	other, _ := NewReadKey()
	for _, bad := range []string{"", "ubk-", other, k[:len(k)-1], k + "a", strings.Replace(k, "ubk", "xyz", 1)} {
		if kc.Check(bad) {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestNewKeyCheckRejectsBadHash(t *testing.T) {
	for _, h := range []string{"", "zz", strings.Repeat("a", 63)} {
		if _, err := NewKeyCheck(h); err == nil {
			t.Fatalf("accepted %q", h)
		}
	}
}

func TestSaveKeyHashStoresOnlyTheHash(t *testing.T) {
	k, _ := NewReadKey()
	p := filepath.Join(t.TempDir(), "readkey.sha256")
	if err := SaveKeyHash(p, k); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), k) || strings.Contains(strings.ToUpper(string(b)), "UBK") {
		t.Fatal("the key itself was stored")
	}
	kc, err := LoadKeyCheck(p)
	if err != nil || !kc.Check(k) {
		t.Fatalf("reload: %v", err)
	}
	if err := SaveKeyHash(p, "nonsense"); err == nil {
		t.Fatal("saved a non-key")
	}
}
