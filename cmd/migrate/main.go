// Команда migrate применяет SQL-миграции из папки migrations.
//
//	go run ./cmd/migrate up       — применить все новые
//	go run ./cmd/migrate down     — откатить последнюю
//	go run ./cmd/migrate status   — показать, что применено
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib" // драйвер "pgx" для database/sql
	"github.com/pressly/goose/v3"

	"github.com/signaveris-13/uslugi/internal/config"
	"github.com/signaveris-13/uslugi/migrations"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(); err != nil {
		log.Error("миграции", "err", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("укажи команду: up, down или status")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := cfg.RequireDB(); err != nil {
		return err
	}

	sqlDB, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}

	ctx := context.Background()
	switch cmd := os.Args[1]; cmd {
	case "up":
		return goose.UpContext(ctx, sqlDB, ".")
	case "down":
		return goose.DownContext(ctx, sqlDB, ".")
	case "status":
		return goose.StatusContext(ctx, sqlDB, ".")
	default:
		return fmt.Errorf("неизвестная команда %q", cmd)
	}
}
