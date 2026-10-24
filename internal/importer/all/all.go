// Package all builds the registry holding every importer the app ships.
package all

import (
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/appinsights"
	"api-audit-explorer/internal/importer/iis"
	"api-audit-explorer/internal/importer/mongodlog"
	"api-audit-explorer/internal/importer/schedule"
	"api-audit-explorer/internal/importer/sqlserver"
	"api-audit-explorer/internal/importer/wincounters"
	"api-audit-explorer/internal/importer/winevents"
)

// Importers returns a fresh instance of every importer. win-events carries the
// IIS WAS and Task Scheduler classifiers, so those channels get typed kinds.
func Importers() []importer.Importer {
	return []importer.Importer{
		iis.NewW3C(),
		iis.NewHTTPERR(),
		wincounters.New(),
		winevents.New(iis.ClassifyWAS, winevents.ClassifyTaskScheduler),
		schedule.NewHangfire(),
		schedule.NewAgent(),
		appinsights.New(),
		mongodlog.New(),
		sqlserver.NewXE(),
		sqlserver.NewErrorlog(),
		sqlserver.NewPDH(),
	}
}

// Registry is the registry of every shipped importer.
func Registry() *importer.Registry { return importer.NewRegistry(Importers()...) }
