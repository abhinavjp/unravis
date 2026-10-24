// Package all builds the registry holding every importer the app ships.
package all

import (
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/appinsights"
	"api-audit-explorer/internal/importer/dotnetcounters"
	"api-audit-explorer/internal/importer/iis"
	"api-audit-explorer/internal/importer/log4net"
	"api-audit-explorer/internal/importer/mel"
	"api-audit-explorer/internal/importer/mongodlog"
	"api-audit-explorer/internal/importer/mongoexports"
	"api-audit-explorer/internal/importer/procsnap"
	"api-audit-explorer/internal/importer/redis"
	"api-audit-explorer/internal/importer/schedule"
	"api-audit-explorer/internal/importer/serilog"
	"api-audit-explorer/internal/importer/sqlserver"
	"api-audit-explorer/internal/importer/wincounters"
	"api-audit-explorer/internal/importer/winevents"
)

// Importers returns a fresh instance of every importer. win-events carries the
// IIS WAS, Windows Application log and Task Scheduler classifiers, so those
// channels get typed kinds.
func Importers() []importer.Importer {
	return []importer.Importer{
		iis.NewW3C(),
		iis.NewHTTPERR(),
		wincounters.New(),
		winevents.New(iis.ClassifyWAS, winevents.ClassifyApp, winevents.ClassifyTaskScheduler),
		schedule.NewHangfire(),
		schedule.NewAgent(),
		appinsights.New(),
		mongodlog.New(),
		sqlserver.NewXE(),
		sqlserver.NewErrorlog(),
		sqlserver.NewPDH(),
		procsnap.New(),
		dotnetcounters.New(),
		serilog.NewCLEF(),
		serilog.NewText(),
		redis.NewSlowlog(),
		redis.NewLatency(),
		redis.NewInfo(),
		redis.NewLog(),
		mongoexports.NewProfile(),
		mongoexports.NewCurrentOp(),
		mongoexports.NewServerStatus(),
		mongoexports.NewMongostat(),
		log4net.NewText(),
		log4net.NewXML(),
		mel.NewConsole(),
		mel.NewJSON(),
		mel.NewStdout(),
	}
}

// Registry is the registry of every shipped importer.
func Registry() *importer.Registry { return importer.NewRegistry(Importers()...) }
