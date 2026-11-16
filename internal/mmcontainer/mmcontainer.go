// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package mmcontainer runs a Mattermost server backed by Postgres in Docker for
// integration tests.
package mmcontainer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	// DefaultImage is used unless MM_IMAGE is set.
	DefaultImage = "mattermost/mattermost-enterprise-edition:latest"

	AdminUsername = "admin"
	AdminPassword = "admin"
	adminEmail    = "admin@example.com"
	teamName      = "test"

	postgresImage = "docker.io/postgres:15.2-alpine"
	dbAlias       = "db"
	dbName        = "mattermost_test"
	dbUser        = "user"
	dbPassword    = "pass"
	serverPort    = "8065/tcp"
)

// Container is a running Mattermost server with its own Postgres and network.
type Container struct {
	*testcontainers.DockerContainer
	postgres *postgres.PostgresContainer
	network  *testcontainers.DockerNetwork
}

// Run starts Postgres and Mattermost on a fresh network, patches cfg into the
// server config when non-nil, and creates the admin user and a team. The image
// is MM_IMAGE when set, else DefaultImage.
func Run(ctx context.Context, cfg *model.Config) (_ *Container, err error) {
	image := DefaultImage
	if env := os.Getenv("MM_IMAGE"); env != "" {
		image = env
	}

	c := &Container{}
	defer func() {
		if err != nil {
			err = errors.Join(err, c.Terminate(context.WithoutCancel(ctx)))
		}
	}()

	c.network, err = network.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("create network: %w", err)
	}

	c.postgres, err = postgres.Run(ctx, postgresImage,
		postgres.WithDatabase(dbName),
		postgres.WithUsername(dbUser),
		postgres.WithPassword(dbPassword),
		network.WithNetwork([]string{dbAlias}, c.network),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second)),
	)
	if err != nil {
		return nil, fmt.Errorf("start postgres: %w", err)
	}

	c.DockerContainer, err = testcontainers.Run(ctx, image,
		network.WithNetwork([]string{"mattermost"}, c.network),
		testcontainers.WithEnv(map[string]string{
			"MM_SQLSETTINGS_DATASOURCE":                     fmt.Sprintf("postgres://%s:%s@%s:5432/%s?sslmode=disable", dbUser, dbPassword, dbAlias, dbName),
			"MM_SQLSETTINGS_DRIVERNAME":                     "postgres",
			"MM_SERVICESETTINGS_ENABLELOCALMODE":            "true",
			"MM_PASSWORDSETTINGS_MINIMUMLENGTH":             "5",
			"MM_PLUGINSETTINGS_ENABLEUPLOADS":               "true",
			"MM_PLUGINSETTINGS_AUTOMATICPREPACKAGEDPLUGINS": "false",
			"MM_LOGSETTINGS_CONSOLELEVEL":                   "DEBUG",
			"MM_SERVICEENVIRONMENT":                         model.ServiceEnvironmentTest,
		}),
		testcontainers.WithExposedPorts(serverPort),
		testcontainers.WithCmd("mattermost", "server"),
		testcontainers.WithWaitStrategyAndDeadline(60*time.Second, wait.ForLog("Server is listening on")),
	)
	if err != nil {
		return nil, fmt.Errorf("start mattermost: %w", err)
	}

	if err = c.init(ctx, cfg); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Container) init(ctx context.Context, cfg *model.Config) error {
	if cfg != nil {
		data, err := json.Marshal(cfg)
		if err != nil {
			return fmt.Errorf("marshal config: %w", err)
		}
		const path = "/tmp/config-patch.json"
		if err = c.CopyToContainer(ctx, data, path, 0o644); err != nil {
			return fmt.Errorf("copy config patch: %w", err)
		}
		if err = c.mmctl(ctx, "config", "patch", path); err != nil {
			return err
		}
	}

	url, err := c.URL(ctx)
	if err != nil {
		return err
	}
	if err = c.SetConfig(ctx, "ServiceSettings.SiteURL", url); err != nil {
		return err
	}
	if err = c.mmctl(ctx, "user", "create", "--email", adminEmail, "--username", AdminUsername, "--password", AdminPassword, "--system-admin", "--email-verified"); err != nil {
		return err
	}
	if err = c.mmctl(ctx, "team", "create", "--name", teamName, "--display-name", "Test"); err != nil {
		return err
	}
	return c.mmctl(ctx, "team", "users", "add", teamName, AdminUsername)
}

// URL returns the server's base URL as reachable from the host.
func (c *Container) URL(ctx context.Context) (string, error) {
	return c.PortEndpoint(ctx, serverPort, "http")
}

// AdminClient returns a client logged in as the admin user.
func (c *Container) AdminClient(ctx context.Context) (*model.Client4, error) {
	url, err := c.URL(ctx)
	if err != nil {
		return nil, err
	}
	client := model.NewAPIv4Client(url)
	if _, _, err := client.Login(ctx, AdminUsername, AdminPassword); err != nil {
		return nil, fmt.Errorf("admin login: %w", err)
	}
	return client, nil
}

// SetConfig sets one server config key through mmctl.
func (c *Container) SetConfig(ctx context.Context, key, value string) error {
	return c.mmctl(ctx, "config", "set", key, value)
}

func (c *Container) mmctl(ctx context.Context, args ...string) error {
	exitCode, output, err := c.Exec(ctx, append([]string{"mmctl", "--local"}, args...))
	if err != nil {
		return fmt.Errorf("mmctl %s: %w", args[0], err)
	}
	if exitCode != 0 {
		out, _ := io.ReadAll(output)
		return fmt.Errorf("mmctl %v exited %d: %s", args, exitCode, out)
	}
	return nil
}

// Terminate removes the containers and network. It is safe on a partially
// started Container.
func (c *Container) Terminate(ctx context.Context) error {
	var errs []error
	if c.DockerContainer != nil {
		errs = append(errs, c.DockerContainer.Terminate(ctx))
	}
	if c.postgres != nil {
		errs = append(errs, c.postgres.Terminate(ctx))
	}
	if c.network != nil {
		errs = append(errs, c.network.Remove(ctx))
	}
	return errors.Join(errs...)
}
