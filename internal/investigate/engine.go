package investigate

// Result is everything one run says: the facts, the signals, the eleven steps,
// the ranked suspects, the origin and how sure we are. Thresholds carries the
// version and the whole table the run used, so a saved answer names its rules.
type Result struct {
	Version       string         `json:"version"`
	Thresholds    Thresholds     `json:"thresholds"`
	Headline      string         `json:"headline"`
	Facts         Facts          `json:"facts"`
	Signals       Signals        `json:"signals"`
	Steps         []Step         `json:"steps"`
	Suspects      []Suspect      `json:"suspects"`
	NotApplicable []CatalogEntry `json:"notApplicable"`
	Origin        Origin         `json:"origin"`
	Confidence    Confidence     `json:"confidence"`
	// SeriesMinutes counts the per-minute rows in Facts.Series, which Compact drops.
	SeriesMinutes int `json:"seriesMinutes"`
}

// Analyze runs the whole investigation: facts, then signals, then the evaluation
// (A27). It is a pure function of the Input.
func Analyze(in Input) Result {
	th := Current()
	f := ComputeFacts(in, th)
	s := ComputeSignals(in, f, th)
	steps := Steps(in, f, s, th)
	sps := Evaluate(in, f, s, th)
	o := ComputeOrigin(in, f, s, th)
	conf := ComputeConfidence(in, f, s, o, sps, steps)

	st := &steps[10]
	st.Findings = append(st.Findings, o.Note, o.ClockNote)
	for _, r := range o.Roles {
		if r.At != nil {
			st.Findings = append(st.Findings, r.Label+": "+r.Role+", first moved at "+fmtT(*r.At)+".")
		} else {
			st.Findings = append(st.Findings, r.Label+": did not move or could not be dated.")
		}
	}

	return Result{
		Version:       Version,
		Thresholds:    th,
		Headline:      Headline(f, o, sps),
		Facts:         f,
		Signals:       s,
		Steps:         steps,
		Suspects:      sps,
		NotApplicable: NotApplicable(),
		Origin:        o,
		Confidence:    conf,
		SeriesMinutes: len(f.Series),
	}
}

// Compact returns the result without the per-minute series, for a caller that
// wants the verdict and not the chart data.
func (r Result) Compact() Result {
	r.Facts.Series = nil
	return r
}
