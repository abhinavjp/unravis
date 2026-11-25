package localci

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const fullWorkflow = `name: Full
on:
  push:
    branches: [main, develop]
    paths: ["src/**"]
    paths-ignore: ["docs/**"]
    tags: ["v*"]
  pull_request:
    branches: [main]
    types: [opened]
  schedule:
    - cron: "0 3 * * *"
    - cron: "0 4 * * 1"
  workflow_dispatch:
    inputs:
      tag:
        description: The tag
        required: true
        type: string
      fast:
        type: boolean
        default: false
env:
  A: "1"
  B: ${{ github.ref }}
defaults:
  run:
    shell: bash
    working-directory: sub
concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: ${{ github.event_name == 'pull_request' }}
permissions:
  contents: read
jobs:
  build:
    name: Build it
    runs-on: ubuntu-24.04
    if: ${{ github.event_name == 'push' }}
    env:
      X: y
    defaults:
      run:
        shell: pwsh
    timeout-minutes: 15
    continue-on-error: true
    permissions:
      id-token: write
    outputs:
      tag: ${{ steps.t.outputs.tag }}
    steps:
      - id: t
        name: Tag
        run: echo "tag=${{ inputs.tag }}"
        shell: bash
        working-directory: x
        env:
          K: v
        continue-on-error: false
        timeout-minutes: 3
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          fetch-depth: 0
  test:
    runs-on: [windows-2022]
    needs: build
    strategy:
      fail-fast: false
      max-parallel: 2
      matrix:
        shard: [1, 2]
        os: [a, b]
        exclude:
          - shard: 2
            os: b
        include:
          - shard: 1
            extra: z
    steps:
      - if: always()
        run: echo hi
  gate:
    runs-on: ubuntu-latest
    needs: [build, test]
    steps:
      - run: echo done
`

func TestParseSupportedKeys(t *testing.T) {
	w, uns, err := ParseWorkflow("full.yml", []byte(fullWorkflow))
	if err != nil {
		t.Fatal(err)
	}
	if len(uns) != 0 {
		t.Fatalf("unexpected unsupported: %v", uns)
	}
	if w.Name != "Full" || w.File != "full.yml" {
		t.Errorf("name/file: %q %q", w.Name, w.File)
	}
	push := w.On["push"]
	if !reflect.DeepEqual(push.Branches, []string{"main", "develop"}) || !reflect.DeepEqual(push.Paths, []string{"src/**"}) || push.Line != 3 {
		t.Errorf("push: %+v", push)
	}
	if got := w.On["pull_request"].Branches; !reflect.DeepEqual(got, []string{"main"}) {
		t.Errorf("pull_request branches: %v", got)
	}
	if got := w.On["schedule"].Cron; !reflect.DeepEqual(got, []string{"0 3 * * *", "0 4 * * 1"}) {
		t.Errorf("cron: %v", got)
	}
	if _, ok := w.On["workflow_dispatch"]; !ok {
		t.Error("workflow_dispatch missing")
	}
	if in := w.Inputs["tag"]; in.Type != "string" || !in.Required || in.Description != "The tag" {
		t.Errorf("input tag: %+v", in)
	}
	if in := w.Inputs["fast"]; in.Type != "boolean" || in.Default != "false" || in.Required {
		t.Errorf("input fast: %+v", in)
	}
	if w.Env["A"] != "1" || w.Env["B"] != "${{ github.ref }}" {
		t.Errorf("env: %v", w.Env)
	}
	if w.Defaults.Shell != "bash" || w.Defaults.WorkingDirectory != "sub" {
		t.Errorf("defaults: %+v", w.Defaults)
	}
	if w.Concurrency == nil || w.Concurrency.Group != "ci-${{ github.ref }}" || w.Concurrency.CancelInProgress != "${{ github.event_name == 'pull_request' }}" {
		t.Errorf("concurrency: %+v", w.Concurrency)
	}
	if len(w.Jobs) != 3 || w.Jobs[0].ID != "build" || w.Jobs[1].ID != "test" || w.Jobs[2].ID != "gate" {
		t.Fatalf("jobs order: %v", w.Jobs)
	}

	b := w.JobByID("build")
	if b.Name != "Build it" || b.RunsOn != "ubuntu-24.04" || b.If != "${{ github.event_name == 'push' }}" ||
		b.Env["X"] != "y" || b.Defaults.Shell != "pwsh" || b.TimeoutMinutes != 15 || b.ContinueOnError != "true" ||
		b.Outputs["tag"] != "${{ steps.t.outputs.tag }}" || len(b.Steps) != 2 {
		t.Errorf("build job: %+v", b)
	}
	if b.Line != 36 {
		t.Errorf("build line = %d", b.Line)
	}
	s0 := b.Steps[0]
	if s0.ID != "t" || s0.Name != "Tag" || s0.Shell != "bash" || s0.WorkingDirectory != "x" || s0.Env["K"] != "v" ||
		s0.ContinueOnError != "false" || s0.TimeoutMinutes != 3 || !strings.Contains(s0.Run, "inputs.tag") {
		t.Errorf("step 0: %+v", s0)
	}
	s1 := b.Steps[1]
	if !strings.HasPrefix(s1.Uses, "actions/checkout@3d3c") || s1.With["fetch-depth"] != "0" {
		t.Errorf("step 1: %+v", s1)
	}
	if s1.Line != 61 {
		t.Errorf("step 1 line = %d", s1.Line)
	}

	tj := w.JobByID("test")
	if tj.RunsOn != "windows-2022" || !reflect.DeepEqual(tj.Needs, []string{"build"}) || tj.Steps[0].If != "always()" {
		t.Errorf("test job: %+v", tj)
	}
	st := tj.Strategy
	if st == nil || st.FailFast || st.MaxParallel != 2 {
		t.Fatalf("strategy: %+v", st)
	}
	if !reflect.DeepEqual(st.Matrix.AxisOrder, []string{"shard", "os"}) || len(st.Matrix.Include) != 1 || len(st.Matrix.Exclude) != 1 {
		t.Errorf("matrix: %+v", st.Matrix)
	}
	if got := w.JobByID("gate").Needs; !reflect.DeepEqual(got, []string{"build", "test"}) {
		t.Errorf("gate needs: %v", got)
	}
	if w.JobByID("nope") != nil {
		t.Error("JobByID(nope) should be nil")
	}
}

