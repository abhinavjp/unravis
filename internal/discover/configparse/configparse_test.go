package configparse_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"api-audit-explorer/internal/discover/configparse"
)

// Synthetic applicationHost.config. SENTINEL-7f3a sits in every place a real
// config keeps a secret or a host name, and in the attributes the extractor
// does not keep. The marker must never reach the result.
const appHostXML = `<?xml version="1.0" encoding="UTF-8"?>
<configuration>
  <connectionStrings>
    <add name="db" connectionString="Server=203.0.113.5;User Id=sa;Password=SENTINEL-7f3a" />
  </connectionStrings>
  <system.applicationHost>
    <applicationPools>
      <add name="DefaultAppPool" managedRuntimeVersion="v4.0" />
      <add name="pool-a" managedRuntimeVersion="v2.0">
        <processModel identityType="SpecificUser" userName="SENTINEL-7f3a-user" password="SENTINEL-7f3a" />
      </add>
      <add name="pool-b" managedRuntimeVersion="" />
      <add name="bad&#9;name" managedRuntimeVersion="v4.0" />
    </applicationPools>
    <sites>
      <site name="web-a SENTINEL-7f3a" id="1">
        <application path="/" applicationPool="pool-a">
          <virtualDirectory path="/" physicalPath="%SystemDrive%\inetpub\wwwroot\web-a" userName="SENTINEL-7f3a" password="SENTINEL-7f3a" />
          <virtualDirectory path="/static" physicalPath="D:\Static\SENTINEL-7f3a" />
        </application>
        <bindings>
          <binding protocol="https" bindingInformation="198.51.100.7:443:web-a.example.test" sslFlags="SENTINEL-7f3a" />
        </bindings>
        <logFile directory="D:\Logs\IIS" logFormat="W3C" />
        <traceFailedRequestsLogging directory="D:\Logs\Traces" enabled="true" />
      </site>
      <site name="web-b" id="2" serverAutoStart="true">
        <application path="/" applicationPool="DefaultAppPool">
          <virtualDirectory path="/" physicalPath="D:\Sites\web-b" />
        </application>
        <bindings>
          <binding protocol="http" bindingInformation="*:80:web-b.example.test" />
        </bindings>
      </site>
      <site name="no-id" id="SENTINEL-7f3a">
        <logFile directory="D:\Logs\Nope" />
      </site>
      <siteDefaults>
        <logFile logFormat="W3C" directory="%SystemDrive%\inetpub\logs\LogFiles" />
        <traceFailedRequestsLogging directory="%SystemDrive%\inetpub\logs\FailedReqLogFiles" />
      </siteDefaults>
      <applicationDefaults applicationPool="DefaultAppPool" />
    </sites>
  </system.applicationHost>
  <location path="web-a">
    <system.webServer>
      <aspNetCore processPath="dotnet" arguments="web.dll --key SENTINEL-7f3a" />
    </system.webServer>
  </location>
</configuration>`

func TestParseAppHost(t *testing.T) {
	ah, err := configparse.ParseAppHost(strings.NewReader(appHostXML))
	if err != nil {
		t.Fatal(err)
	}
	if len(ah.Sites) != 2 {
		t.Fatalf("sites = %d, want 2 (the site with a non-numeric id is dropped)", len(ah.Sites))
	}
	s1, s2 := ah.Sites[0], ah.Sites[1]
	if s1.ID != "1" || s1.LogDir != `D:\Logs\IIS` || s1.LogFormat != "W3C" || s1.FrebDir != `D:\Logs\Traces` {
		t.Errorf("site 1 = %+v", s1)
	}
	if len(s1.AppPaths) != 1 || s1.AppPaths[0] != `%SystemDrive%\inetpub\wwwroot\web-a` {
		t.Errorf("site 1 app paths = %v, want the root application only", s1.AppPaths)
	}
	if s2.ID != "2" || s2.LogDir != "" || len(s2.AppPaths) != 1 {
		t.Errorf("site 2 = %+v", s2)
	}
	if ah.DefaultLogDir != `%SystemDrive%\inetpub\logs\LogFiles` || ah.DefaultLogFormat != "W3C" ||
		ah.DefaultFrebDir != `%SystemDrive%\inetpub\logs\FailedReqLogFiles` {
		t.Errorf("defaults = %q %q %q", ah.DefaultLogDir, ah.DefaultLogFormat, ah.DefaultFrebDir)
	}
	want := []configparse.Pool{{Name: "DefaultAppPool", RuntimeVersion: "v4.0"}, {Name: "pool-a", RuntimeVersion: "v2.0"}, {Name: "pool-b", RuntimeVersion: ""}}
	if fmt.Sprint(ah.Pools) != fmt.Sprint(want) {
		t.Errorf("pools = %v, want %v", ah.Pools, want)
	}
	if ah.Dropped != 2 {
		t.Errorf("dropped = %d, want 2 (one site, one pool)", ah.Dropped)
	}
}

