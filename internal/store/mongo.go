package store

import (
	"api-audit-explorer/internal/audit"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type mongoHandle struct{ client *mongo.Client }

func defaultClientFactory(ctx context.Context, uri string, tlsConfig *tls.Config) (clientHandle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	clientOptions := options.Client().ApplyURI(uri).
		SetMaxPoolSize(4).
		SetMinPoolSize(0).
		SetConnectTimeout(audit.OperationTimeout).
		SetServerSelectionTimeout(audit.OperationTimeout).
		SetMaxConnIdleTime(2 * time.Minute)
	if tlsConfig != nil {
		clientOptions.SetTLSConfig(tlsConfig.Clone())
	}
	client, err := mongo.Connect(clientOptions)
	if err != nil {
		return nil, mongoClientSetupFailure(err)
	}
	modern := &mongoHandle{client: client}
	return newAdaptiveMongoHandle(uri, tlsConfig, modern), nil
}

func (m *mongoHandle) Probe(ctx context.Context, database, collection string) (string, string, error) {
	if err := m.client.Ping(ctx, nil); err != nil {
		return "", "", err
	}
	var build struct {
		Version string `bson:"version"`
	}
	if err := m.client.Database("admin").RunCommand(ctx, bson.D{{Key: "buildInfo", Value: 1}}).Decode(&build); err != nil {
		return "", "", err
	}
	if !supportedMongoVersion(build.Version) {
		return build.Version, "", fmt.Errorf("unsupported MongoDB server version")
	}
	readProbe := m.client.Database(database).Collection(collection).FindOne(ctx, bson.D{}, options.FindOne().SetProjection(bson.D{{Key: "_id", Value: 1}}))
	if err := readProbe.Err(); err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
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

func (m *mongoHandle) Ping(ctx context.Context) error {
	return m.client.Ping(ctx, nil)
}

func (m *mongoHandle) ListDatabases(ctx context.Context) ([]string, error) {
	return m.client.ListDatabaseNames(ctx, bson.D{}, options.ListDatabases().SetAuthorizedDatabases(true).SetNameOnly(true))
}

func (m *mongoHandle) Aggregate(ctx context.Context, database, collection string, pipeline mongo.Pipeline) (mongoCursor, error) {
	// The driver derives maxTimeMS from the context deadline. Including it in
	// the raw command as well causes a duplicate-field server error.
	command := aggregateCommand(collection, pipeline)
	cursor, err := m.client.Database(database).RunCommandCursor(ctx, command)
	if err != nil {
		return nil, err
	}
	return cursor, nil
}

func aggregateCommand(collection string, pipeline mongo.Pipeline) bson.D {
	return bson.D{
		{Key: "aggregate", Value: collection},
		{Key: "pipeline", Value: pipeline},
		{Key: "cursor", Value: bson.D{{Key: "batchSize", Value: int32(100)}}},
		{Key: "allowDiskUse", Value: false},
	}
}

func (m *mongoHandle) Close(ctx context.Context) error { return m.client.Disconnect(ctx) }

func supportedMongoVersion(version string) bool {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	return major > 4 || (major == 4 && minor >= 4)
}
