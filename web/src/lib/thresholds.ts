// Sample-size thresholds shared by every comparison, rate and quantile rule.
// They mirror MinSampleCount and P99MinSampleCount in internal/audit/thresholds.go;
// tests/thresholds.test.cjs fails if the two drift apart.

/** Fewest valid (timed) samples a window needs before its quantiles are compared. */
export const MIN_SAMPLE = 20;
/** Fewest valid samples before a P99 is reported. */
export const P99_MIN_SAMPLE = 100;
