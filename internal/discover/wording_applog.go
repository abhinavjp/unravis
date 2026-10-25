package discover

// Wording keys the applog detector uses. The sentences live in wording.go, where
// the golden test pins them; this file only names the keys so the detector has
// no string literals for them.
const (
	wordLog4netShows     = "discover.log4net.shows"
	wordNLogShows        = "discover.nlog.shows"
	wordSerilogShows     = "discover.serilog.shows"
	wordMELShows         = "discover.mel.shows"
	wordAppInsightsShows = "discover.appinsights.shows"
	wordAppInsightsHow   = "discover.appinsights.how"
	wordHangfireShows    = "discover.hangfire.shows"
	wordHangfireHow      = "discover.hangfire.how"
	wordQuartzShows      = "discover.quartz.shows"
	wordQuartzHow        = "discover.quartz.how"
)
