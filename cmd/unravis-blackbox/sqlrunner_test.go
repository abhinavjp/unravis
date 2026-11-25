package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"api-audit-explorer/internal/bbsql"
	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/sealedfile"
)

// C13 wiring: the SQL service must carry the owner's disk bounds, because host
// housekeeping never looks inside the \sql subfolder. These are the numbers a
// real install would enforce; the enforcement itself (a full disk, a folder
// over its cap) needs the owner's server.
func TestSQLDiskBounds(t *testing.T) {
	minFree, keepHours, maxBytes := sqlDiskBounds(blackbox.Config{
		KeepDays: 7, MaxGB: 2, MinFreePercent: 10,
	})
	if minFree != 10 {
		t.Errorf("free-space floor = %d, want the owner's 10", minFree)
	}
	if keepHours != 168 {
		t.Errorf("retention = %d hours, want 7 days in hours", keepHours)
	}
	if want := int64((2 << 30) / sqlBudgetShare); maxBytes != want {
		t.Errorf("byte cap = %d, want %d (maxGB / %d)", maxBytes, want, sqlBudgetShare)
	}

	// The smallest settings the validator accepts must still bound the folder.
	_, keepHours, maxBytes = sqlDiskBounds(blackbox.Config{
		KeepDays: 1, MaxGB: 0.1, MinFreePercent: 1,
	})
	if keepHours != 24 {
		t.Errorf("keepDays 1 -> %d hours", keepHours)
	}
	if maxBytes <= 0 {
		t.Errorf("byte cap = %d, want a positive bound at the smallest config", maxBytes)
	}
}

// sqlFreePercent answers for a real folder: a reading, or an error the runner
// treats as "no room". A negative or above-100 answer would silently disable
// the floor, so it is bounded here.
func TestSQLFreePercentRange(t *testing.T) {
	free, err := sqlFreePercent(t.TempDir())
	if err != nil {
		t.Skipf("this platform cannot read volume free space: %v", err)
	}
	if free < 0 || free > 100 {
		t.Errorf("free percent = %v, want 0..100", free)
	}
}

type xorSeal struct{}

func (xorSeal) Protect(b []byte) ([]byte, error) {
	o := make([]byte, len(b))
	for i := range b {
		o[i] = b[i] ^ 0x5a
	}
	return o, nil
}
func (x xorSeal) Unprotect(b []byte) ([]byte, error) { return x.Protect(b) }

func xorProt() (sealedfile.Protector, error) { return xorSeal{}, nil }

func TestSQLConnForUsesStoredLoginOnlyWhenSaved(t *testing.T) {
	dir := t.TempDir()
	c, err := sqlConnFor(dir, blackbox.Probe{}, xorProt)
	if err != nil || c.Login != nil || c.Server != sqlLocalServer {
		t.Fatalf("no login saved: %+v %v", c, err)
	}
	s := bbsql.StoredLogin{Server: "sql01\\PROD", Login: bbsql.Login{User: "u", Password: "p"}, TrustServerCertificate: true}
	if err := bbsql.SaveLogin(dir, s, xorSeal{}); err != nil {
		t.Fatal(err)
	}
	probe := blackbox.Probe{ServerCertificate: filepath.Join(dir, "c.pem")}
	c, err = sqlConnFor(dir, probe, xorProt)
	if err != nil || c.Login == nil || c.Server != "sql01\\PROD" || !c.TrustServerCertificate || c.ServerCertificate != probe.ServerCertificate {
		t.Fatalf("saved login: %v", err)
	}
}

func TestSQLConnForNeverFallsBackWhenTheLoginCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, bbsql.LoginFile), []byte("damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlConnFor(dir, blackbox.Probe{}, xorProt); err == nil {
		t.Fatal("a damaged login fell back to Windows sign-in")
	}
	if _, err := sqlConnFor(dir, blackbox.Probe{}, func() (sealedfile.Protector, error) { return nil, os.ErrInvalid }); err == nil {
		t.Fatal("a missing sealer fell back to Windows sign-in")
	}
}

func TestSQLEnabledExtras(t *testing.T) {
	if got := sqlEnabledExtras(blackbox.Config{}); len(got) != 0 {
		t.Fatalf("standard turned on %v", got)
	}
	everything := sqlEnabledExtras(blackbox.Config{SQL: &blackbox.SQLSettings{Profile: blackbox.SQLProfileEverything}})
	if want := bbsql.ExtrasForProfile(bbsql.ProfileEverything); !slices.Equal(everything, want) {
		t.Fatalf("everything = %v, want %v", everything, want)
	}
	// An unknown profile turns on nothing; names in sql.extras are added once, sorted.
	got := sqlEnabledExtras(blackbox.Config{SQL: &blackbox.SQLSettings{Profile: "nope", Extras: []string{"zz", "aa", "zz"}}})
	if !slices.Equal(got, []string{"aa", "zz"}) {
		t.Fatalf("extras = %v", got)
	}
	if len(sqlEnabledExtras(blackbox.Config{SQL: &blackbox.SQLSettings{Profile: blackbox.SQLProfileDeep}})) < len(everything) {
		t.Fatal("deep must hold at least what everything holds")
	}
}

// A login "file" that cannot be read as a file (here a folder) is an error, never "no login".
func TestSQLConnForRefusesALoginPathThatIsNotAFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, bbsql.LoginFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlConnFor(dir, blackbox.Probe{}, xorProt); err == nil {
		t.Fatal("an unreadable login path fell back to Windows sign-in")
	}
}

// Query text is on only for the deep profile with the flag set; every other config leaves the cap at 0, which keeps the sampler unbuilt.
func TestSQLQueryTextCharsIsOnlyForDeepWithTheFlag(t *testing.T) {
	for name, c := range map[string]blackbox.Config{
		"no sql":           {},
		"flag, standard":   {SQL: &blackbox.SQLSettings{QueryText: true}},
		"flag, everything": {SQL: &blackbox.SQLSettings{Profile: blackbox.SQLProfileEverything, QueryText: true}},
		"deep, no flag":    {SQL: &blackbox.SQLSettings{Profile: blackbox.SQLProfileDeep}},
	} {
		if n := sqlQueryTextChars(c); n != 0 {
			t.Errorf("%s: cap %d, want 0", name, n)
		}
	}
	if n := sqlQueryTextChars(blackbox.Config{SQL: &blackbox.SQLSettings{Profile: blackbox.SQLProfileDeep, QueryText: true, QueryTextMaxChars: 700}}); n != 700 {
		t.Errorf("deep with the flag: cap %d, want 700", n)
	}
	if n := sqlQueryTextChars(blackbox.Config{SQL: &blackbox.SQLSettings{Profile: blackbox.SQLProfileDeep, QueryText: true}}); n <= 0 {
		t.Errorf("deep with the flag and no cap: %d, want the default", n)
	}
}
