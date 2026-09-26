package contract

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// icarFixture is a minimal stand-in for the ICAR bundle: two operations, a
// transitive schema chain, an unreferenced schema and a nullable date.
const icarFixture = `{
  "openapi": "3.1.0",
  "info": {"title": "ICAR", "version": "1.5"},
  "servers": [{"url": "https://icar.example"}],
  "paths": {
    "/locations/{l}/animals": {
      "get": {
        "parameters": [{"$ref": "#/components/parameters/l"}],
        "responses": {"200": {"description": "ok", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/icarAnimal"}}}}}
      },
      "post": {"responses": {"201": {"description": "created"}}}
    },
    "/locations/{l}/weights": {
      "get": {"responses": {"200": {"description": "ok", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/icarWeight"}}}}}}
    }
  },
  "components": {
    "parameters": {"l": {"name": "l", "in": "path", "required": true, "schema": {"type": "string"}}},
    "schemas": {
      "icarAnimal": {"type": "object", "required": ["id"], "properties": {"id": {"type": "string"}, "meta": {"$ref": "#/components/schemas/icarMeta"}}},
      "icarMeta": {"type": "object", "properties": {"created": {"description": "when", "anyOf": [{"type": "null"}, {"$ref": "#/components/schemas/icarDateTime"}]}}},
      "icarDateTime": {"description": "UTC", "type": "string", "format": "date-time"},
      "icarWeight": {"type": "object", "properties": {"kg": {"type": "number"}}},
      "icarUnused": {"type": "string"}
    }
  }
}`

const extFixture = `
x-agritelem-contract:
  icarOperations:
    /locations/{l}/animals: [get]
openapi: 3.1.0
info: {title: Agritelem, version: 0.1.0}
servers: [{url: "http://localhost:8080"}]
security: [{bearerAuth: []}]
paths:
  /locations/{l}/animals:
    get:
      parameters:
        - $ref: "#/components/parameters/agritelemPageSize"
  /agritelem/ping:
    get:
      responses:
        "200": {description: ok}
components:
  securitySchemes:
    bearerAuth: {type: http, scheme: bearer}
  parameters:
    agritelemPageSize: {name: page-size, in: query, schema: {type: integer}}
  schemas:
    icarAnimal:
      properties:
        agritelem: {$ref: "#/components/schemas/agritelemAnimalExtension"}
    agritelemAnimalExtension: {type: object}
`

func build(t *testing.T, icar, ext string) map[string]any {
	t.Helper()
	out, err := Build([]byte(icar), []byte(ext))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	return doc
}

func at(t *testing.T, doc any, path ...string) any {
	t.Helper()
	cur := doc
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[p]
	}
	return cur
}

func TestBuildSelectsOnlyListedOperations(t *testing.T) {
	doc := build(t, icarFixture, extFixture)
	if at(t, doc, "paths", "/locations/{l}/animals", "get") == nil {
		t.Error("selected operation GET /locations/{l}/animals is missing")
	}
	if at(t, doc, "paths", "/locations/{l}/animals", "post") != nil {
		t.Error("unselected operation POST /locations/{l}/animals was kept")
	}
	if at(t, doc, "paths", "/locations/{l}/weights") != nil {
		t.Error("unselected path /locations/{l}/weights was kept")
	}
	if at(t, doc, "paths", "/agritelem/ping", "get") == nil {
		t.Error("extension path /agritelem/ping is missing")
	}
}

func TestBuildMergesAdditively(t *testing.T) {
	doc := build(t, icarFixture, extFixture)
	params, _ := at(t, doc, "paths", "/locations/{l}/animals", "get", "parameters").([]any)
	if len(params) != 2 {
		t.Fatalf("want ICAR param + extension param, got %v", params)
	}
	if at(t, doc, "components", "schemas", "icarAnimal", "properties", "id") == nil {
		t.Error("ICAR property icarAnimal.id lost in merge")
	}
	if at(t, doc, "components", "schemas", "icarAnimal", "properties", "agritelem") == nil {
		t.Error("extension property icarAnimal.agritelem not merged")
	}
}

