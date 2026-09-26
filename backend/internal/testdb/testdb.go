//go:build integration

// Package testdb starts a disposable TimescaleDB container for integration
// tests. It is compiled only with the `integration` build tag.
package testdb

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Image is the TimescaleDB image used in tests; keep it in step with compose.yaml.
const Image = "timescale/timescaledb:2.30.1-pg17"

// Main starts a container, stores its URL in *dsn, runs the tests and removes
// the container. Call it from TestMain.
func Main(m *testing.M, dsn *string) int {
	ctx := context.Background()
	container, err := postgres.Run(ctx, Image,
		postgres.WithDatabase("agritelem"),
		postgres.WithUsername("agritelem"),
		postgres.WithPassword("agritelem"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start %s (is Docker running?): %v\n", Image, err)
		return 1
	}
	defer func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			fmt.Fprintf(os.Stderr, "terminate container: %v\n", err)
		}
	}()
	*dsn, err = container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "connection string: %v\n", err)
		return 1
	}
	return m.Run()
}
