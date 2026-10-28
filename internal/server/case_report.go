package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/investigate"
)

// The report: what the guided analysis found, for someone who was not there.
//
//	POST /api/cases/{id}/report   body {format: "html"|"csv", request?, include?}
//	                              200 {format, filename, mime, sha256, bytes, content}
//
// The report is built from the engine's words and numbers, the checklist and the
// names of the saved views, copied field by field into reportDoc. It has no field
// for a request or response payload, a header, a user id, a connection or a
// password, so it cannot print one; the tests check the field names and an end to
// end run with sentinel strings. A gap is never hidden: a step that could not be
// tested, a source that was not read and a window with missing minutes are
// printed in the words of the app. `request` is the same body as the
// investigation route; without it the report says nothing has been analysed
// (an empty case still exports).
//
// Answers: 400 for a body that breaks a rule; 404 for an unknown case; 413 for a
// body over the limit; 415 for a body that is not JSON; 503 in the demo (except
// in the capture-only demo cases). A closed case exports like any other.

const (
	reportHTML = "html"
	reportCSV  = "csv"

	reportNothingText = "Nothing has been analysed in this case yet. Pick a slow stretch and a normal stretch in Guided analysis, then export again."
	reportNoPayloads  = "No request payloads, no user ids, no connection details, no passwords. Dates in UTC. Unravis only reads."
	reportMaxText     = 2000

	reportWarnStale     = "The windows or lists changed after the last run. This report holds the earlier run, as it was on screen."
	reportWarnTruncated = "The read stopped at its limit, so some calls are not in the figures. Narrow the window to read everything."
)

type reportRequest struct {
	Format  string                `json:"format"`
	Request *investigationRequest `json:"request"`
	Include *reportInclude        `json:"include"`
}

// reportInclude chooses the optional parts. A part left out of the request is
// included. The headline, how sure we are and the origin are always printed.
type reportInclude struct {
	Figures   *bool `json:"figures"`
	Suspects  *bool `json:"suspects"`
	Checklist *bool `json:"checklist"`
	Sources   *bool `json:"sources"`
	// Warnings are codes, never text: "stale" or "truncated". The server prints a
	// fixed sentence for each. Anything else is refused.
	Warnings []string `json:"warnings"`
}

func (i *reportInclude) on(pick func(*reportInclude) *bool) bool {
	if i == nil {
		return true
	}
	if v := pick(i); v != nil {
		return *v
	}
	return true
}

var reportWarningOrder = []string{"stale", "truncated"}

var reportWarningSentences = map[string]string{"stale": reportWarnStale, "truncated": reportWarnTruncated}

// reportWarningCodes checks the warning codes in a request. It returns false for
// a code the report does not know, or for a list that is far too long.
func reportWarningCodes(in *reportInclude) ([]string, bool) {
	if in == nil {
		return nil, true
	}
	if len(in.Warnings) > 8 {
		return nil, false
	}
	var out []string
	for _, code := range in.Warnings {
		if _, known := reportWarningSentences[code]; !known {
			return nil, false
		}
		out = appendOnce(out, code)
	}
	return out, true
}

func appendOnce(list []string, s string) []string {
	for _, have := range list {
		if have == s {
			return list
		}
	}
	return append(list, s)
}

// reportCautions is the list of fixed sentences printed at the top of the file:
// the client's codes, the run's own truncation, then the run's read warnings.
func reportCautions(src reportSource) []string {
	var codes []string
	for _, c := range src.Warnings {
		codes = appendOnce(codes, c)
	}
	if src.Read != nil && src.Read.Truncated {
		codes = appendOnce(codes, "truncated")
	}
	var out []string
	for _, want := range reportWarningOrder {
		for _, have := range codes {
			if have == want {
				out = append(out, reportWarningSentences[want])
			}
		}
	}
	if src.Read != nil {
		for _, w := range src.Read.Warnings {
			if t := clipText(w, reportMaxText); strings.TrimSpace(t) != "" {
				out = appendOnce(out, t)
			}
		}
	}
	return out
}

