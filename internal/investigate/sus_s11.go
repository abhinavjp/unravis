package investigate

// sigRecvTimeout reads HTTPERR rows where HTTP.sys gave up waiting for a
// request's headers or body (S11). These rows are common at a low rate on a
// public server, so they must also rise over the comparison period.
func sigRecvTimeout(in Input, f Facts, th Thresholds, fl map[string]Signal) Signal {
	s, _ := httpErrBurst(in, th, true, "HTTPERR receive-timeout rows", "Timer_HeaderWait", "Timer_EntityBody")
	return s
}
