// Команда worker — фоновые задачи: краулер чатов, напоминания
// «Объявление актуально?» (FR-P-07), follow-up в CRM (FR-M-05).
// Пока только проверяет базу и ждёт сигнала остановки.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/signaveris-13/uslugi/internal/config"
	"github.com/signaveris-13/uslugi/internal/db"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err == nil {
		err = cfg.RequireDB()
	}
	if err != nil {
		log.Error("конфигурация", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("база", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	log.Info("воркер запущен, задач пока нет")
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("воркер остановлен")
			return
		case <-ticker.C:
			// Здесь будут задачи по расписанию.
		}
	}
}