type reportAnswer struct {
	Format   string `json:"format"`
	Filename string `json:"filename"`
	Mime     string `json:"mime"`
	SHA256   string `json:"sha256"`
	Bytes    int    `json:"bytes"`
	Content  string `json:"content"`
}

/* ---------------------------------------------------------------- the document */

// reportDoc is everything a report prints. Each part is plain text or numbers.
type reportDoc struct {
	Title     string
	CaseID    string
	Generated string
	Closed    bool
	// Notice is set when nothing was analysed; the sections below it are empty.
	Notice   string
	Analysed bool
	// Cautions are fixed sentences about this file, printed before everything else.
	Cautions []string
	Windows  []reportFact
	Headline string
	How      reportHow
	Origin   reportOrigin

	ShowFigures bool
	Figures     []reportFigure
	Facts       []reportFact

	ShowSuspects  bool
	Suspects      []reportSuspect
	NotApplicable []string

	ShowSources bool
	Steps       []reportStep
	Gaps        []string
	ReadNotes   []string

	ShowChecklist bool
	Lists         []reportList
	Views         []reportView

	Footer string
}

type reportFact struct{ Label, Text string }

type reportHow struct {
	Word, Why, Overturn string
	Used, Missing, Not  []string
}

type reportOrigin struct {
	Status, Label, At, Note, ClockNote string
	Roles                              []reportRole
}

type reportRole struct{ Label, Role, At, Why string }

type reportFigure struct{ Label, Reference, Incident, Change string }

type reportSuspect struct {
	ID, Title, Group, Colour, ColourWord, Result, Why, Check, Overturn, Needs string
}

type reportStep struct {
	Number    int
	Name      string
	Readiness string
	Reasons   []string
	Findings  []string
}

type reportList struct {
	Key, Name string
	Items     []reportItem
}

type reportItem struct{ Text, Suspect string }

type reportView struct{ Name, Tool string }

// reportSource is what the handler hands the builder: the engine result (nil
// when nothing was analysed), what the run read, the checklist and views.
type reportSource struct {
	Title     string
	CaseID    string
	Closed    bool
	Generated time.Time
	Request   *investigationRequest
	Result    *investigate.Result
	Read      *investigationRead
	Items     []casestore.ChecklistItem
	Views     []casestore.SavedView
	Include   *reportInclude
	Warnings  []string // checked codes, see reportWarningCodes
}

var (
	reportListNames = map[string]string{
		casestore.ListToDo: "Need to do", casestore.ListDoing: "Working on",
		casestore.ListLearned: "What we learned", casestore.ListRuledOut: "Ruled out",
	}
	reportColourWords = map[investigate.Colour]string{
		investigate.Red: "Most likely", investigate.Orange: "Likely",
		investigate.Green: "Ruled out", investigate.Grey: "Not sure",
	}
	reportPartNames = map[string]string{
		"A": "Audit set-up (A)", "C": "Action time (C)", "P": "Payload capture time (P)", "O": "Outside time (O)",
	}
)

