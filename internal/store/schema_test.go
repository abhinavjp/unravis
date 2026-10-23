package store

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// censusDoc builds one projected census document: the pipeline emits, per
// sampled document, the list of its top-level field names and BSON types.
func censusDoc(t *testing.T, fields ...[2]string) bson.Raw {
	t.Helper()
	list := bson.A{}
	for _, f := range fields {
		list = append(list, bson.D{{Key: "k", Value: f[0]}, {Key: "t", Value: f[1]}})
	}
	raw, err := bson.Marshal(bson.D{{Key: "f", Value: list}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type legacyFakeClient struct{ *fakeClient }

func (legacyFakeClient) isLegacy() bool { return true }

func TestSchemaCensusCountsFieldPresence(t *testing.T) {
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	source := addFakeSource(t, s, "audit")
	var gotPipeline mongo.Pipeline
	handle.aggregateFn = func(_ context.Context, db, collection string, pipeline mongo.Pipeline) (mongoCursor, error) {
		gotPipeline = pipeline
		if db != "db" || collection != "audit" {
			t.Errorf("sampled %s.%s", db, collection)
		}
		return &fakeCursor{docs: []any{
			censusDoc(t, [2]string{"UserId", "string"}, [2]string{"ApiTime", "int"}, [2]string{"Extra", "string"}),
			censusDoc(t, [2]string{"ApiTime", "double"}, [2]string{"Extra", "string"}),
			censusDoc(t, [2]string{"UserId", "objectId"}, [2]string{"ApiTime", "int"}),
		}}, nil
	}

	got, err := s.SampleSchema(context.Background(), source.ID, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := audit.SchemaCensus{Sampled: 3, Fields: []audit.FieldPresence{
		{Name: "ApiTime", Present: 3, Types: []string{"double", "int"}},
		{Name: "Extra", Present: 2, Types: []string{"string"}},
		{Name: "UserId", Present: 2, Types: []string{"objectId", "string"}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("census = %#v\nwant     %#v", got, want)
	}
	if len(gotPipeline) < 2 || gotPipeline[0][0].Key != "$sample" || gotPipeline[len(gotPipeline)-1][0].Key != "$project" {
		t.Fatalf("pipeline = %#v, want $sample then $project", gotPipeline)
	}
}

func TestSchemaCensusNeverReturnsValuesAndProjectsOnlyNamesAndTypes(t *testing.T) {
	pipeline := schemaPipeline(10, false)
	encoded, err := bson.MarshalExtJSON(bson.D{{Key: "p", Value: pipeline}}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	var tree any
	if err := json.Unmarshal(encoded, &tree); err != nil {
		t.Fatal(err)
	}
	// Any reference to a field value ($$e.v) must sit inside $type, so that
	// only the type name can ever leave the server.
	var walk func(node any, insideType bool)
	walk = func(node any, insideType bool) {
		switch v := node.(type) {
		case map[string]any:
			for k, child := range v {
				walk(child, insideType || k == "$type")
			}
		case []any:
			for _, child := range v {
				walk(child, insideType)
			}
		case string:
			if v == "$$e.v" && !insideType {
				t.Errorf("value reference %q outside $type", v)
			}
		}
	}
	walk(tree, false)
}

func TestSchemaCensusCapsSampleSizeAndFallsBackToLimitOnLegacy(t *testing.T) {
	tests := []struct {
		name      string
		legacy    bool
		n         int
		wantStage string
		wantSize  int32
	}{
		{"modern default", false, 0, "$sample", audit.DefaultSchemaSample},
		{"modern negative", false, -4, "$sample", audit.DefaultSchemaSample},
		{"modern capped", false, 50000, "$sample", audit.MaxSchemaSample},
		{"modern exact", false, 37, "$sample", 37},
		{"legacy capped", true, 5000, "$limit", audit.MaxSchemaSample},
		{"legacy exact", true, 12, "$limit", 12},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var handle *fakeClient
			s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
				handle = &fakeClient{}
				if tc.legacy {
					return legacyFakeClient{handle}, nil
				}
				return handle, nil
			})
			source := addFakeSource(t, s, "audit")
			var first bson.E
			handle.aggregateFn = func(_ context.Context, _, _ string, pipeline mongo.Pipeline) (mongoCursor, error) {
				first = pipeline[0][0]
				return &fakeCursor{}, nil
			}
			got, err := s.SampleSchema(context.Background(), source.ID, tc.n)
			if err != nil {
				t.Fatal(err)
			}
			if got.Sampled != 0 || got.Fields == nil || len(got.Fields) != 0 {
				t.Fatalf("empty sample = %#v", got)
			}
			if first.Key != tc.wantStage {
				t.Fatalf("first stage = %s, want %s", first.Key, tc.wantStage)
			}
			var size int64
			switch v := first.Value.(type) {
			case int:
				size = int64(v)
			case int32:
				size = int64(v)
			case int64:
				size = v
			case bson.D:
				size = int64(v[0].Value.(int32))
			default:
				t.Fatalf("stage value %T", first.Value)
			}
			if size != int64(tc.wantSize) {
				t.Fatalf("size = %d, want %d", size, tc.wantSize)
			}
		})
	}
}

func TestSchemaCensusUsesAdmissionAndReportsSanitizedFailures(t *testing.T) {
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	source := addFakeSource(t, s, "audit")
	if _, err := s.SampleSchema(context.Background(), "", 5); !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("blank source id error = %v", err)
	}
	if _, err := s.SampleSchema(context.Background(), "missing", 5); !errors.Is(err, audit.ErrNotFound) {
		t.Fatalf("unknown source error = %v", err)
	}
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return nil, errors.New("mongodb://reader:secret@host:27017/db: network error")
	}
	_, err := s.SampleSchema(context.Background(), source.ID, 5)
	if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "mongodb://") {
		t.Fatalf("driver error leaked or missing: %v", err)
	}
	for range cap(s.global) {
		s.global <- struct{}{}
	}
	defer func() {
		for range cap(s.global) {
			<-s.global
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.SampleSchema(ctx, source.ID, 5); !errors.Is(err, audit.ErrBusy) {
		t.Fatalf("busy global capacity error = %v", err)
	}
}
