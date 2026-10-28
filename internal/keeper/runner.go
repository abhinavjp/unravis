package keeper

import (
	"context"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/dbhealth"
	"api-audit-explorer/internal/store"
)

// RealRunner reaches real servers with the drivers Unravis itself uses (dbhealth for SQL Server, internal/store for MongoDB). Only the
// keeper service builds one.
type RealRunner struct {
	store *store.Store
}

// NewRealRunner makes the runner the keeper service uses.
func NewRealRunner() *RealRunner { return &RealRunner{store: store.New()} }

type commandFunc store.MongoCommandFunc

func (f commandFunc) Command(ctx context.Context, database, commandJSON string) (map[string]any, error) {
	return f(ctx, database, commandJSON)
}

func (r *RealRunner) TestSQL(ctx context.Context, t dbhealth.SQLTarget) (dbhealth.TestResult, error) {
	return dbhealth.TestSQL(ctx, t)
}

func (r *RealRunner) HealthSQL(ctx context.Context, t dbhealth.SQLTarget, label string) (dbhealth.Report, error) {
	c, err := dbhealth.OpenSQL(ctx, t)
	if err != nil {
		return dbhealth.Report{}, dbhealth.ClassifyConnect(err)
	}
	defer c.Close()
	return dbhealth.RunSQL(ctx, c, label, t.Database, time.Now), nil
}

func (r *RealRunner) LaneSQL(ctx context.Context, t dbhealth.SQLTarget, label string, req dbhealth.LaneRequest) (dbhealth.Report, error) {
	c, err := dbhealth.OpenSQL(ctx, t)
	if err != nil {
		return dbhealth.Report{}, dbhealth.ClassifyConnect(err)
	}
	defer c.Close()
	return dbhealth.RunSQLLane(ctx, c, label, req, time.Now), nil
}

func (r *RealRunner) TestMongo(ctx context.Context, in audit.ConnectionInput) (audit.ConnectionResult, error) {
	return r.store.TestConnection(ctx, in)
}

func (r *RealRunner) ValidateMongo(in audit.ConnectionInput) error {
	return r.store.ValidateConnectionSettings(in)
}

func (r *RealRunner) HealthMongo(ctx context.Context, in audit.ConnectionInput, database, collection string) (dbhealth.Report, error) {
	run, closeFn, err := r.store.OpenCommandClient(ctx, in)
	if err != nil {
		return dbhealth.Report{}, err
	}
	defer closeFn()
	return dbhealth.RunMongo(ctx, commandFunc(run), "MongoDB", database, collection, time.Now), nil
}

func (r *RealRunner) LaneMongo(ctx context.Context, in audit.ConnectionInput, label string, req dbhealth.LaneRequest) (dbhealth.Report, error) {
	run, closeFn, err := r.store.OpenCommandClient(ctx, in)
	if err != nil {
		return dbhealth.Report{}, err
	}
	defer closeFn()
	return dbhealth.RunMongoLane(ctx, commandFunc(run), label, req, time.Now), nil
}
