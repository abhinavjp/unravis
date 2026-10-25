package discover_test

import (
	"context"
	"testing"

	"api-audit-explorer/internal/discover"
)

// TestDBDetectorsThroughRun runs the three detectors under Run, which refuses a
// finding whose wording keys are missing, and checks the marker stays out of the
// whole scan.
func TestDBDetectorsThroughRun(t *testing.T) {
	e, lines := redisHost(dbtLines("requirepass "+dbtMarker, `logfile C:\Redis\logs\redis.log`))
	dbtFile(e, `C:\Redis\logs\redis.log`, dbtRedisLog)
	dbtAddService(e, "MongoDB", "mongod.exe", `C:\Mongo\bin`)
	dbtFile(e, mongoCfgAt, "security:\n  keyFile: C:\\"+dbtMarker+"\nsystemLog:\n  path: C:\\Mongo\\log\\mongod.log\n")
	dbtFile(e, `C:\Mongo\log\mongod.log`, dbtMongoLog)
	lines["MongoDB"] = mongoSvcCmd
	sqlInstance(e, "MSSQLSERVER", "MSSQLSERVER", "MSSQL15.MSSQLSERVER", sqlDefaultArgs(sqlLogDir)...)
	sqlFillDir(e, sqlLogDir)

	scan := discover.Run(context.Background(), dbtEnv(e, lines),
		[]discover.Detector{discover.RedisDetector{}, discover.MongoDetector{}, discover.SQLDetector{}})
	if !scan.Complete || len(scan.Gaps) != 0 || len(scan.Findings) != 5 {
		t.Fatalf("complete=%v findings=%d gaps=%+v", scan.Complete, len(scan.Findings), scan.Gaps)
	}
	for _, f := range scan.Findings {
		if f.ID == "" {
			t.Errorf("finding %q has no ID", f.Display)
		}
	}
	dbtAssertClean(t, scan.Findings, scan.Gaps)
}
