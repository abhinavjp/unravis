package store

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
	mongov1 "go.mongodb.org/mongo-driver/mongo"
	optionsv1 "go.mongodb.org/mongo-driver/mongo/options"
	bsonv2 "go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// adaptiveMongoHandle starts with the supported modern driver. It only selects
// the 3.4 adapter after an unauthenticated, read-only isMaster capability
// probe using the same URI transport options and TLS configuration proves
// maxWireVersion 5. Authentication, certificate, and network errors alone
// never select or downgrade the driver.
type adaptiveMongoHandle struct {
	mu              sync.RWMutex
	switchMu        sync.Mutex
	modern          clientHandle
	legacy          clientHandle
	uri             string
	tlsConfig       *tls.Config
	probeWire       func(context.Context, string, *tls.Config) (int32, error)
	newLegacyClient func(context.Context, string, *tls.Config) (clientHandle, error)
}

func newAdaptiveMongoHandle(uri string, tlsConfig *tls.Config, modern clientHandle) *adaptiveMongoHandle {
	var cloned *tls.Config
	if tlsConfig != nil {
		cloned = tlsConfig.Clone()
	}
	return &adaptiveMongoHandle{
		modern: modern, uri: uri, tlsConfig: cloned,
		probeWire: probeMongoWireVersion,
		newLegacyClient: func(ctx context.Context, uri string, config *tls.Config) (clientHandle, error) {
			return newLegacyMongoHandle(ctx, uri, config)
		},
	}
}

func (a *adaptiveMongoHandle) current() clientHandle {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.legacy != nil {
		return a.legacy
	}
	return a.modern
}

func (a *adaptiveMongoHandle) isLegacy() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.legacy != nil
}

// usesLegacyMongo is the query layer's signal to decode the bounded raw
// metadata stream produced by rewriteLegacyPipeline.
func usesLegacyMongo(client clientHandle) bool {
	legacy, ok := client.(interface{ isLegacy() bool })
	return ok && legacy.isLegacy()
}

func (a *adaptiveMongoHandle) switchToLegacy(ctx context.Context, original error) error {
	if a.isLegacy() {
		return nil
	}
	// Preserve explicit credential and TLS failures. A capability check is a
	// compatibility selector, never a reason to retry a rejected login or
	// bypass certificate verification.
	var classified *audit.ConnectionError
	if errors.As(categorizeConnectionError("connection", original), &classified) &&
		(classified.Stage == "authentication" || classified.Stage == "tls") {
		return original
	}
	a.switchMu.Lock()
	defer a.switchMu.Unlock()
	if a.isLegacy() {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return original
	}
	wireVersion, err := a.probeWire(ctx, a.uri, a.tlsConfig)
	if err != nil || wireVersion != 5 {
		return original
	}
	legacy, err := a.newLegacyClient(ctx, a.uri, a.tlsConfig)
	if err != nil {
		return original
	}
	a.mu.Lock()
	if a.legacy != nil {
		a.mu.Unlock()
		_ = legacy.Close(ctx)
		return nil
	}
	modern := a.modern
	a.legacy = legacy
	a.mu.Unlock()
	if modern != nil {
		closeClientWithTimeout(modern)
	}
	return nil
}

func (a *adaptiveMongoHandle) Probe(ctx context.Context, database, collection string) (string, string, error) {
	if a.isLegacy() {
		return a.current().Probe(ctx, database, collection)
	}
	version, warning, err := a.modern.Probe(ctx, database, collection)
	if err == nil {
		return version, warning, nil
	}
	if switchErr := a.switchToLegacy(ctx, err); switchErr != nil {
		return "", "", err
	}
	return a.current().Probe(ctx, database, collection)
}

func (a *adaptiveMongoHandle) Ping(ctx context.Context) error {
	if a.isLegacy() {
		return a.current().(interface{ Ping(context.Context) error }).Ping(ctx)
	}
	modernTester, ok := a.modern.(interface{ Ping(context.Context) error })
	if !ok {
		return errors.New("MongoDB client does not support ping")
	}
	if err := modernTester.Ping(ctx); err != nil {
		if switchErr := a.switchToLegacy(ctx, err); switchErr != nil {
			return err
		}
		return a.current().(interface{ Ping(context.Context) error }).Ping(ctx)
	}
	return nil
}

func (a *adaptiveMongoHandle) ListDatabases(ctx context.Context) ([]string, error) {
	if a.isLegacy() {
		return a.current().(interface {
			ListDatabases(context.Context) ([]string, error)
		}).ListDatabases(ctx)
	}
	modernLister, ok := a.modern.(interface {
		ListDatabases(context.Context) ([]string, error)
	})
	if !ok {
		return nil, errors.New("MongoDB client does not support database listing")
	}
	names, err := modernLister.ListDatabases(ctx)
	if err != nil {
		if switchErr := a.switchToLegacy(ctx, err); switchErr != nil {
			return nil, err
		}
		return a.current().(interface {
			ListDatabases(context.Context) ([]string, error)
		}).ListDatabases(ctx)
	}
	return names, nil
}

