package audit

// Sample-size thresholds shared by every comparison, rate and quantile rule.
// Callers outside this package (the MCP bridge, the web client) restate them in
// prose or literals and must stay in step with these values.
const (
	// MinSampleCount is the fewest valid (timed) samples a window needs before its
	// quantiles are compared: below it a cohort is "insufficient_data", an overview
	// reference is withheld, and a histogram shift has no z-score.
	MinSampleCount = 20
	// P99MinSampleCount is the fewest valid samples before a P99 is reported.
	// Below it the nearest-rank P99 is just the maximum.
	P99MinSampleCount = 100
)
