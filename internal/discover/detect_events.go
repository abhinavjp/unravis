package discover

import (
	"context"
	"strings"
)

// Event channels the detector offers, in display order. Each is read with the
// win-events importer. The Security channel is not here and never will be:
// eventsRefused stops it even if a host lists it and a caller asks.
type eventChannel struct {
	name  string
	shows string
}

var eventChannels = []eventChannel{
	{"Application", showsEventsApp},
	{"System", showsEventsSystem},
	{"Microsoft-Windows-TaskScheduler/Operational", showsTaskSched},
}

// eventsRefused reports a channel the scan never offers or probes.
func eventsRefused(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	return n == "security" || strings.HasPrefix(n, "security/")
}

type eventsDetector struct{}

// NewEventsDetector returns the detector named "events". It offers the
// Application, System and Task Scheduler Operational channels that this account
// can read, and reports a gap for each one it cannot.
func NewEventsDetector() Detector { return eventsDetector{} }

func (eventsDetector) Name() string { return "events" }

// Detect lists the host's channels and probes each wanted one. Access denied on
// the listing or on a channel is a needs-elevation gap; a channel the host does
// not have is not-found. Nothing is read from any channel.
func (eventsDetector) Detect(ctx context.Context, env Env) ([]Finding, []Gap) {
	const det = "events"
	if env.Channels == nil {
		return nil, []Gap{{Detector: det, Kind: KindEventChannel, Display: "Event logs", Reason: ReasonNotSupportedOS}}
	}
	listed, listErr := env.Channels.List()
	var have map[string]string // lower case -> name as the host spells it
	if listErr == nil {
		have = make(map[string]string, len(listed))
		for _, n := range listed {
			if eventsRefused(n) {
				continue
			}
			have[strings.ToLower(n)] = n
		}
	}

	var findings []Finding
	var gaps []Gap
	for _, ch := range eventChannels {
		if ctx.Err() != nil {
			gaps = append(gaps, Gap{Detector: det, Kind: KindEventChannel, Display: ch.name, Reason: ReasonUnreadable})
			continue
		}
		if listErr != nil {
			gaps = append(gaps, GapFromError(det, KindEventChannel, ch.name, listErr))
			continue
		}
		name, ok := have[strings.ToLower(ch.name)]
		if !ok || eventsRefused(name) {
			gaps = append(gaps, Gap{Detector: det, Kind: KindEventChannel, Display: ch.name, Reason: ReasonNotFound})
			continue
		}
		if err := env.Channels.CanRead(name); err != nil {
			gaps = append(gaps, GapFromError(det, KindEventChannel, ch.name, err))
			continue
		}
		findings = append(findings, Finding{
			Detector:   det,
			Kind:       KindEventChannel,
			Importer:   "win-events",
			Display:    ch.name,
			Confidence: 70, // a well-known channel that opens for reading
			Shows:      ch.shows,
			// A channel has no file size to stat. The pull is capped at the
			// importer's limits (256 MiB, 2 M rows) and always uses a time window.
			Cost: CostMedium,
		})
	}
	return findings, gaps
}
