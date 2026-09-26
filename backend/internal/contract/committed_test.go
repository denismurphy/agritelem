package contract

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

const (
	vendoredICAR = "../../../contracts/vendor/icar-ade-1.5.1/combinedURLScheme.json"
	extension    = "../../../contracts/agritelem.yaml"
	committed    = "../../../contracts/openapi.json"
)

// TestCommittedContractIsCurrent fails when contracts/openapi.json was not
// regenerated after a change to its inputs or to this package.
func TestCommittedContractIsCurrent(t *testing.T) {
	icar, err := os.ReadFile(vendoredICAR)
	if err != nil {
		t.Fatal(err)
	}
	ext, err := os.ReadFile(extension)
	if err != nil {
		t.Fatal(err)
	}
	built, err := Build(icar, ext)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	current, err := os.ReadFile(committed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(built, current) {
		t.Fatal("contracts/openapi.json is out of date; run `make contracts`")
	}
}

func TestCommittedContractIsValidOpenAPI(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromFile(committed)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate: %v", err)
	}
}
