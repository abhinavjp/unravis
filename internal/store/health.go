package store

import (
	"context"
	"encoding/json"
	"errors"

	"api-audit-explorer/internal/audit"
	bsonv1 "go.mongodb.org/mongo-driver/bson"
	bsonv2 "go.mongodb.org/mongo-driver/v2/bson"
)

// MongoCommandFunc runs one database command given as relaxed extended JSON and returns the reply as a JSON-shaped map. It does no checking of its own:
// the caller (internal/dbhealth) only passes commands from its fixed, read-only list.
type MongoCommandFunc func(ctx context.Context, database, commandJSON string) (map[string]any, error)

type commandRunner interface {
	RunCommand(ctx context.Context, database, commandJSON string) (map[string]any, error)
}

// OpenCommandClient opens a temporary connection for health checks. The returned close function must be called; it ends the connection and gives the
// connection slot back. Nothing is added to the saved sources.
func (s *Store) OpenCommandClient(ctx context.Context, input audit.ConnectionInput) (MongoCommandFunc, func(), error) {
	if err := s.ValidateConnectionSettings(input); err != nil {
		return nil, nil, err
	}
	uri, err := composeMongoURI(input)
	if err != nil {
		return nil, nil, sanitizedValidationError(err)
	}
	if err := acquire(ctx, s.global); err != nil {
		return nil, nil, err
	}
	release := func() { <-s.global }
	tlsConfig, err := buildTLSConfig(input.TLSMode, input.CACertificate)
	if err != nil {
		release()
		return nil, nil, err
	}
	client, err := s.newClient(ctx, uri, tlsConfig)
	if err != nil {
		release()
		return nil, nil, connectionTestFailure(ctx, "connection", err)
	}
	closer := func() { closeClientWithTimeout(client); release() }
	tester, ok := client.(connectionTestClient)
	runner, ok2 := client.(commandRunner)
	if !ok || !ok2 {
		closer()
		return nil, nil, &audit.ConnectionError{Stage: "connection", Code: "network_failure", Message: "The MongoDB connection could not be tested."}
	}
	if err := tester.Ping(ctx); err != nil {
		closer()
		return nil, nil, connectionTestFailure(ctx, "connection", err)
	}
	return runner.RunCommand, closer, nil
}

func (m *mongoHandle) RunCommand(ctx context.Context, database, commandJSON string) (map[string]any, error) {
	var cmd bsonv2.D
	if err := bsonv2.UnmarshalExtJSON([]byte(commandJSON), false, &cmd); err != nil {
		return nil, err
	}
	raw, err := m.client.Database(database).RunCommand(ctx, cmd).Raw()
	if err != nil {
		return nil, err
	}
	out, err := bsonv2.MarshalExtJSON(raw, false, false)
	if err != nil {
		return nil, err
	}
	return decodeReply(out)
}

func (m *legacyMongoHandle) RunCommand(ctx context.Context, database, commandJSON string) (map[string]any, error) {
	var cmd bsonv1.D
	if err := bsonv1.UnmarshalExtJSON([]byte(commandJSON), false, &cmd); err != nil {
		return nil, err
	}
	raw, err := m.client.Database(database).RunCommand(ctx, cmd).DecodeBytes()
	if err != nil {
		return nil, err
	}
	out, err := bsonv1.MarshalExtJSON(raw, false, false)
	if err != nil {
		return nil, err
	}
	return decodeReply(out)
}

func (a *adaptiveMongoHandle) RunCommand(ctx context.Context, database, commandJSON string) (map[string]any, error) {
	run := func(h clientHandle) (map[string]any, error) {
		r, ok := h.(commandRunner)
		if !ok {
			return nil, errors.New("MongoDB client does not support commands")
		}
		return r.RunCommand(ctx, database, commandJSON)
	}
	if a.isLegacy() {
		return run(a.current())
	}
	out, err := run(a.modern)
	if err == nil {
		return out, nil
	}
	if switchErr := a.switchToLegacy(ctx, err); switchErr != nil {
		return nil, err
	}
	return run(a.current())
}

func decodeReply(b []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}
