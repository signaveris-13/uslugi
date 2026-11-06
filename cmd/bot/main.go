// Команда bot — Telegram-бот Uslugi.
// Пока умеет только отвечать на /start; сценарии клиента и исполнителя
// добавляются следующими задачами.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/signaveris-13/uslugi/internal/config"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err == nil {
		err = cfg.RequireBot()
	}
	if err != nil {
		log.Error("конфигурация", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	b, err := bot.New(cfg.BotToken,
		bot.WithDefaultHandler(func(ctx context.Context, b *bot.Bot, u *models.Update) {}),
	)
	if err != nil {
		log.Error("не удалось запустить бота", "err", err)
		os.Exit(1)
	}

	b.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypePrefix, startHandler(log))

	log.Info("бот запущен, жду сообщений", "env", cfg.Env)
	b.Start(ctx) // long polling до Ctrl+C
	log.Info("бот остановлен")
}

func startHandler(log *slog.Logger) bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, u *models.Update) {
		msg := u.Message
		if msg == nil || msg.From == nil {
			return
		}

		// Всё после "/start " — это payload deep link: inv_<token>, l_<id>, src_<tag>…
		payload := strings.TrimSpace(strings.TrimPrefix(msg.Text, "/start"))
		log.Info("start", "user_id", msg.From.ID, "username", msg.From.Username, "payload", payload)

		text := fmt.Sprintf(
			"Привет! Это тестовый бот Uslugi — скоро здесь будет каталог услуг.\n\nТвой Telegram ID: %d",
			msg.From.ID,
		)
		if _, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: msg.Chat.ID, Text: text}); err != nil {
			log.Error("не удалось ответить", "err", err)
		}
	}
}
