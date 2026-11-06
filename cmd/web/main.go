// Команда web — HTTP-сервер: админка с CRM, позже веб-каталог (templ + htmx).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // часовые пояса внутри бинаря, не зависим от системы

	"github.com/go-telegram/bot"

	"github.com/signaveris-13/uslugi/internal/config"
	"github.com/signaveris-13/uslugi/internal/db"
	"github.com/signaveris-13/uslugi/internal/store"
	"github.com/signaveris-13/uslugi/internal/web"
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

	st := store.New(pool)
	if err := st.BootstrapTeam(ctx, cfg.TeamTelegramIDs); err != nil {
		log.Error("команда", "err", err)
		os.Exit(1)
	}

	botUsername := cfg.BotUsername
	if botUsername == "" && cfg.BotToken != "" {
		botUsername = fetchBotUsername(ctx, cfg.BotToken, log)
	}
	if botUsername == "" {
		log.Warn("username бота неизвестен: вход в админку не заработает. Задай BOT_USERNAME в .env")
	}

	tz, err := time.LoadLocation(cfg.AdminTimezone)
	if err != nil {
		log.Error("часовой пояс", "err", err, "tz", cfg.AdminTimezone)
		os.Exit(1)
	}

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: web.New(st, log, web.Options{
			BotUsername:  botUsername,
			SecureCookie: !cfg.IsLocal(),
			TimeZone:     tz,
		}).Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Info("веб-сервер запущен", "addr", cfg.HTTPAddr, "admin", "http://localhost"+cfg.HTTPAddr+"/login")
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("веб-сервер", "err", err)
		os.Exit(1)
	}
	log.Info("веб-сервер остановлен")
}

// fetchBotUsername спрашивает у Telegram username бота по токену.
func fetchBotUsername(ctx context.Context, token string, log *slog.Logger) string {
	b, err := bot.New(token, bot.WithSkipGetMe())
	if err != nil {
		log.Warn("бот", "err", err)
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	me, err := b.GetMe(ctx)
	if err != nil {
		log.Warn("не удалось узнать username бота у Telegram", "err", err)
		return ""
	}
	return me.Username
}
