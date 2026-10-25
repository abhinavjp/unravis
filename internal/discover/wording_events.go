package discover

// Wording for the event-channel detector. These keys live in their own file so
// the event task does not edit wording.go; they join the same table at start-up,
// so WordingKeys, the lint and the golden file cover them like any other key.
//
// The System channel and the Task Scheduler channel reuse the existing keys
// "discover.events.shows" and "discover.taskscheduler.shows".
const (
	showsEventsApp    = "discover.eventsapp.shows"
	showsEventsSystem = "discover.events.shows"
	showsTaskSched    = "discover.taskscheduler.shows"
)

func init() {
	shows[showsEventsApp] = "They record app crashes, hangs and start failures, with times."
}
