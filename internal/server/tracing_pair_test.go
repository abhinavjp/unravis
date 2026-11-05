package server

import (
	"reflect"
	"strings"
	"testing"

	"api-audit-explorer/internal/casestore"
)

func TestPairFileTags(t *testing.T) {
	ok := casestore.StatusOK
	cases := []struct {
		name    string
		tags    []casestore.SourceTag
		imports []casestore.ImportRecord
		want    map[int64]int64
	}{
		{
			name:    "a: one tag and one file of the same name pair",
			tags:    []casestore.SourceTag{{ID: 1, Kind: "iis-w3c", Label: "u_ex261021.log"}},
			imports: []casestore.ImportRecord{{ID: 10, Importer: "iis-w3c", FileName: "u_ex261021.log", Status: ok}},
			want:    map[int64]int64{1: 10},
		},
		{
			name: "b: two tags and two files pair each by name",
			tags: []casestore.SourceTag{
				{ID: 1, Kind: "iis-w3c", Label: "a.log"},
				{ID: 2, Kind: "iis-w3c", Label: "b.log"},
			},
			imports: []casestore.ImportRecord{
				{ID: 10, Importer: "iis-w3c", FileName: "b.log", Status: ok},
				{ID: 11, Importer: "iis-w3c", FileName: "a.log", Status: ok},
			},
			want: map[int64]int64{1: 11, 2: 10},
		},
		{
			name:    "c: a rejected import of the same name is not paired",
			tags:    []casestore.SourceTag{{ID: 1, Kind: "iis-w3c", Label: "a.log"}},
			imports: []casestore.ImportRecord{{ID: 10, Importer: "iis-w3c", FileName: "a.log", Status: casestore.StatusQuarantined}},
			want:    map[int64]int64{},
		},
		{
			name:    "d: a mongo database tag is never paired",
			tags:    []casestore.SourceTag{{ID: 1, Kind: mongoDatabaseKind, Label: "a.log"}},
			imports: []casestore.ImportRecord{{ID: 10, Importer: mongoDatabaseKind, FileName: "a.log", Status: ok}},
			want:    map[int64]int64{},
		},
		{
			name: "e: two same-kind tags and two same-kind files with no name match pair nothing",
			tags: []casestore.SourceTag{
				{ID: 1, Kind: "iis-w3c", Label: "IIS one"},
				{ID: 2, Kind: "iis-w3c", Label: "IIS two"},
			},
			imports: []casestore.ImportRecord{
				{ID: 10, Importer: "iis-w3c", FileName: "x.log", Status: ok},
				{ID: 11, Importer: "iis-w3c", FileName: "y.log", Status: ok},
			},
			want: map[int64]int64{},
		},
		{
			name:    "f: one tag and one file of the same kind pair by kind",
			tags:    []casestore.SourceTag{{ID: 1, Kind: "iis-w3c", Label: "Web server logs"}},
			imports: []casestore.ImportRecord{{ID: 10, Importer: "iis-w3c", FileName: "u_ex261021.log", Status: ok}},
			want:    map[int64]int64{1: 10},
		},
		{
			name: "g: a linked tag is untouched and its import is not given away",
			tags: []casestore.SourceTag{
				{ID: 1, Kind: "iis-w3c", Label: "a.log", ImportID: 10},
				{ID: 2, Kind: "iis-w3c", Label: "a.log"},
			},
			imports: []casestore.ImportRecord{{ID: 10, Importer: "iis-w3c", FileName: "a.log", Status: ok}},
			want:    map[int64]int64{},
		},
		{
			name: "h: duplicate file names pair nothing by name",
			tags: []casestore.SourceTag{{ID: 1, Kind: "iis-w3c", Label: "a.log"}},
			imports: []casestore.ImportRecord{
				{ID: 10, Importer: "iis-w3c", FileName: "a.log", Status: ok},
				{ID: 11, Importer: "iis-w3c", FileName: "a.log", Status: ok},
			},
			want: map[int64]int64{},
		},
		{
			name: "cut label of 60 runes matches the longer file name",
			tags: []casestore.SourceTag{{ID: 1, Kind: "iis-w3c", Label: strings.Repeat("a", 60)}},
			imports: []casestore.ImportRecord{
				{ID: 10, Importer: "iis-w3c", FileName: strings.Repeat("a", 60) + ".log", Status: ok},
			},
			want: map[int64]int64{1: 10},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pairFileTags(tc.tags, tc.imports)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("pairFileTags = %v, want %v", got, tc.want)
			}
		})
	}
}