func TestParseAppHostKeepsNoSecrets(t *testing.T) {
	ah, err := configparse.ParseAppHost(strings.NewReader(appHostXML))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(ah)
	for _, s := range []string{string(b), fmt.Sprintf("%+v", ah), fmt.Sprintf("%#v", ah)} {
		for _, bad := range []string{"SENTINEL", "198.51.100", "web-a.example", "203.0.113", "sa;"} {
			if strings.Contains(s, bad) {
				t.Errorf("result holds %q: %s", bad, s)
			}
		}
	}
}

func TestParseAppHostRefusals(t *testing.T) {
	wrap := func(inner string) string { return "<configuration>" + inner + "</configuration>" }
	deep := strings.Repeat("<a>", configparse.MaxDepth) + strings.Repeat("</a>", configparse.MaxDepth)
	big := wrap("<!-- " + strings.Repeat("x", configparse.MaxBytes) + " -->")
	cases := []struct {
		name string
		in   string
		want error
	}{
		{"doctype", `<!DOCTYPE configuration [<!ENTITY xxe SYSTEM "file:///C:/secret.txt">]>` + wrap("&xxe;"), configparse.ErrDTD},
		{"internal entity", `<!DOCTYPE c [<!ENTITY a "SENTINEL-7f3a">]><c>&a;</c>`, configparse.ErrDTD},
		{"too deep", wrap(deep), configparse.ErrDepth},
		{"too large", big, configparse.ErrTooLarge},
		{"empty", "", configparse.ErrSyntax},
		{"not xml", "just text", configparse.ErrSyntax},
		{"truncated", "<configuration><system.applicationHost>", configparse.ErrSyntax},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ah, err := configparse.ParseAppHost(strings.NewReader(c.in))
			if !errors.Is(err, c.want) || ah != nil {
				t.Fatalf("got (%v, %v), want error %v", ah, err, c.want)
			}
			if strings.Contains(err.Error(), "SENTINEL") || strings.Contains(err.Error(), "secret") {
				t.Errorf("error text carries input: %v", err)
			}
		})
	}
	// Just inside the depth limit passes.
	ok := strings.Repeat("<a>", configparse.MaxDepth-1) + strings.Repeat("</a>", configparse.MaxDepth-1)
	if _, err := configparse.ParseAppHost(strings.NewReader(ok)); err != nil {
		t.Errorf("depth %d refused: %v", configparse.MaxDepth-1, err)
	}
}

