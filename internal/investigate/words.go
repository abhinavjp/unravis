package investigate

// The fixed words of the guided analysis (A25: Steps 0 to 10). The names and the
// "In a nutshell" lines are shown on every step card and printed in
// docs/user/guided-analysis.md; a test keeps the two in step. None of them
// names a cause, and none uses a percentage or a score.

// StepCount is the number of steps, numbered 0 to 10.
const StepCount = 11

var stepNames = [StepCount]string{
	"Frame", "Trust data", "Shape", "Time split", "Load", "Changes",
	"App tier", "SQL", "Mongo", "Host", "Origin",
}

var stepNutshells = [StepCount]string{
	"A slow period only means something next to a normal one. Pick the slow stretch and a normal stretch from before it, and we compare the two.",
	"Before reading any number we check that the data can be trusted: clocks agree, no call is cut off, and nothing is missing.",
	"We find when things first got slower, whether it was a sudden jump, a slow climb or a repeating pattern, and how many routes moved.",
	"Each call's time is split into audit set-up, the work itself and time outside the app. The part that grew most shows where to look first.",
	"We set the number of calls arriving against how long each one takes, to see whether a pool or a limit could have filled up.",
	"We place the changes you listed next to the start of the slowdown. A change counts only if it came just before the start, not if it merely overlapped.",
	"We check the app's own queues and pools, using what the audit and the IIS logs can show. What they cannot show is marked as not tested.",
	"We check SQL Server against its own data: waits, blocking and plans. Until that data is imported, this step says Cannot test yet.",
	"We check MongoDB against its own data: slow operations, cache and disk. Until that data is imported, this step says Cannot test yet.",
	"We check the host against its own counters: CPU, memory, disk and network. Until that data is imported, this step says Cannot test yet.",
	"We line up when each tier first moved. The earliest is the origin and the others followed or amplified it. If they moved together, we say there is no clear origin.",
}

// StepName returns the name of step n, or "" when n is out of range.
func StepName(n int) string {
	if n < 0 || n >= StepCount {
		return ""
	}
	return stepNames[n]
}

// StepNutshell returns the "In a nutshell" line of step n.
func StepNutshell(n int) string {
	if n < 0 || n >= StepCount {
		return ""
	}
	return stepNutshells[n]
}
