package redis

import (
	"testing"

	"api-audit-explorer/internal/importer/importertest"
)

func TestConformanceSlowlog(t *testing.T) { importertest.Run(t, NewSlowlog(), slowFixtures()) }
func TestConformanceLatency(t *testing.T) { importertest.Run(t, NewLatency(), latFixtures()) }
func TestConformanceInfo(t *testing.T)    { importertest.Run(t, NewInfo(), infoFixtures()) }

func FuzzSlowlog(f *testing.F) { importertest.FuzzParse(f, NewSlowlog(), slowFixtures()) }
func FuzzLatency(f *testing.F) { importertest.FuzzParse(f, NewLatency(), latFixtures()) }
func FuzzInfo(f *testing.F)    { importertest.FuzzParse(f, NewInfo(), infoFixtures()) }
