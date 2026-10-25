package discover_test

import (
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/discover"
	"api-audit-explorer/internal/discover/fake"
)

func serilogPath(path string) string {
	return `{"Serilog":{"WriteTo":[{"Name":"File","Args":{"path":"` + path + `"}}]}}`
}

func assertRefusedUnsafe(t *testing.T, fs []discover.Finding, gs []discover.Gap, hide ...string) {
	t.Helper()
	for _, f := range fs {
		if f.Kind == discover.KindLogFile || f.Kind == discover.KindLogSet {
			t.Fatalf("a log finding was emitted for a refused sink: %+v", f)
		}
	}
	if gapWith(gs, discover.ReasonUnsafePath) == nil {
		t.Fatalf("want an unsafe-path gap, got %v", gs)
	}
	for _, g := range gs {
		for _, h := range hide {
			if strings.Contains(strings.ToLower(g.Display), strings.ToLower(h)) {
				t.Fatalf("gap display %q shows a refused name", g.Display)
			}
		}
	}
	assertNoSentinel(t, fs, gs)
}

func TestSinkNamingAKeyFileIsRefused(t *testing.T) {
	cases := map[string]struct{ cfgPath, file string }{
		"private key outside":       {`D:/otherdir/id_rsa`, `D:\otherdir\id_rsa`},
		"private key in the site":   {`logs/id_rsa`, site + `\logs\id_rsa`},
		"pem file":                  {`D:/otherdir/server.pem`, `D:\otherdir\server.pem`},
		"credentials json":          {`D:/otherdir/credentials.json`, `D:\otherdir\credentials.json`},
		"key word":                  {`D:/otherdir/SENTINEL-7f3a-key.log`, `D:\otherdir\SENTINEL-7f3a-key.log`},
		"cert word":                 {`logs/cert.txt`, site + `\logs\cert.txt`},
		"no extension":              {`D:/otherdir/notes`, `D:\otherdir\notes`},
		"not a log extension":       {`logs/data.bin`, site + `\logs\data.bin`},
		"pfx":                       {`logs/site.pfx`, site + `\logs\site.pfx`},
		"appsettings read as a log": {`logs/appsettings.json`, site + `\logs\appsettings.json`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := fake.New()
			put(e, site+`\appsettings.json`, serilogPath(c.cfgPath), 0)
			// Content that would pass for a log if the sniff were the only gate.
			put(e, c.file, serilogLine, time.Hour)
			fs, gs := detect(t, e)
			assertRefusedUnsafe(t, fs, gs, "id_rsa", "SENTINEL-7f3a", "server.pem", "credentials")
		})
	}
}

func TestSinkOutsideTheSiteNeedsARecognisedHead(t *testing.T) {
	cfg := serilogPath("D:/otherdir/app.log")
	t.Run("log-shaped content is reported", func(t *testing.T) {
		e := fake.New()
		put(e, site+`\appsettings.json`, cfg, 0)
		put(e, `D:\otherdir\app.log`, serilogLine, time.Hour)
		fs, gs := detect(t, e)
		f := mustOne(t, fs, "serilog-text")
		if f.Confidence < 70 || len(gs) != 0 {
			t.Fatalf("conf=%d gaps=%v", f.Confidence, gs)
		}
	})
	t.Run("other content is refused", func(t *testing.T) {
		e := fake.New()
		put(e, site+`\appsettings.json`, cfg, 0)
		put(e, `D:\otherdir\app.log`, "-----BEGIN PRIVATE KEY-----\nSENTINEL-7f3a\n-----END PRIVATE KEY-----\n", time.Hour)
		fs, gs := detect(t, e)
		assertRefusedUnsafe(t, fs, gs)
	})
	t.Run("an empty file cannot be sniffed so it is refused", func(t *testing.T) {
		e := fake.New()
		put(e, site+`\appsettings.json`, cfg, 0)
		put(e, `D:\otherdir\app.log`, "", time.Hour)
		fs, gs := detect(t, e)
		assertRefusedUnsafe(t, fs, gs)
	})
	t.Run("inside the site an unrecognised head is still reported", func(t *testing.T) {
		e := fake.New()
		put(e, site+`\appsettings.json`, serilogPath("logs/app.log"), 0)
		put(e, site+`\logs\app.log`, "not a recognised format\n", time.Hour)
		fs, _ := detect(t, e)
		f := mustOne(t, fs, "serilog-text")
		if f.Confidence != 40 {
			t.Fatalf("conf=%d", f.Confidence)
		}
	})
	t.Run("a folder given as an extra root counts as inside", func(t *testing.T) {
		e := fake.New()
		put(e, site+`\appsettings.json`, cfg, 0)
		put(e, `D:\otherdir\app.log`, "not a recognised format\n", time.Hour)
		fs, _ := detect(t, e, site, `D:\otherdir`)
		mustOne(t, fs, "serilog-text")
	})
}
