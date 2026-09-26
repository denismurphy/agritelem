// Package httpapi exposes the Agritelem HTTP API. oapi.gen.go holds the types
// and strict server interface generated from contracts/openapi.json.
package httpapi

//go:generate go tool -modfile=../../tools.mod oapi-codegen -config oapi-codegen.yaml ../../../contracts/openapi.json