func TestStrategyDefaultFailFast(t *testing.T) {
	src := "on: push\njobs:\n  a:\n    runs-on: x\n    strategy:\n      matrix:\n        n: [1]\n    steps:\n      - run: x\n"
	w, uns, err := ParseWorkflow("a.yml", []byte(src))
	if err != nil || len(uns) != 0 {
		t.Fatal(err, uns)
	}
	if !w.Jobs[0].Strategy.FailFast {
		t.Error("fail-fast should default to true")
	}
}

func TestOnForms(t *testing.T) {
	for _, on := range []string{"on: push", "on: [push, pull_request]", "on:\n  push:\n  pull_request:"} {
		src := on + "\njobs:\n  a:\n    runs-on: x\n    steps:\n      - run: x\n"
		w, uns, err := ParseWorkflow("a.yml", []byte(src))
		if err != nil || len(uns) != 0 {
			t.Errorf("%q: %v %v", on, err, uns)
			continue
		}
		if _, ok := w.On["push"]; !ok {
			t.Errorf("%q: no push", on)
		}
	}
}

func TestUnsupportedCategories(t *testing.T) {
	const tail = "jobs:\n  a:\n    runs-on: ubuntu-24.04\n    steps:\n      - run: x\n"
	cases := []struct {
		name  string
		src   string
		thing string
		line  int
	}{
		{"unknown top-level key", "on: push\nrun-name: hi\n" + tail, "top-level key run-name", 2},
		{"unknown event", "on:\n  push:\n  release:\n    types: [published]\n" + tail, "event release", 3},
		{"workflow_call", "on:\n  workflow_call:\n" + tail, "event workflow_call", 2},
		{"unknown push filter", "on:\n  push:\n    wat: 1\n" + tail, "push filter wat", 3},
		{"unsupported uses", "on: push\njobs:\n  a:\n    runs-on: x\n    steps:\n      - name: n\n        uses: actions/attest-build-provenance@abc # v1\n", "uses actions/attest-build-provenance", 7},
		{"local action", "on: push\njobs:\n  a:\n    runs-on: x\n    steps:\n      - uses: ./.github/actions/foo\n", "uses ./.github/actions/foo", 6},
		{"bad shell", "on: push\njobs:\n  a:\n    runs-on: x\n    steps:\n      - run: x\n        shell: python\n", "shell python", 7},
		{"custom shell", "on: push\njobs:\n  a:\n    runs-on: x\n    steps:\n      - run: x\n        shell: bash -e {0}\n", "shell bash -e {0}", 7},
		{"services", "on: push\njobs:\n  a:\n    runs-on: x\n    services:\n      db:\n        image: pg\n    steps:\n      - run: x\n", "job key services", 5},
		{"container", "on: push\njobs:\n  a:\n    runs-on: x\n    container: alpine\n    steps:\n      - run: x\n", "job key container", 5},
		{"environment", "on: push\njobs:\n  a:\n    runs-on: x\n    environment: prod\n    steps:\n      - run: x\n", "job key environment", 5},
		{"job concurrency", "on: push\njobs:\n  a:\n    runs-on: x\n    concurrency: g\n    steps:\n      - run: x\n", "job key concurrency", 5},
		{"reusable workflow", "on: push\njobs:\n  a:\n    uses: org/repo/.github/workflows/w.yml@v1\n", "job key uses (reusable workflow)", 4},
		{"matrix expression", "on: push\njobs:\n  a:\n    runs-on: x\n    strategy:\n      matrix: ${{ fromJSON(needs.x.outputs.m) }}\n    steps:\n      - run: x\n", "matrix as an expression", 6},
		{"both run and uses", "on: push\njobs:\n  a:\n    runs-on: x\n    steps:\n      - run: x\n        uses: actions/checkout@v4\n", "step with both run and uses", 6},
		{"neither run nor uses", "on: push\njobs:\n  a:\n    runs-on: x\n    steps:\n      - name: n\n", "step with neither run nor uses", 6},
		{"unknown job key", "on: push\njobs:\n  a:\n    runs-on: x\n    wat: 1\n    steps:\n      - run: x\n", "job key wat", 5},
		{"unknown step key", "on: push\njobs:\n  a:\n    runs-on: x\n    steps:\n      - run: x\n        wat: 1\n", "step key wat", 7},
		{"runs-on list of two", "on: push\njobs:\n  a:\n    runs-on: [a, b]\n    steps:\n      - run: x\n", "runs-on list with 2 labels", 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, uns, err := ParseWorkflow("w.yml", []byte(tc.src))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, u := range uns {
				if u.Thing == tc.thing && u.Line == tc.line && u.File == "w.yml" {
					want := fmt.Sprintf("unsupported: %s at w.yml:%d", tc.thing, tc.line)
					if u.Error() != want {
						t.Errorf("Error() = %q, want %q", u.Error(), want)
					}
					return
				}
			}
			t.Fatalf("want %q at line %d, got %v", tc.thing, tc.line, uns)
		})
	}
}

