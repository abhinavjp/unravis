package investigate

// Lane-local marks for the SQL event lane (FETCHABILITY-AUDIT D10, 6a). They
// are not in Thresholds, so they do not move Version.
const (
	sqlDeadlockMin  = 5   // deadlocks in the incident window
	sqlDeadlockRise = 2.0 // times the reference rate per minute
)

// D16 marks (attention events = client-side timeouts or cancels).
const (
	sqlAttnMin5     = 3    // attention rows in any 5 minutes of the incident
	sqlAttnShare    = 0.02 // or this share of completed calls ...
	sqlAttnMinCalls = 50   // ... when the incident holds at least this many calls
)

// D17 marks (error 18456, failed logins).
const (
	sqlLoginMin5 = 5   // failed logins in any 5 minutes of the incident
	sqlLoginRise = 3.0 // times the comparison rate per minute
)

// ApplySQLEvents writes the SQL event lane answers into in.Lanes. It never
// overwrites a Yes another reader set. Without a SQL source it does nothing.
func (in *Input) ApplySQLEvents() {
	if !in.SQLPresent {
		return
	}
	if in.Lanes == nil {
		in.Lanes = map[string]Tri{}
	}
	in.setLane("D2", sqlD2(*in))
	in.setLane("D16", sqlD16(*in))
	in.setLane("D17", sqlD17(*in))
	if !containsStr(in.LaneSources, "sql-events") {
		in.LaneSources = append(in.LaneSources, "sql-events")
	}
}

// setLane records a lane answer. Unknown is not written and a Yes already set
// by another reader is kept.
func (in *Input) setLane(id string, v Tri) {
	if v == Unknown {
		return
	}
	if in.Lanes == nil {
		in.Lanes = map[string]Tri{}
	}
	if in.Lanes[id] == Yes {
		return
	}
	in.Lanes[id] = v
}

// containsStr reports whether ss holds s.
func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// sqlD2 answers D2 (deadlocks) from imported deadlock rows.
func sqlD2(in Input) Tri {
	if !in.Incident.Valid() {
		return Unknown // no readable window: no rows there is not a No
	}
	if in.SQLKinds != nil && !in.SQLKinds["deadlock"] {
		return Unknown
	}
	b := CountBurst(sqlTimes(in, "deadlock"), in.Incident, in.Reference)
	if b.Inc >= sqlDeadlockMin && (b.RefRate() == 0 || b.IncRate() >= sqlDeadlockRise*b.RefRate()) {
		return Yes
	}
	if b.Inc == 0 {
		return No
	}
	return Unknown
}

// sqlD16 reads attention rows (D16). Without attention capture it is unknown.
func sqlD16(in Input) Tri {
	if !in.SQLKinds["attention"] || !in.Incident.Valid() {
		return Unknown // no capture, or no readable window: no rows there is not a No
	}
	b := CountBurst(sqlTimes(in, "attention"), in.Incident, in.Reference)
	if b.Peak5 >= sqlAttnMin5 {
		return Yes
	}
	if in.SQLKinds["call"] {
		calls := CountBurst(sqlTimes(in, "call"), in.Incident, in.Reference).Inc
		if calls >= sqlAttnMinCalls && float64(b.Inc) >= sqlAttnShare*float64(calls) {
			return Yes
		}
	}
	if b.Inc == 0 {
		return No
	}
	return Unknown
}

// sqlD17 reads failed-login rows from the error log (D17).
func sqlD17(in Input) Tri {
	if !in.SQLKinds["login-failed"] || !in.Incident.Valid() || !in.Reference.Valid() {
		return Unknown // a rise needs both windows readable
	}
	b := CountBurst(sqlTimes(in, "login-failed"), in.Incident, in.Reference)
	switch {
	case b.Peak5 >= sqlLoginMin5 && (b.RefRate() == 0 || b.IncRate() >= sqlLoginRise*b.RefRate()):
		return Yes
	case b.Inc == 0 || b.IncRate() <= b.RefRate():
		return No
	}
	return Unknown
}
