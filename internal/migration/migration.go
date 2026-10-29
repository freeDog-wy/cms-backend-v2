package migration

import (
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed sql/*.sql
var files embed.FS

func Up(databaseURL string) error {
	src, err := iofs.New(files, "sql")
	if err != nil {
		return fmt.Errorf("open migration files: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", src, databaseURL)
	if err != nil {
		return errors.Join(
			fmt.Errorf("init migrator: %w", err),
			src.Close(),
		)
	}

	defer func() {
		sourceErr, databaseError := m.Close()
		err = errors.Join(err, sourceErr, databaseError)
	}()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
