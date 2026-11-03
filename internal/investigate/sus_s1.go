package investigate

// sigQueueBurst reads HTTPERR rows that show the HTTP.sys queue full or the
// app-pool queue timing out (S1). No rise against the comparison period is
// required: these rows are rare on a healthy server.
func sigQueueBurst(in Input, f Facts, th Thresholds, fl map[string]Signal) Signal {
	s, _ := httpErrBurst(in, th, false, "HTTPERR queue rows", "QueueFull", "Timer_AppPool", "AppPoolTimer")
	return s
}
