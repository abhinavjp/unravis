package investigate

// sigDrain reads a slow client or proxy (S12): HTTPERR rows where HTTP.sys
// dropped a slow reader, or IIS time outside the application rising while the
// application's own P95 did not. It runs after o_large and p95_up are set.
func sigDrain(in Input, f Facts, th Thresholds, fl map[string]Signal) Signal {
	hs, _ := httpErrBurst(in, th, false, "HTTPERR slow-client rows", "Timer_MinBytesPerSecond", "Connection_Dropped")
	if hs.Value == Yes {
		return hs
	}
	o, p := fl[SigOLarge], fl[SigP95Up]
	if o.Value == Yes && p.Value == No {
		return sig(Yes, "Time outside the application rose while the application P95 did not.")
	}
	if hs.Value == No && o.Value == No {
		return sig(No, "No slow-client HTTPERR burst, and time outside the application did not rise.")
	}
	if hs.Value == No {
		// HTTPERR is complete and quiet; the outside-time route could not decide.
		return sig(Unknown, "No slow-client HTTPERR burst, but time outside the application could not be read alone.")
	}
	if o.Value == Unknown && o.Missing != "" {
		return missing(o.Missing, "Neither an HTTPERR log nor time outside the application could be read.")
	}
	return missing("httperr", "No HTTPERR log was imported, and time outside the application does not show a slow client alone.")
}