func TestParseAppHostBOMAndEncodingDecl(t *testing.T) {
	in := "\xEF\xBB\xBF" + `<?xml version="1.0" encoding="UTF-8"?><configuration><system.applicationHost><sites>` +
		`<site name="s" id="7"><logFile directory="D:\Logs\Café-日本" /></site></sites></system.applicationHost></configuration>`
	ah, err := configparse.ParseAppHost(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(ah.Sites) != 1 || ah.Sites[0].LogDir != `D:\Logs\Café-日本` {
		t.Errorf("sites = %+v", ah.Sites)
	}
}

func TestParseAppHostOverlongValue(t *testing.T) {
	in := `<configuration><system.applicationHost><sites><site id="1"><logFile directory="` +
		strings.Repeat("a", 5000) + `" /></site></sites></system.applicationHost></configuration>`
	ah, err := configparse.ParseAppHost(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if ah.Sites[0].LogDir != configparse.Overlong {
		t.Errorf("an overlong value was kept: %d bytes", len(ah.Sites[0].LogDir))
	}
}

const webConfigXML = `<?xml version="1.0" encoding="utf-8"?>
<configuration>
  <location path="." inheritInChildApplications="false">
    <system.webServer>
      <handlers><add name="aspNetCore" path="*" verb="*" modules="AspNetCoreModuleV2" /></handlers>
      <aspNetCore processPath="dotnet" arguments=".\web-a.dll --password SENTINEL-7f3a" stdoutLogEnabled="true" stdoutLogFile=".\logs\stdout" hostingModel="inprocess">
        <environmentVariables>
          <environmentVariable name="ConnectionStrings__Db" value="Server=203.0.113.5;Password=SENTINEL-7f3a" />
        </environmentVariables>
      </aspNetCore>
    </system.webServer>
  </location>
</configuration>`

func TestParseWebConfig(t *testing.T) {
	out, err := configparse.ParseWebConfig(strings.NewReader(webConfigXML))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || !out[0].Enabled || out[0].File != `.\logs\stdout` {
		t.Fatalf("stdout = %+v", out)
	}
	if s := fmt.Sprintf("%+v", out); strings.Contains(s, "SENTINEL") || strings.Contains(s, "203.0.113") {
		t.Errorf("result holds a secret: %s", s)
	}
}

func TestParseWebConfigVariants(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
		on   bool
	}{
		{"direct, disabled", `<configuration><system.webServer><aspNetCore stdoutLogEnabled="false" stdoutLogFile="D:\Logs\App\out" /></system.webServer></configuration>`, 1, false},
		{"no stdout file", `<configuration><system.webServer><aspNetCore processPath="dotnet" /></system.webServer></configuration>`, 0, false},
		{"upper-case true", `<configuration><system.webServer><aspNetCore stdoutLogEnabled="True" stdoutLogFile="out" /></system.webServer></configuration>`, 1, true},
		{"wrong parent", `<configuration><system.web><aspNetCore stdoutLogFile="out" /></system.web></configuration>`, 0, false},
		{"no aspNetCore", `<configuration><appSettings><add key="k" value="SENTINEL-7f3a" /></appSettings></configuration>`, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := configparse.ParseWebConfig(strings.NewReader(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if len(out) != c.want || (c.want > 0 && out[0].Enabled != c.on) {
				t.Errorf("got %+v", out)
			}
		})
	}
	if _, err := configparse.ParseWebConfig(strings.NewReader(`<!DOCTYPE a [<!ENTITY b "c">]><configuration/>`)); !errors.Is(err, configparse.ErrDTD) {
		t.Errorf("web.config DOCTYPE: %v", err)
	}
}

func FuzzParseAppHost(f *testing.F) {
	f.Add([]byte(appHostXML))
	f.Add([]byte(webConfigXML))
	f.Add([]byte(`<!DOCTYPE a [<!ENTITY b "c">]><a>&b;</a>`))
	f.Add([]byte("\xEF\xBB\xBF<configuration/>"))
	f.Add([]byte(`<configuration><system.applicationHost><sites><site id="1"><logFile directory="x"/></site></sites></system.applicationHost></configuration>`))
	f.Fuzz(func(t *testing.T, in []byte) {
		ah, err := configparse.ParseAppHost(strings.NewReader(string(in)))
		if err != nil {
			if ah != nil {
				t.Fatal("result and error together")
			}
			return
		}
		if len(ah.Sites) > 2048 || len(ah.Pools) > 1024 {
			t.Fatal("cap exceeded")
		}
		for _, s := range ah.Sites {
			for _, c := range s.ID {
				if c < '0' || c > '9' {
					t.Fatalf("site id %q is not digits", s.ID)
				}
			}
		}
		if out, err := configparse.ParseWebConfig(strings.NewReader(string(in))); err == nil && len(out) > 8 {
			t.Fatal("stdout cap exceeded")
		}
	})
}