func TestStructuralErrors(t *testing.T) {
	cases := map[string]string{
		"broken yaml":      "on: [push\njobs: {",
		"empty":            "",
		"not a mapping":    "- a\n- b\n",
		"no jobs":          "on: push\n",
		"duplicate key":    "on: push\non: pull_request\njobs:\n  a:\n    runs-on: x\n    steps:\n      - run: x\n",
		"no runs-on":       "on: push\njobs:\n  a:\n    steps:\n      - run: x\n",
		"steps not a list": "on: push\njobs:\n  a:\n    runs-on: x\n    steps: nope\n",
		"bad timeout":      "on: push\njobs:\n  a:\n    runs-on: x\n    timeout-minutes: soon\n    steps:\n      - run: x\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := ParseWorkflow("w.yml", []byte(src)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestExpandMatrix(t *testing.T) {
	str := func(m []map[string]any) []string {
		var out []string
		for _, c := range m {
			var keys []string
			for k := range c {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			var parts []string
			for _, k := range keys {
				parts = append(parts, k+"="+fmt.Sprint(c[k]))
			}
			out = append(out, strings.Join(parts, ","))
		}
		return out
	}
	t.Run("empty", func(t *testing.T) {
		got := ExpandMatrix(MatrixSpec{})
		if len(got) != 1 || len(got[0]) != 0 {
			t.Errorf("got %v", got)
		}
	})
	t.Run("product in axis order", func(t *testing.T) {
		m := MatrixSpec{
			Axes:      map[string][]any{"a": {1, 2}, "b": {"x", "y"}},
			AxisOrder: []string{"a", "b"},
		}
		want := []string{"a=1,b=x", "a=1,b=y", "a=2,b=x", "a=2,b=y"}
		if got := str(ExpandMatrix(m)); !reflect.DeepEqual(got, want) {
			t.Errorf("got %v want %v", got, want)
		}
	})
	t.Run("exclude", func(t *testing.T) {
		m := MatrixSpec{
			Axes:      map[string][]any{"a": {1, 2}, "b": {"x", "y"}},
			AxisOrder: []string{"a", "b"},
			Exclude:   []map[string]any{{"a": 2, "b": "x"}},
		}
		want := []string{"a=1,b=x", "a=1,b=y", "a=2,b=y"}
		if got := str(ExpandMatrix(m)); !reflect.DeepEqual(got, want) {
			t.Errorf("got %v want %v", got, want)
		}
	})
	t.Run("exclude by partial key", func(t *testing.T) {
		m := MatrixSpec{
			Axes:      map[string][]any{"a": {1, 2}, "b": {"x", "y"}},
			AxisOrder: []string{"a", "b"},
			Exclude:   []map[string]any{{"b": "y"}},
		}
		want := []string{"a=1,b=x", "a=2,b=x"}
		if got := str(ExpandMatrix(m)); !reflect.DeepEqual(got, want) {
			t.Errorf("got %v want %v", got, want)
		}
	})
	t.Run("include merges extra keys", func(t *testing.T) {
		m := MatrixSpec{
			Axes:      map[string][]any{"a": {1, 2}, "b": {"x"}},
			AxisOrder: []string{"a", "b"},
			Include:   []map[string]any{{"a": 1, "extra": "e"}},
		}
		want := []string{"a=1,b=x,extra=e", "a=2,b=x"}
		if got := str(ExpandMatrix(m)); !reflect.DeepEqual(got, want) {
			t.Errorf("got %v want %v", got, want)
		}
	})
	t.Run("include never overwrites an axis value", func(t *testing.T) {
		m := MatrixSpec{
			Axes:      map[string][]any{"a": {1}},
			AxisOrder: []string{"a"},
			Include:   []map[string]any{{"a": 2, "extra": "e"}},
		}
		want := []string{"a=1", "a=2,extra=e"}
		if got := str(ExpandMatrix(m)); !reflect.DeepEqual(got, want) {
			t.Errorf("got %v want %v", got, want)
		}
	})
	t.Run("include creates new combination", func(t *testing.T) {
		m := MatrixSpec{
			Axes:      map[string][]any{"os": {"a", "b"}},
			AxisOrder: []string{"os"},
			Include:   []map[string]any{{"os": "c", "k": "v"}},
		}
		want := []string{"os=a", "os=b", "k=v,os=c"}
		if got := str(ExpandMatrix(m)); !reflect.DeepEqual(got, want) {
			t.Errorf("got %v want %v", got, want)
		}
	})
	t.Run("include without axis keys adds to all", func(t *testing.T) {
		m := MatrixSpec{
			Axes:      map[string][]any{"os": {"a", "b"}},
			AxisOrder: []string{"os"},
			Include:   []map[string]any{{"k": "v"}},
		}
		want := []string{"k=v,os=a", "k=v,os=b"}
		if got := str(ExpandMatrix(m)); !reflect.DeepEqual(got, want) {
			t.Errorf("got %v want %v", got, want)
		}
	})
	t.Run("only includes", func(t *testing.T) {
		m := MatrixSpec{Include: []map[string]any{{"t": "A"}, {"t": "B"}}}
		want := []string{"t=A", "t=B"}
		if got := str(ExpandMatrix(m)); !reflect.DeepEqual(got, want) {
			t.Errorf("got %v want %v", got, want)
		}
	})
	t.Run("exclude runs before include", func(t *testing.T) {
		m := MatrixSpec{
			Axes:      map[string][]any{"a": {1, 2}},
			AxisOrder: []string{"a"},
			Exclude:   []map[string]any{{"a": 2}},
			Include:   []map[string]any{{"a": 2}},
		}
		want := []string{"a=1", "a=2"}
		if got := str(ExpandMatrix(m)); !reflect.DeepEqual(got, want) {
			t.Errorf("got %v want %v", got, want)
		}
	})
}

func TestExpandMatrixFromParsedWorkflow(t *testing.T) {
	w, _, err := ParseWorkflow("full.yml", []byte(fullWorkflow))
	if err != nil {
		t.Fatal(err)
	}
	j := w.JobByID("test")
	combos := ExpandMatrix(j.Strategy.Matrix)
	// shard x os = 4, minus (2,b) = 3; include {shard:1, extra:z} merges into (1,a) and (1,b).
	if len(combos) != 3 {
		t.Fatalf("combos: %v", combos)
	}
	if combos[0]["extra"] != "z" || combos[1]["extra"] != "z" || combos[2]["extra"] != nil {
		t.Errorf("include merge: %v", combos)
	}
	if got := MatrixLabel(j, combos[0]); got != "test (1, a, z)" {
		t.Errorf("label = %q", got)
	}
}

func TestMatrixLabel(t *testing.T) {
	j := &Job{ID: "go-tests", Strategy: &Strategy{Matrix: MatrixSpec{AxisOrder: []string{"shard"}}}}
	if got := MatrixLabel(j, map[string]any{"shard": 1}); got != "go-tests (1)" {
		t.Errorf("got %q", got)
	}
	d := &Job{ID: "desktop", Name: "desktop", Strategy: &Strategy{Matrix: MatrixSpec{AxisOrder: []string{"os"}}}}
	if got := MatrixLabel(d, map[string]any{"os": "windows-2022"}); got != "desktop (windows-2022)" {
		t.Errorf("got %q", got)
	}
	m := &Job{ID: "m", Strategy: &Strategy{Matrix: MatrixSpec{AxisOrder: []string{"b", "a"}}}}
	if got := MatrixLabel(m, map[string]any{"a": 1, "b": 2}); got != "m (2, 1)" {
		t.Errorf("axis order: %q", got)
	}
	plain := &Job{ID: "plain", Name: "Plain job"}
	if got := MatrixLabel(plain, map[string]any{}); got != "Plain job" {
		t.Errorf("got %q", got)
	}
	if got := MatrixLabel(plain, nil); got != "Plain job" {
		t.Errorf("got %q", got)
	}
}

func TestTopoLevels(t *testing.T) {
	mk := func(needs map[string][]string, order ...string) *Workflow {
		w := &Workflow{}
		for _, id := range order {
			w.Jobs = append(w.Jobs, &Job{ID: id, Needs: needs[id]})
		}
		return w
	}
	t.Run("levels", func(t *testing.T) {
		w := mk(map[string][]string{"gate": {"a", "b", "c"}, "c": {"a"}}, "gate", "a", "b", "c")
		got, err := TopoLevels(w)
		if err != nil {
			t.Fatal(err)
		}
		want := [][]string{{"a", "b"}, {"c"}, {"gate"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v want %v", got, want)
		}
	})
	t.Run("no jobs", func(t *testing.T) {
		got, err := TopoLevels(&Workflow{})
		if err != nil || len(got) != 0 {
			t.Errorf("got %v %v", got, err)
		}
	})
	t.Run("missing needs", func(t *testing.T) {
		_, err := TopoLevels(mk(map[string][]string{"a": {"ghost"}}, "a"))
		if err == nil || !strings.Contains(err.Error(), "ghost") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("cycle", func(t *testing.T) {
		_, err := TopoLevels(mk(map[string][]string{"a": {"b"}, "b": {"c"}, "c": {"a"}}, "a", "b", "c"))
		if err == nil || !strings.Contains(err.Error(), "a -> b -> c -> a") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("self cycle", func(t *testing.T) {
		_, err := TopoLevels(mk(map[string][]string{"a": {"a"}}, "a"))
		if err == nil || !strings.Contains(err.Error(), "a -> a") {
			t.Errorf("err = %v", err)
		}
	})
}

// workflowsDir is relative to this package.
const workflowsDir = "../../.github/workflows"

// Golden: what the runner supports today. Workflows not listed here must report nothing.
// A key is the workflow file name, the value is a substring that must appear among the
// unsupported things of that workflow.
var knownUnsupported = map[string][]string{
	"release.yml":        {"uses actions/attest-build-provenance"},
	"verify-release.yml": {"top-level key run-name"},
}

func TestRepoWorkflows(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(workflowsDir, "*.yml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflows found in %s: %v", workflowsDir, err)
	}
	var clean []string
	for _, f := range files {
		name := filepath.Base(f)
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		w, uns, err := ParseWorkflow(name, data)
		if err != nil {
			t.Errorf("%s: structural error: %v", name, err)
			continue
		}
		if _, err := TopoLevels(w); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		for _, j := range w.Jobs {
			if j.Strategy != nil {
				if n := len(ExpandMatrix(j.Strategy.Matrix)); n == 0 {
					t.Errorf("%s: job %s expands to no matrix combination", name, j.ID)
				}
			}
		}
		for _, u := range uns {
			t.Logf("%s", u.Error())
		}
		want := knownUnsupported[name]
		if len(want) == 0 {
			if len(uns) != 0 {
				t.Errorf("%s: expected fully supported, got %v", name, uns)
			}
			clean = append(clean, name)
			continue
		}
		for _, sub := range want {
			found := false
			for _, u := range uns {
				if strings.Contains(u.Thing, sub) {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: expected an unsupported item containing %q, got %v", name, sub, uns)
			}
		}
	}
	sort.Strings(clean)
	t.Logf("fully supported workflows (%d): %s", len(clean), strings.Join(clean, ", "))
}
