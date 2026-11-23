package secretscrub

import (
	"strings"
	"testing"
	"time"
)

func TestMaskFlags(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"long option", `mysql -u root --password hunter2 -h db`, `mysql -u root --password [hidden] -h db`},
		{"api key and token", `app.exe --api-key hunter2 --token zz9 --verbose`, `app.exe --api-key [hidden] --token [hidden] --verbose`},
		{"slash option", `app.exe /password hunter2 /p keep`, `app.exe /password [hidden] /p keep`},
		{"quoted value with a space", `app --password "hun ter2" --x`, `app --password "[hidden]" --x`},
		{"single quoted", `app --secret 'a b' --x`, `app --secret '[hidden]' --x`},
		{"quote never closed", `app --password "a b c`, `app --password "[hidden]`},
		{"sqlcmd -P", `sqlcmd -S srv -U sa -P hunter2 -Q "select 1"`, `sqlcmd -S srv -U sa -P [hidden] -Q "select 1"`},
		{"sqlcmd by path", `C:\Program Files\x\SQLCMD.EXE -P "hun ter2" -d x`, `C:\Program Files\x\SQLCMD.EXE -P "[hidden]" -d x`},
		{"mysql attached", `mysql -phunter2 -h db`, `mysql -p[hidden] -h db`},
		{"mysql attached, quoted", `mysql -p"hunter2" -h db`, `mysql -p[hidden]" -h db`},
		{"redis", `redis-cli -a hunter2 -h x`, `redis-cli -a [hidden] -h x`},
		{"mongo", `mongosh -u a -p hunter2 host`, `mongosh -u a -p [hidden] host`},
		{"curl user", `curl -u bob:hunter2 https://x`, `curl -u bob:[hidden] https://x`},
		{"curl long user", `curl --user bob:hunter2 https://x`, `curl --user bob:[hidden] https://x`},
		{"tool on the first line only", "x --password a\nsqlcmd\nnext -P keep", "x --password [hidden]\nsqlcmd\nnext -P keep"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Mask(tc.in, rm); got != tc.want {
				t.Fatalf("\n in: %s\ngot: %s\nwant: %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestMaskFlagsLeavesOrdinaryCommands(t *testing.T) {
	for _, in := range []string{
		`psql -p 5432 -h x`,
		`mysql -p -h db`,
		`app --password --verbose`,
		`app --password`,
		`psql --no-password dbname`,
		`ls -P x --skip-token y`,
		`app --password-file /etc/p --token-file /etc/t`,
		`git commit -m "fix: password reset" --amend`,
		`a - password b -- token`,
		`tar -xzf a.tgz -C /tmp`,
		`curl -u bob https://x`,
		`curl -s https://x/a:b`,
		`mongosh -p`,
		`https://example.com/a/-password/x`,
	} {
		if got := Mask(in, rm); got != in {
			t.Errorf("ordinary command changed:\n in: %s\ngot: %s", in, got)
		}
	}
}

func TestMaskFlagsHostileInputIsFast(t *testing.T) {
	for name, in := range map[string]string{
		"many options":       strings.Repeat("--password ", 200000),
		"many short options": "sqlcmd " + strings.Repeat("-P ", 400000),
		"open quotes":        strings.Repeat(`--token "a `, 100000),
		"tool names":         strings.Repeat("mysql ", 300000) + "-pX",
		"long word":          "--password " + strings.Repeat("a", 4<<20),
		"slashes":            strings.Repeat("/a", 1<<20),
	} {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			_ = Mask(in, rm)
			if d := time.Since(start); d > 3*time.Second {
				t.Fatalf("%s: %s for %d bytes", name, d, len(in))
			}
		})
	}
}

func TestMaskFlagsLeavesPathsAndJSON(t *testing.T) {
	for _, in := range []string{
		`<PasswordPolicy>8</PasswordPolicy> /usr/lib/password-store/foo.go {"pass_count": 3}`,
		`/etc/token/x.conf loaded`,
		`C:/secrets/password.txt read`,
		`see /password/reset page`,
	} {
		if got := Mask(in, rm); got != in {
			t.Errorf("changed:\n in: %s\ngot: %s", in, got)
		}
	}
}

func TestMaskFlagsMySQLAttachedWithMarks(t *testing.T) {
	in := `mysql -pa:b=c -h db`
	if got, want := Mask(in, rm), `mysql -p[hidden] -h db`; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestMaskFlagsRandomInputNeverPanics(t *testing.T) {
	pieces := []string{"--password", "-P", "-p", "-pX", "-a", "-u", "--user", "/token", "--api-key", "sqlcmd", "mysql", "redis-cli", "mongosh", "curl", "x", "a:b", `"`, "'", " ", "\t", "\n", "=", ":", "-", "--", "/", `\`, "(", "--no-password"}
	seed := uint32(777)
	next := func(n int) int { seed = seed*1664525 + 1013904223; return int(seed>>8) % n }
	for round := 0; round < 4000; round++ {
		var b strings.Builder
		for k := 0; k < 3+next(40); k++ {
			b.WriteString(pieces[next(len(pieces))])
			if next(3) > 0 {
				b.WriteByte(' ')
			}
		}
		in := b.String()
		out := Mask(in, rm)
		if len(out) > len(in)+len(rm)*(len(in)/2+1) {
			t.Fatalf("output grew too much for %q: %q", in, out)
		}
	}
}
