package discover_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/discover"
	"api-audit-explorer/internal/discover/fake"
	"api-audit-explorer/internal/importer/all"
)

// webSentinel is a secret marker. It appears in the nginx and Apache configs
// below, in directives the detector must drop, and must never reach a finding,
// a gap, a Paths value or the JSON of either.
const webSentinel = "SENTINEL-7f3a"

var webModTime = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

// webNginxConf names one access log and three directives the detector must
// drop: ssl_certificate_key, auth_basic_user_file and proxy_pass.
const webNginxConf = `worker_processes 1;
events { worker_connections 1024; }
http {
    server {
        listen 80;
        ssl_certificate_key C:\secrets\` + webSentinel + `.key;
        auth_basic_user_file C:\secrets\` + webSentinel + `.htpasswd;
        location / {
            proxy_pass http://127.0.0.1:9000/` + webSentinel + `;
        }
        access_log logs/access.log main;
    }
}
`

// webApacheConf is an httpd.conf with ServerRoot, one CustomLog and a key file
// the detector must drop.
const webApacheConf = `ServerRoot "C:/Apache24"
Listen 80
SSLCertificateKeyFile C:\secrets\` + webSentinel + `.key
<VirtualHost *:80>
    CustomLog "logs/access.log" combined
</VirtualHost>
`

func webTestEnv(e *fake.Env) discover.Env { return e.Env(all.Registry()) }

// loadFixture reads a synthetic fixture from another importer's testdata.
func loadFixture(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// assertWebNoSentinel fails when the secret marker shows up in any finding (its
// JSON or its paths) or in any gap.
func assertWebNoSentinel(t *testing.T, findings []discover.Finding, gaps []discover.Gap) {
	t.Helper()
	b, err := json.Marshal(struct {
		Findings []discover.Finding `json:"findings"`
		Gaps     []discover.Gap     `json:"gaps"`
	}{findings, gaps})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), webSentinel) {
		t.Errorf("secret marker reached the JSON: %s", b)
	}
	for _, f := range findings {
		for _, p := range f.Paths() {
			if strings.Contains(p, webSentinel) {
				t.Errorf("secret marker reached a path: %q", p)
			}
		}
	}
}

func seedNginx(e *fake.Env, conf string) {
	e.Services.Items = []discover.Service{{Name: "nginx", State: "running", BinaryDir: `C:\nginx`, Image: "nginx.exe"}}
	e.FS.AddFile(`C:\nginx\conf\nginx.conf`, int64(len(conf)), webModTime, []byte(conf))
}

func seedLog(e *fake.Env, path string, content []byte) {
	e.FS.AddFile(path, int64(len(content)), webModTime, content)
}

func TestWebNginxDeclaredLog(t *testing.T) {
	e := fake.New()
	seedNginx(e, webNginxConf)
	seedLog(e, `C:\nginx\logs\access.log`, loadFixture(t, "../importer/accesslog/testdata/nginx_good.log"))

	findings, gaps := discover.NewWeb().Detect(context.Background(), webTestEnv(e))
	if len(gaps) != 0 {
		t.Errorf("gaps = %+v, want none", gaps)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(findings))
	}
	f := findings[0]
	if f.Importer != "nginx-access" || f.Kind != discover.KindLogFile || f.Confidence != 90 {
		t.Errorf("finding = importer %q kind %q confidence %d, want nginx-access log-file 90", f.Importer, f.Kind, f.Confidence)
	}
	if f.Shows != "discover.nginx.shows" || f.Display != "access.log" {
		t.Errorf("shows %q display %q", f.Shows, f.Display)
	}
	if got := f.Paths(); len(got) != 1 || got[0] != `C:\nginx\logs\access.log` {
		t.Errorf("paths = %q", got)
	}
	assertWebNoSentinel(t, findings, gaps)
	fake.AssertFindingsValid(t, findings, all.Registry())
}

func TestWebNginxDefaultLogWhenNoDirective(t *testing.T) {
	e := fake.New()
	seedNginx(e, "events { }\nhttp { server { listen 80; } }\n")
	seedLog(e, `C:\nginx\logs\access.log`, loadFixture(t, "../importer/accesslog/testdata/nginx_good.log"))

	findings, _ := discover.NewWeb().Detect(context.Background(), webTestEnv(e))
	if len(findings) != 1 || findings[0].Confidence != 70 {
		t.Fatalf("findings = %+v, want one finding at confidence 70 for the nginx default", findings)
	}
}

func TestWebNginxOffAndVariableDestinations(t *testing.T) {
	e := fake.New()
	seedNginx(e, "http { access_log off; }\n")
	seedLog(e, `C:\nginx\logs\access.log`, loadFixture(t, "../importer/accesslog/testdata/nginx_good.log"))
	findings, gaps := discover.NewWeb().Detect(context.Background(), webTestEnv(e))
	if len(findings) != 0 || len(gaps) != 0 {
		t.Errorf("access_log off: findings %d gaps %d, want none", len(findings), len(gaps))
	}

	e = fake.New()
	seedNginx(e, "http { access_log $var_log_path; }\n")
	findings, gaps = discover.NewWeb().Detect(context.Background(), webTestEnv(e))
	if len(findings) != 0 || len(gaps) != 1 || gaps[0].Reason != discover.ReasonConfigUnparsed {
		t.Errorf("variable destination: findings %d gaps %+v, want one config-unparsed gap", len(findings), gaps)
	}
}

func TestWebApacheCustomLog(t *testing.T) {
	e := fake.New()
	e.Services.Items = []discover.Service{{Name: "Apache2.4", State: "running", BinaryDir: `C:\Apache24\bin`, Image: "httpd.exe"}}
	e.FS.AddFile(`C:\Apache24\conf\httpd.conf`, int64(len(webApacheConf)), webModTime, []byte(webApacheConf))
	seedLog(e, `C:\Apache24\logs\access.log`, loadFixture(t, "../importer/accesslog/testdata/apache_good.log"))

	findings, gaps := discover.NewWeb().Detect(context.Background(), webTestEnv(e))
	if len(gaps) != 0 {
		t.Errorf("gaps = %+v, want none", gaps)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(findings))
	}
	f := findings[0]
	if f.Importer != "apache-combined" || f.Shows != "discover.apache.shows" || f.Confidence != 90 {
		t.Errorf("finding = importer %q shows %q confidence %d", f.Importer, f.Shows, f.Confidence)
	}
	if got := f.Paths(); len(got) != 1 || got[0] != `C:\Apache24\logs\access.log` {
		t.Errorf("paths = %q", got)
	}
	assertWebNoSentinel(t, findings, gaps)
	fake.AssertFindingsValid(t, findings, all.Registry())
}

func TestWebDisagreeingHeadIsLowConfidence(t *testing.T) {
	e := fake.New()
	seedNginx(e, webNginxConf)
	seedLog(e, `C:\nginx\logs\access.log`, []byte("this is not an access log\n"))

	findings, _ := discover.NewWeb().Detect(context.Background(), webTestEnv(e))
	if len(findings) != 1 || findings[0].Confidence != 40 {
		t.Fatalf("findings = %+v, want one finding at confidence 40", findings)
	}
}

func TestWebUNCLogRefused(t *testing.T) {
	e := fake.New()
	seedNginx(e, `http { access_log \\fileserver\share\access.log; }`+"\n")
	findings, gaps := discover.NewWeb().Detect(context.Background(), webTestEnv(e))
	if len(findings) != 0 {
		t.Errorf("UNC log produced %d finding(s)", len(findings))
	}
	if len(gaps) != 1 || gaps[0].Reason != discover.ReasonUnsafePath {
		t.Errorf("gaps = %+v, want one unsafe-path gap", gaps)
	}
	assertWebNoSentinel(t, findings, gaps)
}

func TestWebDeniedNeedsElevation(t *testing.T) {
	e := fake.New()
	seedNginx(e, webNginxConf)
	seedLog(e, `C:\nginx\logs\access.log`, loadFixture(t, "../importer/accesslog/testdata/nginx_good.log"))
	fake.AssertDeniedGap(t, discover.NewWeb(), e)
}

func TestWebMissingSourceGivesNoFinding(t *testing.T) {
	e := fake.New()
	findings, gaps := discover.NewWeb().Detect(context.Background(), webTestEnv(e))
	if len(findings) != 0 || len(gaps) != 0 {
		t.Errorf("empty host: findings %d gaps %+v, want none", len(findings), gaps)
	}

	// A service with its config but no access log: nothing to report.
	e = fake.New()
	seedNginx(e, webNginxConf)
	findings, _ = discover.NewWeb().Detect(context.Background(), webTestEnv(e))
	if len(findings) != 0 {
		t.Errorf("missing log produced %d finding(s)", len(findings))
	}
}

func TestWebProcessWithoutServiceIsGap(t *testing.T) {
	e := fake.New()
	e.Procs.Items = []discover.Proc{{Image: "httpd.exe", PID: 4242}}
	findings, gaps := discover.NewWeb().Detect(context.Background(), webTestEnv(e))
	if len(findings) != 0 {
		t.Errorf("process with no service produced %d finding(s)", len(findings))
	}
	if len(gaps) != 1 || gaps[0].Reason != discover.ReasonNotFound {
		t.Errorf("gaps = %+v, want one not-found gap", gaps)
	}
}
