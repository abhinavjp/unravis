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
		{"mysql attached, quoted", `mysql -p"hunter2" -h db`, `mysql -p"[hidden]" -h db`},
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
		"json arrays":        strings.Repeat(`["--password","`, 200000),
		"tags":               strings.Repeat("<Arg>--token ", 200000),
		"secure strings":     strings.Repeat("ConvertTo-SecureString '", 200000),
		"pipes":              strings.Repeat("a|sqlcmd -P ", 200000),
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
	pieces := []string{"--password", "-P", "-p", "-pX", "-a", "-u", "--user", "/token", "--api-key", "sqlcmd", "mysql", "redis-cli", "mongosh", "curl", "x", "a:b", `"`, "'", " ", "\t", "\n", "=", ":", "-", "--", "/", `\`, "(", "--no-password", "[", "]", "{", "}", ",", "<Arg>", "</Arg>", "ConvertTo-SecureString", "-AsPlainText", "|", "&&", ";", "-sSu", "--user=a:b", "/RP", "schtasks", "-w", "ldapsearch", "(", ")", "$true"}
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

// Review of PR 208, round 1.
func TestMaskFlagsReviewFindings(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"one dash, long name (PowerShell, Go flag)", `Invoke-Sqlcmd -Server x -Password hunter2 -Query q`, `Invoke-Sqlcmd -Server x -Password [hidden] -Query q`},
		{"one dash token", `unravis.exe -token hunter2 -v`, `unravis.exe -token [hidden] -v`},
		{"sqlcmd attached", `sqlcmd -S x -Phunter2 -d db`, `sqlcmd -S x -P[hidden] -d db`},
		{"bcp attached", `bcp t out f -U sa -Phunter2`, `bcp t out f -U sa -P[hidden]`},
		{"redis attached", `redis-cli -ahunter2 -h x`, `redis-cli -a[hidden] -h x`},
		{"curl attached", `curl -ubob:hunter2 https://x`, `curl -ubob:[hidden] https://x`},
		{"mysql attached with a space in quotes", `mysql -p"hun ter2" -h db`, `mysql -p"[hidden]" -h db`},
		{"sqlcmd slash form", `sqlcmd /S srv /U sa /P hunter2 /d db`, `sqlcmd /S srv /U sa /P [hidden] /d db`},
		{"backslash continuation", "sqlcmd -S x \\\n  -P hunter2", "sqlcmd -S x \\\n  -P [hidden]"},
		{"powershell backtick continuation", "sqlcmd -S x `\n  -P hunter2", "sqlcmd -S x `\n  -P [hidden]"},
		{"cmd caret continuation", "sqlcmd -S x ^\r\n  -P hunter2", "sqlcmd -S x ^\r\n  -P [hidden]"},
		{"value on the line after a backslash", "app --password \\\n  hunter2 --x", "app --password \\\n  [hidden] --x"},
		{"tool in markdown code", "run `sqlcmd -S x -P hunter2` now", "run `sqlcmd -S x -P [hidden]` now"},
		{"tool in a subshell", `x=$(mysql -phunter2 -e q)`, `x=$(mysql -p[hidden] -e q)`},
		{"JSON argument array", `"Args": ["--password", "hunter2", "--x"]`, `"Args": ["--password", "[hidden]", "--x"]`},
		{"JSON argument array, no spaces", `["app","--token","hunter2"]`, `["app","--token","[hidden]"]`},
		{"YAML block argument list", "args:\n  - --password\n  - hunter2\n  - --x", "args:\n  - --password\n  - [hidden]\n  - --x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Mask(tc.in, rm); got != tc.want {
				t.Fatalf("\n in: %q\ngot: %q\nwant: %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestMaskFlagsReviewKeepsOrdinaryText(t *testing.T) {
	for _, in := range []string{
		`"POST /token HTTP/1.1" 200 12`,
		`GET /secret HTTP/2 200`,
		"mysql -e q; find . -perm 644",
		"curl https://x | grep -u foo:bar",
		"mysql -e q && ls -pa",
		"sqlcmd -S x\nfind -perm 644",
	} {
		if got := Mask(in, rm); got != in {
			t.Errorf("changed:\n in: %q\ngot: %q", in, got)
		}
	}
}

func TestMaskFlagsFailsClosedOverBudget(t *testing.T) {
	old := workFactor
	workFactor = 0
	defer func() { workFactor = old }()
	in := strings.Repeat("abc def\n", 20000) + "sqlcmd -S x -U sa -P hunter2\n"
	if out := Mask(in, rm); strings.Contains(out, "hunter2") {
		t.Fatal("over budget, a command line secret survived")
	}
}

// Review of PR 208, round 2.
func TestMaskFlagsReview2Findings(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"JSON array, no spaces", `"Args":["--password","hunter2"]`, `"Args":["--password","[hidden]"]`},
		{"JSON array, pretty printed", "\"Args\": [\n    \"--password\",\n    \"hunter2\",\n    \"--x\"\n]", "\"Args\": [\n    \"--password\",\n    \"[hidden]\",\n    \"--x\"\n]"},
		{"command line inside a JSON string", `{"cmd":"sqlcmd -P hunter2","pid":4}`, `{"cmd":"sqlcmd -P [hidden]","pid":4}`},
		{"unquoted value keeps the quote", `{"CommandLine":"app.exe --token abc","Pid":4}`, `{"CommandLine":"app.exe --token [hidden]","Pid":4}`},
		{"logfmt keeps the quote", `msg="app --token abc" level=info`, `msg="app --token [hidden]" level=info`},
		{"secure string in a parameter", `New-ADUser -AccountPassword (ConvertTo-SecureString "hunter2" -AsPlainText -Force) -Name x`, `New-ADUser -AccountPassword (ConvertTo-SecureString "[hidden]" -AsPlainText -Force) -Name x`},
		{"bare secure string", `$p = ConvertTo-SecureString 'hunter2' -AsPlainText -Force`, `$p = ConvertTo-SecureString '[hidden]' -AsPlainText -Force`},
		{"secure string -String", `ConvertTo-SecureString -String "hunter2" -AsPlainText`, `ConvertTo-SecureString -String "[hidden]" -AsPlainText`},
		{"keytool storepass", `keytool -importcert -storepass hunter2 -keypass k2`, `keytool -importcert -storepass [hidden] -keypass [hidden]`},
		{"openssl passin", `openssl rsa -in k -passin pass:hunter2`, `openssl rsa -in k -passin [hidden]`},
		{"cmdkey colon form", `cmdkey /add:srv /user:bob /pass:hunter2`, `cmdkey /add:srv /user:bob /pass:[hidden]`},
		{"redis --pass", `redis-cli --pass hunter2 -h x`, `redis-cli --pass [hidden] -h x`},
		{"sshpass", `sshpass -p hunter2 ssh bob@h`, `sshpass -p [hidden] ssh bob@h`},
		{"psexec", `psexec \\srv -u bob -p hunter2 cmd`, `psexec \\srv -u bob -p [hidden] cmd`},
		{"ldapsearch", `ldapsearch -x -D cn=a -w hunter2 -b dc=x`, `ldapsearch -x -D cn=a -w [hidden] -b dc=x`},
		{"schtasks", `schtasks /Create /RU bob /RP hunter2 /TN t`, `schtasks /Create /RU bob /RP [hidden] /TN t`},
		{"curl cluster", `curl -sSu bob:hunter2 https://x`, `curl -sSu bob:[hidden] https://x`},
		{"curl --user=", `curl --user=bob:hunter2 https://x`, `curl --user=bob:[hidden] https://x`},
		{"tool after a pipe, no spaces", `q|sqlcmd -P hunter2`, `q|sqlcmd -P [hidden]`},
		{"tool after &&", `a&&mysql -phunter2`, `a&&mysql -p[hidden]`},
		{"XML argument", `<Arg>--password hunter2</Arg>`, `<Arg>--password [hidden]</Arg>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Mask(tc.in, rm); got != tc.want {
				t.Fatalf("\n in: %q\ngot: %q\nwant: %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestMaskFlagsReview2KeepsProseAndParameters(t *testing.T) {
	for _, in := range []string{
		"See /token for details",
		"Use --password to set the login",
		"| --token | string | the API token |",
		"location /token {",
		"New-ADUser -TokenLifetime 3600 -PasswordNeverExpires $true -Name x",
		"vault --secret-store vault1",
		"app -Password $true",
		"app --password-policy strict -MinPasswordLength 12",
	} {
		if got := Mask(in, rm); got != in {
			t.Errorf("changed:\n in: %q\ngot: %q", in, got)
		}
	}
}
