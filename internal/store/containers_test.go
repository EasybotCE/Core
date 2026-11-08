//go:build integration

// SPDX-License-Identifier: AGPL-3.0-only
package store

import (
	"context"
	"github.com/testcontainers/testcontainers-go/modules/mysql"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"testing"
	"time"
)

// Docker is required. Missing daemon/startup failures are failures, not skips.
func TestContainerDatabases(t *testing.T) {
	for _, driver := range []string{"mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			var dsn string
			if driver == "mysql" {
				c, err := mysql.Run(ctx, "mysql:8.4", mysql.WithDatabase("easybotce"), mysql.WithUsername("easybotce"), mysql.WithPassword("container-test-only"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
					defer stop()
					if err := c.Terminate(cleanup); err != nil {
						t.Error("terminate MySQL container")
					}
				})
				dsn, err = c.ConnectionString(ctx, "parseTime=true")
				if err != nil {
					t.Fatal("MySQL DSN")
				}
			} else {
				c, err := postgres.Run(ctx, "postgres:17-alpine", postgres.WithDatabase("easybotce"), postgres.WithUsername("easybotce"), postgres.WithPassword("container-test-only"), postgres.BasicWaitStrategies())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
					defer stop()
					if err := c.Terminate(cleanup); err != nil {
						t.Error("terminate PostgreSQL container")
					}
				})
				dsn, err = c.ConnectionString(ctx, "sslmode=disable")
				if err != nil {
					t.Fatal("PostgreSQL DSN")
				}
			}
			t.Setenv("EASYBOTCE_TEST_DB_DRIVER", driver)
			t.Setenv("EASYBOTCE_TEST_DB_DSN", dsn)
			t.Run("endpoints", TestStoreLifecycle)
			t.Run("routing", TestRoutingLifecycle)
		})
	}
}
