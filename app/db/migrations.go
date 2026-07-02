package db

import (
	"context"
	"embed"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

//go:embed *.sql
var sqlMigrations embed.FS

var Migrations = migrate.NewMigrations()

func init() {
	if err := Migrations.Discover(sqlMigrations); err != nil {
		panic(err)
	}
}

func Migrate(ctx context.Context, db *bun.DB) error {
	m := migrate.NewMigrator(db, Migrations)
	if err := m.Init(ctx); err != nil {
		return err
	}
	_, err := m.Migrate(ctx)
	return err
}
