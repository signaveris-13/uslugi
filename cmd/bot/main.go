// Команда bot — Telegram-бот Uslugi.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/go-telegram/bot"

	"github.com/signaveris-13/uslugi/internal/botapp"
	"github.com/signaveris-13/uslugi/internal/config"
	"github.com/signaveris-13/uslugi/internal/db"
	"github.com/signaveris-13/uslugi/internal/media"
	"github.com/signaveris-13/uslugi/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err == nil {
		err = cfg.RequireBot()
	}
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

	st := store.New(pool)
	if err := st.BootstrapTeam(ctx, cfg.TeamTelegramIDs); err != nil {
		log.Error("команда", "err", err)
		os.Exit(1)
	}

	app := botapp.New(st, log)
	if m, err := media.New(cfg.MediaDir); err == nil {
		app.SetMedia(m)
	} else {
		log.Warn("папка для фото недоступна: фото из приложения бот не покажет", "err", err)
	}
	app.SetPublicURL(cfg.PublicURL)
	if cfg.PublicURL == "" || !strings.HasPrefix(cfg.PublicURL, "https://") {
		log.Warn("PUBLIC_URL не задан или не https: кнопок Mini App в боте не будет")
	}
	b, err := bot.New(cfg.BotToken, bot.WithDefaultHandler(app.DefaultHandler))
	if err != nil {
		log.Error("не удалось запустить бота", "err", err)
		os.Exit(1)
	}
	app.Register(b)
	if me, err := b.GetMe(ctx); err == nil {
		app.SetBotUsername(me.Username)
	} else {
		log.Warn("username бота", "err", err)
	}

	app.SetupMenuButton(ctx, b)

	log.Info("бот запущен, жду сообщений", "env", cfg.Env)
	b.Start(ctx) // long polling до Ctrl+C
	log.Info("бот остановлен")
}