// newReportDoc copies the words and numbers a report prints out of the source.
func newReportDoc(src reportSource) reportDoc {
	d := reportDoc{
		Title: clipText(src.Title, 200), CaseID: src.CaseID, Closed: src.Closed,
		Generated:     src.Generated.UTC().Format("2 Jan 2006 15:04 UTC"),
		ShowFigures:   src.Include.on(func(i *reportInclude) *bool { return i.Figures }),
		ShowSuspects:  src.Include.on(func(i *reportInclude) *bool { return i.Suspects }),
		ShowSources:   src.Include.on(func(i *reportInclude) *bool { return i.Sources }),
		ShowChecklist: src.Include.on(func(i *reportInclude) *bool { return i.Checklist }),
		Footer:        reportNoPayloads,
	}
	if d.Title == "" {
		d.Title = "Untitled case"
	}
	if req := src.Request; req != nil && src.Result != nil {
		d.Windows = []reportFact{
			{"Slow stretch", fmtWindow(req.Incident.Start, req.Incident.End)},
			{"Normal stretch", fmtWindow(req.Reference.Start, req.Reference.End)},
		}
		if req.Route != "" {
			d.Windows = append(d.Windows, reportFact{"Route", clipText(req.Route, 256)})
		}
	}
	if src.Result == nil {
		d.Notice = reportNothingText
	} else {
		d.Analysed = true
		fillAnalysis(&d, *src.Result)
		if src.Read != nil {
			for _, w := range src.Read.Warnings {
				d.ReadNotes = append(d.ReadNotes, clipText(w, reportMaxText))
			}
			if src.Read.Truncated {
				d.ReadNotes = append(d.ReadNotes, "Some calls were not read because there were too many in the windows.")
			}
		}
	}
	for _, l := range casestore.ChecklistLists {
		list := reportList{Key: l, Name: reportListNames[l], Items: []reportItem{}}
		for _, it := range src.Items {
			if it.List == l {
				list.Items = append(list.Items, reportItem{Text: clipText(it.Text, reportMaxText), Suspect: it.Suspect})
			}
		}
		d.Lists = append(d.Lists, list)
	}
	for _, v := range src.Views {
		d.Views = append(d.Views, reportView{Name: clipText(v.Name, 200), Tool: v.Tool})
	}
	d.Cautions = reportCautions(src)
	return d
}

func fillAnalysis(d *reportDoc, res investigate.Result) {
	d.Headline = res.Headline
	c := res.Confidence
	d.How = reportHow{Word: c.Word, Why: c.Why, Overturn: c.Overturn, Used: orEmpty(c.SourcesUsed), Missing: orEmpty(c.SourcesMissing), Not: orEmpty(c.NotTested)}

	o := res.Origin
	d.Origin = reportOrigin{Status: o.Status, Label: o.Label, Note: o.Note, ClockNote: o.ClockNote}
	if o.At != nil {
		d.Origin.At = fmtMoment(*o.At)
	}
	for _, r := range o.Roles {
		role := reportRole{Label: r.Label, Role: r.Role, Why: r.Why, At: "did not move or could not be dated"}
		if r.At != nil {
			role.At = fmtMoment(*r.At)
		}
		d.Origin.Roles = append(d.Origin.Roles, role)
	}

	f := res.Facts
	d.Figures = []reportFigure{
		{"Calls used", fmt.Sprint(f.Ref.N), fmt.Sprint(f.Incident.N), ""},
		{"Median time (P50)", ms(f.Change.P50.RefMs), ms(f.Change.P50.IncMs), fmtChange(f.Change.P50)},
		{"Slow-end time (P95)", ms(f.Change.P95.RefMs), ms(f.Change.P95.IncMs), fmtChange(f.Change.P95)},
		{"Mean time", ms(f.Change.Mean.RefMs), ms(f.Change.Mean.IncMs), fmtChange(f.Change.Mean)},
		{"Calls per second (audited)", perSec(f.Ref.PerSec), perSec(f.Incident.PerSec), ""},
		{"Calls in flight (mean)", num(f.Load.RefL), num(f.Load.IncL), ""},
	}
	for _, p := range f.Parts.Items {
		name := reportPartNames[p.Name]
		if name == "" {
			name = p.Name
		}
		change := fmtChange(p.Change)
		if !p.Tested {
			change = "Cannot test yet"
		}
		d.Figures = append(d.Figures, reportFigure{name + ", mean", ms(p.RefMeanMs), ms(p.IncMeanMs), change})
	}
	d.Facts = []reportFact{
		{"When it started", onsetText(f.Onset)},
		{"Shape", clipText(f.Shape.Kind+". "+f.Shape.Why, reportMaxText)},
		{"How wide", clipText(f.Scope.Kind+". "+f.Scope.Why, reportMaxText)},
	}
	if f.Parts.Dominant != "" {
		d.Facts = append(d.Facts, reportFact{"Part that grew most", clipText(f.Parts.DominantWhy, reportMaxText)})
	}

	for _, s := range res.Suspects {
		d.Suspects = append(d.Suspects, reportSuspect{
			ID: s.ID, Title: s.Title, Group: s.Group, Colour: string(s.Colour), ColourWord: reportColourWords[s.Colour],
			Result: string(s.Result), Why: s.Why, Check: s.NextCheck, Overturn: s.Overturn, Needs: s.Needs,
		})
	}
	for _, n := range res.NotApplicable {
		d.NotApplicable = append(d.NotApplicable, n.ID+" "+n.Title)
	}

	for _, s := range res.Steps {
		d.Steps = append(d.Steps, reportStep{
			Number: s.Number, Name: s.Name, Readiness: string(s.Readiness),
			Reasons: orEmpty(s.Reasons), Findings: nonEmpty(s.Findings),
		})
	}
	for _, g := range f.Gaps {
		kind := "short gap, the step ran on the minutes that exist"
		if g.Kind == "long" {
			kind = "long gap, those minutes cannot be tested"
		}
		d.Gaps = append(d.Gaps, fmt.Sprintf("%s window, %s to %s UTC (%d min): %s.", capital(g.Window), g.Start.UTC().Format("2 Jan 15:04"), g.End.UTC().Format("15:04"), g.Minutes, kind))
	}
}