func TestBuildPrunesToReferenceClosure(t *testing.T) {
	doc := build(t, icarFixture, extFixture)
	schemas, _ := at(t, doc, "components", "schemas").(map[string]any)
	var got []string
	for name := range schemas {
		got = append(got, name)
	}
	// icarDateTime is referenced only from the nullable anyOf, which is
	// inlined before pruning, so it must not survive.
	want := map[string]bool{"icarAnimal": true, "icarMeta": true, "agritelemAnimalExtension": true}
	for name := range want {
		if schemas[name] == nil {
			t.Errorf("referenced schema %q was pruned", name)
		}
	}
	for _, name := range got {
		if !want[name] {
			t.Errorf("unreferenced schema %q was kept", name)
		}
	}
	if at(t, doc, "components", "securitySchemes", "bearerAuth") == nil {
		t.Error("security scheme referenced from top-level security was pruned")
	}
}

func TestBuildRewritesNullableScalarAnyOf(t *testing.T) {
	doc := build(t, icarFixture, extFixture)
	created := at(t, doc, "components", "schemas", "icarMeta", "properties", "created")
	want := map[string]any{"description": "when", "type": []any{"string", "null"}, "format": "date-time"}
	if !reflect.DeepEqual(created, want) {
		t.Errorf("created = %v, want %v", created, want)
	}
}

func TestBuildReplacesInfoServersSecurity(t *testing.T) {
	doc := build(t, icarFixture, extFixture)
	if got := at(t, doc, "info", "title"); got != "Agritelem" {
		t.Errorf("info.title = %v, want Agritelem", got)
	}
	servers, _ := doc["servers"].([]any)
	if len(servers) != 1 || at(t, servers[0], "url") != "http://localhost:8080" {
		t.Errorf("servers = %v, want only the extension server", doc["servers"])
	}
	if _, ok := doc["x-agritelem-contract"]; ok {
		t.Error("contractgen directive leaked into output")
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	a, err := Build([]byte(icarFixture), []byte(extFixture))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build([]byte(icarFixture), []byte(extFixture))
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Error("two builds of the same input differ")
	}
}

func TestBuildRejects(t *testing.T) {
	tests := []struct {
		name    string
		ext     string
		wantErr string
	}{
		{
			name:    "scalar override of ICAR",
			ext:     extFixture + "    icarWeight: {type: string}\n",
			wantErr: "/components/schemas/icarWeight/type",
		},
		{
			name: "unknown ICAR operation",
			ext: strings.Replace(extFixture, "/locations/{l}/animals: [get]",
				"/locations/{l}/animals: [get, delete]", 1),
			wantErr: "delete /locations/{l}/animals",
		},
		{
			name:    "new path outside /agritelem/",
			ext:     strings.Replace(extFixture, "/agritelem/ping:", "/ping:", 1),
			wantErr: "/ping",
		},
		{
			name:    "new schema without agritelem prefix",
			ext:     extFixture + "    shinyNew: {type: string}\n",
			wantErr: "shinyNew",
		},
		{
			name:    "dangling reference",
			ext:     strings.Replace(extFixture, `agritelemAnimalExtension"}`, `agritelemMissing"}`, 1),
			wantErr: "#/components/schemas/agritelemMissing",
		},
		{
			name:    "openapi version mismatch",
			ext:     strings.Replace(extFixture, "openapi: 3.1.0", "openapi: 3.0.3", 1),
			wantErr: "3.0.3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Build([]byte(icarFixture), []byte(tt.ext))
			if err == nil {
				t.Fatalf("want error mentioning %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestMergeAdditive(t *testing.T) {
	tests := []struct {
		name    string
		dst     any
		src     any
		want    any
		wantErr string
	}{
		{"adds key", map[string]any{"a": "1"}, map[string]any{"b": "2"}, map[string]any{"a": "1", "b": "2"}, ""},
		{"equal scalar", map[string]any{"a": "1"}, map[string]any{"a": "1"}, map[string]any{"a": "1"}, ""},
		{"conflicting scalar", map[string]any{"a": "1"}, map[string]any{"a": "2"}, nil, "/a"},
		{"appends unique", []any{"x"}, []any{"x", "y"}, []any{"x", "y"}, ""},
		{"kind mismatch", map[string]any{"a": []any{}}, map[string]any{"a": map[string]any{}}, nil, "/a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mergeAdditive(tt.dst, tt.src, "")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want mention of %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
