// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"context"
	"embed"
	"fmt"
	"path/filepath"

	"github.com/mattermost/morph"
	ms "github.com/mattermost/morph/drivers/postgres"
	"github.com/mattermost/morph/sources/embedded"
)

//go:embed migrations/*.sql
var assets embed.FS

// RunMigrations runs all pending Morph schema migrations.
// The caller must hold a cluster mutex before calling this method.
// Morph also uses a PostgreSQL advisory lock internally for additional HA safety.
func (s *Store) RunMigrations() error {
	driver, err := ms.WithInstance(s.db.DB)
	if err != nil {
		return fmt.Errorf("failed to create morph postgres driver: %w", err)
	}

	dirEntries, err := assets.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("failed to read embedded migrations directory: %w", err)
	}

	assetNames := make([]string, 0, len(dirEntries))
	for _, entry := range dirEntries {
		assetNames = append(assetNames, entry.Name())
	}

	assetSource := embedded.Resource(assetNames, func(name string) ([]byte, error) {
		return assets.ReadFile(filepath.Join("migrations", name))
	})

	source, err := embedded.WithInstance(assetSource)
	if err != nil {
		return fmt.Errorf("failed to create morph embedded source: %w", err)
	}

	engine, err := morph.New(context.Background(), driver, source,
		morph.WithLock("agents-plugin-lock-key"),
		morph.SetMigrationTableName("Agents_DB_Migrations"),
		morph.SetStatementTimeoutInSeconds(300),
	)
	if err != nil {
		return fmt.Errorf("failed to create morph engine: %w", err)
	}
	defer engine.Close()

	if err := engine.ApplyAll(); err != nil {
		return fmt.Errorf("failed to apply migrations: %w", err)
	}

	return nil
}
