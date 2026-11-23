package blackbox

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSQLProfileDefaultsToStandard(t *testing.T) {
	var c Config
	if c.SQLProfileName() != SQLProfileStandard {
		t.Fatal(c.SQLProfileName())
	}
	c.SQL = &SQLSettings{}
	if c.SQLProfileName() != SQLProfileStandard {
		t.Fatal("an empty profile is standard")
	}
	c.SQL.Profile = SQLProfileDeep
	if c.SQLProfileName() != SQLProfileDeep {
		t.Fatal(c.SQLProfileName())
	}
}

func TestSQLSettingsValidation(t *testing.T) {
	ok := []SQLSettings{{}, {Profile: "standard"}, {Profile: "everything", Extras: []string{"locks", "topQueries"}}, {Profile: "deep"}}
	for _, s := range ok {
		if err := s.validate(); err != nil {
			t.Errorf("%+v: %v", s, err)
		}
	}
	bad := map[string]SQLSettings{
		"profile":   {Profile: "all"},
		"shape":     {Extras: []string{"Locks"}},
		"injection": {Extras: []string{"a;b"}},
		"space":     {Extras: []string{"a b"}},
		"long":      {Extras: []string{"a" + strings.Repeat("b", 32)}},
		"twice":     {Extras: []string{"locks", "locks"}},
		"empty":     {Extras: []string{""}},
	}
	many := SQLSettings{}
	for i := 0; i < maxSQLExtras+1; i++ {
		many.Extras = append(many.Extras, "x"+strings.Repeat("a", i%10)+string(rune('a'+i%26))+strings.Repeat("b", i/26))
	}
	bad["many"] = many
	for name, s := range bad {
		if err := s.validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestSQLBlockIsLeftOutWhenAbsentAndKeptWhenSet(t *testing.T) {
	raw, _ := json.Marshal(Config{Name: "a"})
	if strings.Contains(string(raw), `"sql"`) {
		t.Fatalf("an absent block must not be written: %s", raw)
	}
	raw, _ = json.Marshal(Config{Name: "a", SQL: &SQLSettings{Profile: "everything", Extras: []string{"locks"}}})
	if !strings.Contains(string(raw), `"sql":{"profile":"everything","extras":["locks"]}`) {
		t.Fatalf("%s", raw)
	}
}

func TestSQLBlockCarriesNoServerOrSecretField(t *testing.T) {
	// The settings file is copied into packs: the block must never grow a place for a server, login or password.
	raw, _ := json.Marshal(SQLSettings{Profile: "deep", Extras: []string{"a"}})
	for _, bad := range []string{"server", "user", "login", "password", "connection"} {
		if strings.Contains(strings.ToLower(string(raw)), bad) {
			t.Fatalf("%s in %s", bad, raw)
		}
	}
}