func (a *adaptiveMongoHandle) Aggregate(ctx context.Context, database, collection string, pipeline mongo.Pipeline) (mongoCursor, error) {
	if a.isLegacy() {
		return a.current().Aggregate(ctx, database, collection, pipeline)
	}
	cursor, err := a.modern.Aggregate(ctx, database, collection, pipeline)
	if err == nil {
		return cursor, nil
	}
	if switchErr := a.switchToLegacy(ctx, err); switchErr != nil {
		return nil, err
	}
	return a.current().Aggregate(ctx, database, collection, pipeline)
}

func (a *adaptiveMongoHandle) Close(ctx context.Context) error {
	return a.current().Close(ctx)
}

// probeMongoWireVersion uses only the legacy driver's read-only handshake
// command. Userinfo is stripped; URI query transport options and typed TLS
// configuration are preserved exactly.
func probeMongoWireVersion(ctx context.Context, uri string, tlsConfig *tls.Config) (int32, error) {
	probeURI, err := withoutMongoCredentials(uri)
	if err != nil {
		return 0, err
	}
	clientOptions := legacyClientOptions(probeURI, tlsConfig).SetMaxPoolSize(1).SetMaxConnIdleTime(0)
	client, err := mongov1.Connect(ctx, clientOptions)
	if err != nil {
		return 0, err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = client.Disconnect(closeCtx)
	}()
	var response struct {
		MaxWireVersion int32 `bson:"maxWireVersion"`
	}
	err = client.Database("admin").RunCommand(ctx, bson.D{{Key: "isMaster", Value: 1}}).Decode(&response)
	if err != nil {
		return 0, err
	}
	return response.MaxWireVersion, nil
}

func withoutMongoCredentials(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil || (u.Scheme != "mongodb" && u.Scheme != "mongodb+srv") || u.Host == "" {
		return "", errors.New("invalid MongoDB URI")
	}
	u.User = nil
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", errors.New("invalid MongoDB URI")
	}
	for key := range query {
		switch strings.ToLower(key) {
		case "authsource", "authmechanism", "authmechanismproperties":
			delete(query, key)
		}
	}
	u.RawQuery = query.Encode()
	return u.String(), nil
}

type legacyMongoHandle struct{ client *mongov1.Client }

func newLegacyMongoHandle(ctx context.Context, uri string, tlsConfig *tls.Config) (*legacyMongoHandle, error) {
	return newLegacyMongoHandleWithMonitor(ctx, uri, tlsConfig, nil)
}

func newLegacyMongoHandleWithMonitor(ctx context.Context, uri string, tlsConfig *tls.Config, monitor *event.CommandMonitor) (*legacyMongoHandle, error) {
	clientOptions := legacyClientOptions(uri, tlsConfig)
	if monitor != nil {
		clientOptions.SetMonitor(monitor)
	}
	client, err := mongov1.Connect(ctx, clientOptions)
	if err != nil {
		return nil, err
	}
	return &legacyMongoHandle{client: client}, nil
}

// legacyClientOptions mirrors mongoClientOptions for the v1 driver: the URI is
// applied first and the app name and timeouts always win.
func legacyClientOptions(uri string, tlsConfig *tls.Config) *optionsv1.ClientOptions {
	clientOptions := optionsv1.Client().ApplyURI(uri).
		SetAppName(appName()).
		SetConnectTimeout(audit.ConnectTimeout).
		SetServerSelectionTimeout(audit.ServerSelectionTimeout).
		SetMaxPoolSize(4).
		SetMinPoolSize(0).
		SetMaxConnIdleTime(2 * time.Minute)
	if tlsConfig != nil {
		clientOptions.SetTLSConfig(tlsConfig.Clone())
	}
	return clientOptions
}

func (m *legacyMongoHandle) isLegacy() bool { return true }

func isMongo34(version string) bool {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return false
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	return majorErr == nil && minorErr == nil && major == 3 && minor == 4
}

func (m *legacyMongoHandle) Probe(ctx context.Context, database, collection string) (string, string, error) {
	if err := m.client.Ping(ctx, nil); err != nil {
		return "", "", err
	}
	var build struct {
		Version string `bson:"version"`
	}
	if err := m.client.Database("admin").RunCommand(ctx, bson.D{{Key: "buildInfo", Value: 1}}).Decode(&build); err != nil {
		return "", "", err
	}
	if !isMongo34(build.Version) {
		return build.Version, "", fmt.Errorf("unsupported MongoDB server version")
	}
	readProbe := m.client.Database(database).Collection(collection).FindOne(ctx, bson.D{}, optionsv1.FindOne().SetProjection(bson.D{{Key: "_id", Value: 1}}))
	if err := readProbe.Err(); err != nil && !errors.Is(err, mongov1.ErrNoDocuments) {
		return build.Version, "", err
	}
	warning := ""
	indexes, err := m.client.Database(database).Collection(collection).Indexes().List(ctx)
	if err != nil {
		warning = "Could not inspect indexes; verify RequestInitiatedAt or CreatedOn is indexed."
	} else {
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = indexes.Close(closeCtx)
		}()
		found := false
		for indexes.Next(ctx) {
			var index struct {
				Keys bson.D `bson:"key"`
			}
			if indexes.Decode(&index) != nil {
				continue
			}
			for _, key := range index.Keys {
				if key.Key == "RequestInitiatedAt" || key.Key == "CreatedOn" {
					found = true
				}
			}
		}
		if indexes.Err() != nil || !found {
			warning = "No timing index was confirmed; queries may be slow. Add an index on RequestInitiatedAt (or legacy CreatedOn)."
		}
	}
	return build.Version, warning, nil
}

