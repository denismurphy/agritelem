// Command contractgen builds contracts/openapi.json from the vendored ICAR ADE
// bundle and the Agritelem extension. With -check it exits non-zero when the
// committed output is stale instead of rewriting it.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"

	"github.com/denismurphy/agritelem/backend/internal/contract"
)

func main() {
	icarPath := flag.String("icar", "../contracts/vendor/icar-ade-1.5.1/combinedURLScheme.json", "vendored ICAR bundle")
	extPath := flag.String("ext", "../contracts/agritelem.yaml", "Agritelem extension document")
	outPath := flag.String("out", "../contracts/openapi.json", "output OpenAPI document")
	check := flag.Bool("check", false, "fail if the output file is out of date instead of writing it")
	flag.Parse()

	if err := run(*icarPath, *extPath, *outPath, *check); err != nil {
		fmt.Fprintln(os.Stderr, "contractgen:", err)
		os.Exit(1)
	}
}

func run(icarPath, extPath, outPath string, check bool) error {
	icar, err := os.ReadFile(icarPath) //nolint:gosec // G304: developer tool; paths come from its own flags.
	if err != nil {
		return err
	}
	ext, err := os.ReadFile(extPath) //nolint:gosec // G304: as above.
	if err != nil {
		return err
	}
	built, err := contract.Build(icar, ext)
	if err != nil {
		return err
	}
	if !check {
		return os.WriteFile(outPath, built, 0o644) //nolint:gosec // G306: a committed, world-readable contract.
	}
	current, err := os.ReadFile(outPath) //nolint:gosec // G304: as above.
	if err != nil {
		return err
	}
	if !bytes.Equal(current, built) {
		return fmt.Errorf("%s is out of date; run `make contracts`", outPath)
	}
	return nil
}