/* ---------------------------------------------------------------- small formatters */

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nonEmpty(s []string) []string {
	out := []string{}
	for _, x := range s {
		if strings.TrimSpace(x) != "" {
			out = append(out, x)
		}
	}
	return out
}

func capital(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func clipText(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "..."
}

func ms(v *float64) string {
	if v == nil {
		return "not measured"
	}
	if *v >= 1000 {
		return fmt.Sprintf("%.2f s", *v/1000)
	}
	return fmt.Sprintf("%.0f ms", *v)
}

func perSec(v *float64) string {
	if v == nil {
		return "not measured"
	}
	return fmt.Sprintf("%.2f", *v)
}

func num(v *float64) string {
	if v == nil {
		return "not measured"
	}
	return fmt.Sprintf("%.1f", *v)
}

func fmtMoment(t time.Time) string { return t.UTC().Format("2 Jan 15:04") + " UTC" }

func fmtWindow(a, b time.Time) string {
	return a.UTC().Format("2 Jan 2006 15:04") + " to " + b.UTC().Format("2 Jan 2006 15:04") + " UTC"
}

func fmtChange(c investigate.Change) string {
	if !c.Known {
		return "Cannot test yet"
	}
	if c.DeltaPct == nil || math.IsNaN(*c.DeltaPct) || math.IsInf(*c.DeltaPct, 0) {
		return "not measured"
	}
	if !c.Meaningful {
		return fmt.Sprintf("%+.0f%%, below the change mark", *c.DeltaPct)
	}
	return fmt.Sprintf("%+.0f%%", *c.DeltaPct)
}

func onsetText(o investigate.Onset) string {
	switch {
	case o.Status == "found" && o.T0 != nil:
		return "Started at " + fmtMoment(*o.T0) + ". " + o.Why
	case o.Status == "not found":
		return "No start was found. " + o.Why
	}
	return "The start could not be dated. " + o.Why
}

// reportFilename is the download name: ascii, no path characters, dated.
func reportFilename(title string, at time.Time, ext string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 40 {
			break
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "case"
	}
	return "unravis-report-" + slug + "-" + at.UTC().Format("2006-01-02") + "." + ext
}

/* ---------------------------------------------------------------- html */

var reportTemplate = template.Must(template.New("report").Funcs(template.FuncMap{
	"suspectClass": func(c string) string {
		switch c {
		case "red", "orange", "green", "grey":
			return c
		}
		return "grey"
	},
	"readyClass": func(r string) string {
		switch r {
		case "Ready":
			return "ready"
		case "Partly ready":
			return "partly"
		case "Failed":
			return "failed"
		}
		return "cannot"
	},
	"listClass": func(s string) string { return "list-" + s },
	"hasText":   func(s string) bool { return strings.TrimSpace(s) != "" },
}).Parse(reportHTMLText))

func renderReportHTML(d reportDoc) ([]byte, error) {
	var out bytes.Buffer
	if err := reportTemplate.Execute(&out, d); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

const reportHTMLText = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>{{.Title}} - Unravis report</title>
<style>
:root{color-scheme:light dark;--bg:#f5f5f3;--surface:#fff;--line:#d9d9d4;--text:#16191d;--text2:#4a525c;--accent:#0b6b57;--red:#b02a3d;--orange:#96600a;--green:#1f6b45;--grey:#5b6878}
@media (prefers-color-scheme:dark){:root{--bg:#050807;--surface:#0a0f0d;--line:#1a2621;--text:#eef3f1;--text2:#93a39b;--accent:#3fd6b0;--red:#ff7b72;--orange:#e3b341;--green:#3fb27f;--grey:#8fa39a}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--text);font:15px/1.5 "Segoe UI Variable Text","Segoe UI",system-ui,sans-serif}
main{max-width:860px;margin:0 auto;padding:24px 16px 40px}
h1{font-size:24px;margin:0 0 4px}h2{font-size:18px;margin:32px 0 8px;border-bottom:1px solid var(--line);padding-bottom:4px}h3{font-size:15px;margin:16px 0 4px}
p{margin:4px 0 8px}ul{margin:4px 0 8px;padding-left:20px}
.meta{color:var(--text2);font-size:13px}
.nutshell{border:1px solid var(--line);background:var(--surface);padding:12px;border-radius:2px}
.nutshell b{display:block;font-size:12px;letter-spacing:.04em;text-transform:uppercase;color:var(--text2)}
.notice{border:1px dashed var(--grey);padding:12px;border-radius:2px}
.warnbox{border:1px solid var(--orange);padding:8px 12px;margin:8px 0;border-radius:2px}
.warnbox b{display:block}.warnbox ul{margin:4px 0 0}
table{border-collapse:collapse;width:100%;background:var(--surface);font-size:14px}
th,td{border:1px solid var(--line);padding:4px 8px;text-align:left;vertical-align:top}
th{background:var(--bg);font-weight:600}
.sticker{display:inline-block;border:1px solid currentColor;border-radius:2px;padding:0 6px;font-size:12px;font-weight:600}
.red{color:var(--red)}.orange{color:var(--orange)}.green{color:var(--green)}.grey{color:var(--grey)}
.ready{color:var(--green)}.partly{color:var(--orange)}.cannot,.pending{color:var(--grey);background:repeating-linear-gradient(135deg,transparent 0 4px,rgba(128,128,128,.18) 4px 8px)}.failed{color:var(--red)}
.suspect{border:1px solid var(--line);background:var(--surface);border-left:4px solid var(--grey);padding:8px 12px;margin:8px 0;border-radius:2px}
.suspect.red{border-left-color:var(--red)}.suspect.orange{border-left-color:var(--orange)}.suspect.green{border-left-color:var(--green)}
.suspect p{color:var(--text)}.suspect .k{color:var(--text2);font-size:13px}
footer{margin-top:40px;border-top:1px solid var(--line);padding-top:12px;color:var(--text2);font-size:13px}
@media print{body{background:#fff}main{padding:0}}
</style>
</head>
<body>
<main>
<h1>{{.Title}}</h1>
<p class="meta">Case {{.CaseID}} · Report made {{.Generated}}{{if .Closed}} · This case is closed{{end}}</p>
{{if .Cautions}}<div class="warnbox" role="note"><b>This report has warnings</b><ul>{{range .Cautions}}<li>{{.}}</li>{{end}}</ul></div>{{end}}
{{if .Notice}}<p class="notice">{{.Notice}}</p>{{end}}
{{if .Analysed}}
{{if .Windows}}<p class="meta">{{range $i, $w := .Windows}}{{if $i}} · {{end}}{{$w.Label}}: {{$w.Text}}{{end}}</p>{{end}}
<div class="nutshell"><b>In a nutshell</b>{{.Headline}}</div>

<h2>How sure are we</h2>
<p><span class="sticker">{{.How.Word}}</span> {{.How.Why}}</p>
{{if .How.Used}}<h3>Sources used</h3><ul>{{range .How.Used}}<li>{{.}}</li>{{end}}</ul>{{end}}
{{if .How.Missing}}<h3>Sources missing</h3><ul>{{range .How.Missing}}<li>{{.}}</li>{{end}}</ul>{{end}}
{{if .How.Not}}<h3>Not tested</h3><ul>{{range .How.Not}}<li>{{.}}</li>{{end}}</ul>{{end}}
{{if hasText .How.Overturn}}<p><b>What would change this:</b> {{.How.Overturn}}</p>{{end}}

<h2>Origin</h2>
<p><b>{{.Origin.Status}}</b>{{if .Origin.Label}}: {{.Origin.Label}}{{if .Origin.At}}, first moved {{.Origin.At}}{{end}}{{end}}</p>
<p>{{.Origin.Note}}</p>
{{if hasText .Origin.ClockNote}}<p class="meta">{{.Origin.ClockNote}}</p>{{end}}
{{if .Origin.Roles}}<table><thead><tr><th>Tier</th><th>Role</th><th>First moved</th><th>Why</th></tr></thead><tbody>
{{range .Origin.Roles}}<tr><td>{{.Label}}</td><td>{{.Role}}</td><td>{{.At}}</td><td>{{.Why}}</td></tr>{{end}}
</tbody></table>{{end}}

{{if .ShowFigures}}
<h2>Figures</h2>
<table><thead><tr><th>Measure</th><th>Normal stretch</th><th>Slow stretch</th><th>Change</th></tr></thead><tbody>
{{range .Figures}}<tr><td>{{.Label}}</td><td>{{.Reference}}</td><td>{{.Incident}}</td><td>{{.Change}}</td></tr>{{end}}
</tbody></table>
<ul>{{range .Facts}}<li><b>{{.Label}}:</b> {{.Text}}</li>{{end}}</ul>
{{end}}

{{if .ShowSuspects}}
<h2>Suspects</h2>
<p class="meta">Most likely first. Grey means not sure: it could not be tested or does not apply, and it is never a pass.</p>
{{range .Suspects}}<div class="suspect {{suspectClass .Colour}}">
<p><span class="sticker {{suspectClass .Colour}}">{{.ColourWord}}</span> <b>{{.ID}} {{.Title}}</b> <span class="meta">{{.Result}}</span></p>
<p>{{.Why}}</p>
{{if hasText .Check}}<p class="k"><b>Cheapest check:</b> {{.Check}}</p>{{end}}
{{if hasText .Overturn}}<p class="k"><b>What would overturn it:</b> {{.Overturn}}</p>{{end}}
{{if hasText .Needs}}<p class="k"><b>To test it, import:</b> {{.Needs}}</p>{{end}}
</div>{{end}}
{{if .NotApplicable}}<p class="meta">Not in production, never coloured: {{range $i, $n := .NotApplicable}}{{if $i}}; {{end}}{{$n}}{{end}}.</p>{{end}}
{{end}}

{{if .ShowSources}}
<h2>Steps and what could not be tested</h2>
<table><thead><tr><th>Step</th><th>Status</th><th>Why</th></tr></thead><tbody>
{{range .Steps}}<tr><td>{{.Number}}. {{.Name}}</td><td><span class="sticker {{readyClass .Readiness}}">{{.Readiness}}</span></td><td>{{range .Reasons}}{{.}} {{end}}</td></tr>{{end}}
</tbody></table>
{{if .Gaps}}<h3>Missing minutes</h3><ul>{{range .Gaps}}<li>{{.}}</li>{{end}}</ul>{{end}}
{{if .ReadNotes}}<h3>What was not read</h3><ul>{{range .ReadNotes}}<li>{{.}}</li>{{end}}</ul>{{end}}
{{range .Steps}}{{if .Findings}}<h3>Step {{.Number}}, {{.Name}}</h3><ul>{{range .Findings}}<li>{{.}}</li>{{end}}</ul>{{end}}{{end}}
{{end}}
{{end}}

{{if .ShowChecklist}}
<h2>Checklist</h2>
{{range .Lists}}<h3>{{.Name}} ({{len .Items}})</h3>
{{if .Items}}<ul>{{range .Items}}<li>{{.Text}}{{if .Suspect}} <span class="meta">({{.Suspect}})</span>{{end}}</li>{{end}}</ul>{{else}}<p class="meta">Nothing here.</p>{{end}}{{end}}
{{if .Views}}<h3>Saved views (names only)</h3><ul>{{range .Views}}<li>{{.Name}} <span class="meta">({{.Tool}})</span></li>{{end}}</ul>{{end}}
{{end}}

<footer>{{.Footer}}</footer>
</main>
</body>
</html>
`

/* ---------------------------------------------------------------- csv */

// csvCell stops a spreadsheet from running a typed cell as a formula. Every cell
// goes through the same rule as the request export (spreadsheetSafe), with no
// exception for signed numbers: a cell that starts with = + - @ or a tab or
// return gets a leading apostrophe.
func csvCell(s string) string {
	return spreadsheetSafe(s)
}

func renderReportCSV(d reportDoc) ([]byte, error) {
	var out bytes.Buffer
	out.Write([]byte{0xEF, 0xBB, 0xBF}) // so a spreadsheet reads the file as UTF-8
	w := csv.NewWriter(&out)
	row := func(section, item, status, reference, incident, text string) {
		_ = w.Write([]string{csvCell(section), csvCell(item), csvCell(status), csvCell(reference), csvCell(incident), csvCell(text)})
	}
	_ = w.Write([]string{"section", "item", "status", "normal stretch", "slow stretch", "text"})
	row("case", d.Title, "", "", "", "Case "+d.CaseID+", report made "+d.Generated)
	if d.Closed {
		row("case", "closed", "", "", "", "This case is closed.")
	}
	if d.Notice != "" {
		row("notice", "", "Cannot test yet", "", "", d.Notice)
	}
	for _, c := range d.Cautions {
		row("warning", "", "Cannot test yet", "", "", c)
	}
	if d.Analysed {
		for _, f := range d.Windows {
			row("window", f.Label, "", "", "", f.Text)
		}
		row("headline", "In a nutshell", "", "", "", d.Headline)
		row("confidence", "How sure are we", d.How.Word, "", "", d.How.Why)
		for _, s := range d.How.Used {
			row("confidence-source", "used", "", "", "", s)
		}
		for _, s := range d.How.Missing {
			row("confidence-source", "missing", "", "", "", s)
		}
		for _, s := range d.How.Not {
			row("confidence-source", "not tested", "", "", "", s)
		}
		if d.How.Overturn != "" {
			row("confidence", "what would change this", "", "", "", d.How.Overturn)
		}
		row("origin", d.Origin.Label, d.Origin.Status, "", "", d.Origin.Note)
		for _, r := range d.Origin.Roles {
			row("origin-tier", r.Label, r.Role, "", "", r.At+". "+r.Why)
		}
		if d.ShowFigures {
			for _, f := range d.Figures {
				row("figure", f.Label, f.Change, f.Reference, f.Incident, "")
			}
			for _, f := range d.Facts {
				row("fact", f.Label, "", "", "", f.Text)
			}
		}
		if d.ShowSuspects {
			for _, s := range d.Suspects {
				row("suspect", s.ID+" "+s.Title, s.ColourWord+": "+s.Result, "", "", s.Why)
				row("suspect-check", s.ID, "", "", "", s.Check)
				row("suspect-overturn", s.ID, "", "", "", s.Overturn)
				if s.Needs != "" {
					row("suspect-needs", s.ID, "", "", "", s.Needs)
				}
			}
			for _, n := range d.NotApplicable {
				row("suspect-not-applicable", n, "", "", "", "Not in production, never coloured.")
			}
		}
		if d.ShowSources {
			for _, s := range d.Steps {
				row("step", fmt.Sprintf("%d. %s", s.Number, s.Name), s.Readiness, "", "", strings.Join(s.Reasons, " "))
				for _, f := range s.Findings {
					row("step-finding", fmt.Sprint(s.Number), "", "", "", f)
				}
			}
			for _, g := range d.Gaps {
				row("gap", "", "Cannot test yet", "", "", g)
			}
			for _, n := range d.ReadNotes {
				row("not-read", "", "", "", "", n)
			}
		}
	}
	if d.ShowChecklist {
		for _, l := range d.Lists {
			for _, it := range l.Items {
				row("checklist", l.Name, it.Suspect, "", "", it.Text)
			}
		}
		for _, v := range d.Views {
			row("saved-view", v.Name, v.Tool, "", "", "")
		}
	}
	row("footer", "", "", "", "", d.Footer)
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

/* ---------------------------------------------------------------- handler */

func (h *handler) caseReport(w http.ResponseWriter, r *http.Request, raw string) {
	if h.demo && !h.captureCases {
		writeError(w, http.StatusServiceUnavailable, planDemoText)
		return
	}
	var body reportRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Format != reportHTML && body.Format != reportCSV {
		writeError(w, http.StatusBadRequest, "The report format must be html or csv.")
		return
	}
	if body.Request != nil {
		if problem := body.Request.problem(); problem != "" {
			writeError(w, http.StatusBadRequest, problem)
			return
		}
	}
	codes, known := reportWarningCodes(body.Include)
	if !known {
		writeError(w, http.StatusBadRequest, "The report warnings were not recognised.")
		return
	}
	src := reportSource{Request: body.Request, Include: body.Include, Warnings: codes, Generated: h.reportNow()}
	var c casestore.Case
	if body.Request != nil {
		res, read, open, ok := h.runInvestigation(w, r, raw, *body.Request)
		if !ok {
			return
		}
		c = open
		res = res.Compact()
		src.Result, src.Read = &res, &read
	} else {
		_, open, ok := h.openCase(w, r, raw)
		if !ok {
			return
		}
		c = open
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	info := c.Info()
	src.Title, src.CaseID, src.Closed = info.Title, string(info.ID), info.Closed != nil
	var inner casestore.Case = c
	if sc, wrapped := c.(*sharedCase); wrapped {
		inner = sc.Case
	}
	if ps, ok := inner.(casestore.PlanStore); ok {
		var err error
		if src.Items, err = ps.ChecklistItems(ctx); err != nil {
			writePlanReadError(w, err)
			return
		}
		if src.Views, err = ps.SavedViews(ctx); err != nil {
			writePlanReadError(w, err)
			return
		}
	}

	doc := newReportDoc(src)
	var (
		content []byte
		err     error
		ext     = body.Format
		mime    = "text/html; charset=utf-8"
	)
	if body.Format == reportCSV {
		content, err = renderReportCSV(doc)
		mime = "text/csv; charset=utf-8"
	} else {
		content, err = renderReportHTML(doc)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "The report could not be built.")
		return
	}
	sum := sha256.Sum256(content)
	writeJSON(w, http.StatusOK, reportAnswer{
		Format: body.Format, Filename: reportFilename(doc.Title, src.Generated, ext), Mime: mime,
		SHA256: hex.EncodeToString(sum[:]), Bytes: len(content), Content: string(content),
	}, maxResponseBytes)
}

// reportNow is the time printed on a report: the demo's clock in the demo.
func (h *handler) reportNow() time.Time {
	now := time.Now().UTC()
	if h.demo {
		if clock, ok := h.repo.(interface{ DemoTime() time.Time }); ok {
			now = clock.DemoTime().UTC()
		}
	}
	return now
}
