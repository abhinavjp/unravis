package discover

import (
	"errors"
	"strings"
	"testing"
)

func TestValidLogPathTable(t *testing.T) {
	const cfg = `D:\Sites\web-a`
	long257 := `D:\` + strings.Repeat("a", 257) // exactly 260 units
	long258 := `D:\` + strings.Repeat("a", 258)
	cases := []struct {
		name   string
		raw    string
		cfgDir string
		want   string // cleaned path when accepted
		code   string // rule name when refused
		reason Reason
	}{
		// Accepted.
		{name: "plain", raw: `D:\Logs\web-a\u_ex010911.log`, want: `D:\Logs\web-a\u_ex010911.log`},
		{name: "forward slashes and drive case", raw: `d:/logs/web-a`, want: `D:\logs\web-a`},
		{name: "non-ASCII", raw: `D:\Журналы\web-a`, want: `D:\Журналы\web-a`},
		{name: "non-ASCII file", raw: `D:\Logs\журнал-2001.log`, want: `D:\Logs\журнал-2001.log`},
		{name: "relative to config dir", raw: `logs\app.log`, cfgDir: cfg, want: `D:\Sites\web-a\logs\app.log`},
		{name: "dot relative", raw: `.\logs\app.log`, cfgDir: cfg, want: `D:\Sites\web-a\logs\app.log`},
		{name: "dot segment and empty segment", raw: `D:\Logs\.\\web-a`, want: `D:\Logs\web-a`},
		{name: "drive root", raw: `D:\`, want: `D:\`},
		{name: "exactly 260", raw: long257, want: long257},
		{name: "spaces in name", raw: `D:\Program Files\Redis\redis.log`, want: `D:\Program Files\Redis\redis.log`},
		{name: "surrounding space trimmed", raw: `  D:\Logs\a.log `, want: `D:\Logs\a.log`},

		// Refused: shape (unsafe-path).
		{name: "UNC host", raw: `\\web-a.example.test\share\a.log`, code: "unc", reason: ReasonUnsafePath},
		{name: "UNC forward slashes", raw: `//192.0.2.7/share/a.log`, code: "unc", reason: ReasonUnsafePath},
		{name: "long path prefix", raw: `\\?\D:\Logs\a.log`, code: "prefix", reason: ReasonUnsafePath},
		{name: "long UNC prefix", raw: `\\?\UNC\web-a\share\a.log`, code: "prefix", reason: ReasonUnsafePath},
		{name: "device prefix", raw: `\\.\pipe\web-a`, code: "prefix", reason: ReasonUnsafePath},
		{name: "ADS on file", raw: `D:\Logs\a.log:hidden`, code: "ads", reason: ReasonUnsafePath},
		{name: "ADS on folder", raw: `D:\Logs:hidden\a.log`, code: "ads", reason: ReasonUnsafePath},
		{name: "8.3 folder", raw: `C:\PROGRA~1\Contoso\a.log`, code: "alias8.3", reason: ReasonUnsafePath},
		{name: "8.3 folder lower", raw: `C:\progra~2\Contoso\a.log`, code: "alias8.3", reason: ReasonUnsafePath},
		{name: "8.3 file with extension", raw: `D:\Logs\WEBLOG~1.TXT`, code: "alias8.3", reason: ReasonUnsafePath},
		{name: "dotdot", raw: `D:\Logs\..\Windows\a.log`, code: "dotdot", reason: ReasonUnsafePath},
		{name: "dotdot relative", raw: `..\other\a.log`, cfgDir: cfg, code: "dotdot", reason: ReasonUnsafePath},
		{name: "dotdot at end", raw: `D:\Logs\web-a\..`, code: "dotdot", reason: ReasonUnsafePath},
		{name: "relative, no config dir", raw: `logs\a.log`, code: "relative-no-dir", reason: ReasonUnsafePath},
		{name: "relative, relative config dir", raw: `a.log`, cfgDir: `Sites\web-a`, code: "relative-no-dir", reason: ReasonUnsafePath},
		{name: "rooted without drive", raw: `\Windows\a.log`, code: "relative", reason: ReasonUnsafePath},
		{name: "drive-relative", raw: `D:logs\a.log`, code: "relative", reason: ReasonUnsafePath},
		{name: "reserved name", raw: `D:\Logs\CON`, code: "reserved", reason: ReasonUnsafePath},
		{name: "reserved with extension", raw: `D:\Logs\nul.txt`, code: "reserved", reason: ReasonUnsafePath},
		{name: "trailing dot", raw: `D:\Logs\a.log.`, code: "dot-space", reason: ReasonUnsafePath},
		{name: "forbidden character", raw: `D:\Logs\a<b>.log`, code: "chars", reason: ReasonUnsafePath},
		{name: "wildcard", raw: `D:\Logs\*.log`, code: "chars", reason: ReasonUnsafePath},
		{name: "NUL byte", raw: "D:\\Logs\\a\x00.log", code: "chars", reason: ReasonUnsafePath},
		{name: "control character", raw: "D:\\Logs\\a\tb.log", code: "chars", reason: ReasonUnsafePath},
		{name: "bidi override", raw: "D:\\Logs\\\u202Egpj.log", code: "chars", reason: ReasonUnsafePath},
		{name: "261 units", raw: long258, code: "length", reason: ReasonUnsafePath},

		// Refused: content (config-unparsed).
		{name: "empty", raw: ``, code: "empty", reason: ReasonConfigUnparsed},
		{name: "blank", raw: "   ", code: "empty", reason: ReasonConfigUnparsed},
		{name: "equals sign", raw: `Server=192.0.2.5\Database=web-a`, code: "value-shape", reason: ReasonConfigUnparsed},
		{name: "semicolon", raw: `D:\Logs\a.log;x`, code: "value-shape", reason: ReasonConfigUnparsed},
		{name: "at sign", raw: `D:\Logs\ops@example.test\a.log`, code: "value-shape", reason: ReasonConfigUnparsed},
		{name: "url", raw: `https://web-a.example.test/log`, code: "value-shape", reason: ReasonConfigUnparsed},
		{name: "password word", raw: `D:\Logs\Password.log`, code: "secret-word", reason: ReasonConfigUnparsed},
		{name: "pwd word", raw: `D:\Logs\PWD\a.log`, code: "secret-word", reason: ReasonConfigUnparsed},
		{name: "secret word", raw: `D:\Logs\my-secret\a.log`, code: "secret-word", reason: ReasonConfigUnparsed},
		{name: "token word", raw: `D:\Logs\Token\a.log`, code: "secret-word", reason: ReasonConfigUnparsed},
		{name: "connectionstring word", raw: `D:\Logs\ConnectionString.txt`, code: "secret-word", reason: ReasonConfigUnparsed},
		{name: "user id word", raw: `D:\Logs\User Id\a.log`, code: "secret-word", reason: ReasonConfigUnparsed},
		{name: "accountkey word", raw: `D:\Logs\AccountKey\a.log`, code: "secret-word", reason: ReasonConfigUnparsed},
		{name: "marker with secret word", raw: `D:\Logs\SENTINEL-7f3a-password\a.log`, code: "secret-word", reason: ReasonConfigUnparsed},
		{name: "env variable", raw: `%windir%\Logs\a.log`, code: "layout-token", reason: ReasonConfigUnparsed},
		{name: "layout renderer", raw: `${basedir}\logs\a.log`, code: "layout-token", reason: ReasonConfigUnparsed},
		{name: "whitespace run", raw: `D:\Logs\a  b\a.log`, code: "whitespace-run", reason: ReasonConfigUnparsed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ValidLogPath(c.raw, c.cfgDir)
			if c.code == "" {
				if err != nil {
					t.Fatalf("refused (%v), want %q", err, c.want)
				}
				if got != c.want {
					t.Fatalf("got %q, want %q", got, c.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("accepted as %q, want refusal %q", got, c.code)
			}
			var pe *PathError
			if !errors.As(err, &pe) {
				t.Fatalf("error %T is not a PathError", err)
			}
			if pe.Code != c.code || pe.Reason != c.reason {
				t.Fatalf("code %q reason %q, want %q %q", pe.Code, pe.Reason, c.code, c.reason)
			}
			if got != "" {
				t.Fatalf("returned %q alongside an error", got)
			}
			if strings.Contains(err.Error(), "SENTINEL") || strings.Contains(err.Error(), `D:\`) {
				t.Fatalf("error text carries the value: %q", err.Error())
			}
			if ReasonFromError(err) != c.reason {
				t.Fatalf("ReasonFromError = %q", ReasonFromError(err))
			}
		})
	}
}

func TestValidateAbsPathSkipsContentRules(t *testing.T) {
	// Shape only: a folder called Password is fine for an FS call, not for a value
	// kept from a config.
	if _, err := ValidateAbsPath(`D:\Logs\Password\a.log`); err != nil {
		t.Fatalf("shape check refused a content word: %v", err)
	}
	if _, err := ValidateAbsPath(`logs\a.log`); err == nil {
		t.Fatal("relative path accepted")
	}
	if _, err := ValidateAbsPath(`C:\PROGRA~1`); err == nil {
		t.Fatal("8.3 alias accepted")
	}
}

func TestCanonKeyIgnoresCase(t *testing.T) {
	a, _ := ValidLogPath(`d:/LOGS/Web-A`, "")
	b, _ := ValidLogPath(`D:\logs\web-a\`, "")
	if CanonKey(a) != CanonKey(b) {
		t.Fatalf("%q vs %q", CanonKey(a), CanonKey(b))
	}
	c, _ := ValidLogPath(`D:\Журналы\Web-A`, "")
	d, _ := ValidLogPath(`d:\журналы\web-a`, "")
	if CanonKey(c) != CanonKey(d) {
		t.Fatalf("non-ASCII keys differ: %q vs %q", CanonKey(c), CanonKey(d))
	}
}

func TestWinPathHelpers(t *testing.T) {
	if got := WinJoin(`D:\Logs\`, `\web-a`, `u.log`); got != `D:\Logs\web-a\u.log` {
		t.Fatalf("WinJoin = %q", got)
	}
	if got := WinBase(`D:\Logs\web-a\`); got != "web-a" {
		t.Fatalf("WinBase = %q", got)
	}
	if got := WinDir(`D:\Logs\web-a`); got != `D:\Logs` {
		t.Fatalf("WinDir = %q", got)
	}
	if got := WinDir(`D:\Logs`); got != `D:\` {
		t.Fatalf("WinDir root = %q", got)
	}
}

func TestSplitImagePath(t *testing.T) {
	cases := []struct {
		name, in, dir, image string
	}{
		{"quoted with quoted arg", `"C:\Program Files\Redis\redis-server.exe" "C:\Program Files\Redis\redis.windows-service.conf"`, `C:\Program Files\Redis`, "redis-server.exe"},
		{"unquoted with spaces in folder", `C:\Program Files\Redis\redis-server.exe --service-run redis.conf`, `C:\Program Files\Redis`, "redis-server.exe"},
		{"svchost", `C:\Windows\system32\svchost.exe -k netsvcs -p`, `C:\Windows\system32`, "svchost.exe"},
		{"env variable folder", `%SystemRoot%\system32\svchost.exe -k web-a`, "", "svchost.exe"},
		{"SQL arguments never returned", `"C:\SQL\Binn\sqlservr.exe" -sMSSQLSERVER -dD:\SENTINEL-7f3a\m.mdf -eD:\SENTINEL-7f3a\ERRORLOG`, `C:\SQL\Binn`, "sqlservr.exe"},
		{"no exe no args", `C:\tools\app`, `C:\tools`, "app"},
		{"UNC image", `\\web-a.example.test\share\x.exe /s`, "", "x.exe"},
		{"empty", ``, "", ""},
		{"unterminated quote", `"C:\broken\x.exe`, "", ""},
		{"exe only", `redis-server.exe`, "", "redis-server.exe"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, image := SplitImagePath(c.in)
			if dir != c.dir || image != c.image {
				t.Fatalf("got (%q, %q), want (%q, %q)", dir, image, c.dir, c.image)
			}
			if strings.Contains(dir+image, "SENTINEL") {
				t.Fatal("argument leaked")
			}
		})
	}
}

func TestRegistryAllowlist(t *testing.T) {
	const sql = `HKLM\SOFTWARE\Microsoft\Microsoft SQL Server`
	cases := []struct {
		key, value string
		ok         bool
	}{
		{`HKLM\SYSTEM\CurrentControlSet\Services\HTTP\Parameters`, "ErrorLoggingDir", true},
		{`hklm\system\currentcontrolset\services\http\parameters\`, "errorloggingdir", true},
		{`HKLM\SYSTEM\CurrentControlSet\Control\TimeZoneInformation`, "TimeZoneKeyName", true},
		{sql + `\MSSQL16.WEBA\MSSQLServer\Parameters`, "SQLArg0", true},
		{sql + `\MSSQL16.WEBA\MSSQLServer\Parameters`, "SQLArg12", true},
		{sql + `\Instance Names\SQL`, "WEBA", true},

		{`HKLM\SYSTEM\CurrentControlSet\Services\HTTP\Parameters`, "UrlSegmentMaxLength", false},
		{`HKLM\SYSTEM\CurrentControlSet\Services\HTTP`, "ErrorLoggingDir", false},
		{`HKLM\SYSTEM\CurrentControlSet\Services\HTTP\Parameters\..\..`, "ErrorLoggingDir", false},
		{`HKCU\SYSTEM\CurrentControlSet\Services\HTTP\Parameters`, "ErrorLoggingDir", false},
		{sql + `\MSSQL16.WEBA\MSSQLServer\Parameters`, "Password", false},
		{sql + `\MSSQL16.WEBA\MSSQLServer`, "SQLArg0", false},
		{sql + `\MSSQL16.WEBA\MSSQLServer\SuperSocketNetLib`, "SQLArg0", false},
		{sql + `\..\x\MSSQLServer\Parameters`, "SQLArg0", false},
		{sql + `\Instance Names\SQL`, `a\b`, false},
		{`HKLM\SAM\SAM`, "Anything", false},
		{`HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "ProductId", false},
	}
	for _, c := range cases {
		if got := RegistryAllowed(c.key, c.value); got != c.ok {
			t.Errorf("RegistryAllowed(%q, %q) = %v, want %v", c.key, c.value, got, c.ok)
		}
	}
}

func TestReasonFromError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want Reason
	}{
		{"sentinel", ErrAccessDenied, ReasonNeedsElevation},
		{"wrapped permission", errors.Join(errors.New("open"), errPerm()), ReasonNeedsElevation},
		{"not supported", ErrNotSupportedOS, ReasonNotSupportedOS},
		{"too many entries", ErrTooManyEntries, ReasonTooLarge},
		{"path shape", unsafeErr("unc"), ReasonUnsafePath},
		{"other", errors.New("boom"), ReasonUnreadable},
	}
	for _, c := range cases {
		if got := ReasonFromError(c.err); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if !IsAccessDenied(ErrAccessDenied) || IsAccessDenied(nil) || IsAccessDenied(errors.New("x")) {
		t.Error("IsAccessDenied")
	}
}

func TestNotSupportedEnv(t *testing.T) {
	env, err := notSupportedEnv()
	if !errors.Is(err, ErrNotSupportedOS) {
		t.Fatalf("err = %v", err)
	}
	if env.FS != nil || env.Machine != nil {
		t.Fatal("stub returned a usable Env")
	}
	if ReasonFromError(err) != ReasonNotSupportedOS {
		t.Fatal("not-supported-os reason")
	}
}