func (m *legacyMongoHandle) Ping(ctx context.Context) error { return m.client.Ping(ctx, nil) }

func (m *legacyMongoHandle) ListDatabases(ctx context.Context) ([]string, error) {
	// MongoDB 3.4 predates authorizedDatabases/nameOnly options. The command's
	// normal permission filtering still returns only databases visible to the
	// authenticated principal.
	return m.client.ListDatabaseNames(ctx, bson.D{})
}

func (m *legacyMongoHandle) Aggregate(ctx context.Context, database, collection string, pipeline mongo.Pipeline) (mongoCursor, error) {
	cmdCtx, maxTime, cancel, err := beginLegacyCommand(ctx)
	if err != nil {
		return nil, err
	}
	rewritten, err := rewriteLegacyPipeline(pipeline)
	if err != nil {
		cancel()
		return nil, err
	}
	legacyPipeline := make(mongov1.Pipeline, 0, len(rewritten))
	for _, stage := range rewritten {
		encoded, err := bsonv2.Marshal(stage)
		if err != nil {
			cancel()
			return nil, errors.New("invalid legacy aggregation stage")
		}
		var legacyStage bson.D
		if err := bson.Unmarshal(encoded, &legacyStage); err != nil {
			cancel()
			return nil, errors.New("invalid legacy aggregation stage")
		}
		legacyPipeline = append(legacyPipeline, legacyStage)
	}
	cursor, err := m.client.Database(database).Collection(collection).Aggregate(cmdCtx, legacyPipeline, optionsv1.Aggregate().SetAllowDiskUse(false).SetBatchSize(100).SetMaxTime(maxTime))
	if err != nil {
		cancel()
		return nil, err
	}
	return wrapLegacyCursor(&legacyMongoCursor{cursor: cursor}, cmdCtx, cancel), nil
}

// beginLegacyCommand fixes the budget of one legacy aggregate: the maxTimeMS it
// is sent with, and a context that ends at now+maxTime. MongoDB 3.4 has no
// per-getMore maxTimeMS the driver can derive from a deadline, so the deadline
// is also enforced on the client for every later batch (see wrapLegacyCursor).
func beginLegacyCommand(ctx context.Context) (context.Context, time.Duration, context.CancelFunc, error) {
	maxTime, err := remainingLegacyMaxTime(ctx)
	if err != nil {
		return nil, 0, nil, err
	}
	cmdCtx, cancel := context.WithTimeout(ctx, maxTime)
	return cmdCtx, maxTime, cancel, nil
}

// wrapLegacyCursor keeps every getMore inside the command deadline and releases
// the command context when the cursor is closed, as the modern driver path does.
func wrapLegacyCursor(inner mongoCursor, cmdCtx context.Context, cancel context.CancelFunc) mongoCursor {
	deadline, _ := cmdCtx.Deadline()
	return &budgetCursor{inner: inner, deadline: deadline, cancel: cancel}
}

func remainingLegacyMaxTime(ctx context.Context) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	remaining := queryBudget(ctx)
	if deadline, ok := ctx.Deadline(); ok {
		until := time.Until(deadline)
		if until <= 0 {
			return 0, context.DeadlineExceeded
		}
		if until < remaining {
			remaining = until
		}
	}
	// MongoDB accepts millisecond precision. A positive sub-millisecond
	// deadline must not serialize as maxTimeMS: 0 (unbounded).
	if remaining < time.Millisecond {
		remaining = time.Millisecond
	}
	return remaining, nil
}

func (m *legacyMongoHandle) Close(ctx context.Context) error { return m.client.Disconnect(ctx) }

type legacyMongoCursor struct{ cursor *mongov1.Cursor }

func (c *legacyMongoCursor) Next(ctx context.Context) bool { return c.cursor.Next(ctx) }

func (c *legacyMongoCursor) Decode(target any) error {
	return bsonv2.Unmarshal([]byte(c.cursor.Current), target)
}

func (c *legacyMongoCursor) Err() error { return c.cursor.Err() }

func (c *legacyMongoCursor) Close(ctx context.Context) error { return c.cursor.Close(ctx) }
