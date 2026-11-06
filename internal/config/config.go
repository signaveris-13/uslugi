// Package config читает настройки из переменных окружения.
// Локально их подставляет Makefile из файла .env.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	// Env: "local", "staging" или "production".
	Env string
	// DatabaseURL — строка подключения к Postgres.
	DatabaseURL string
	// BotToken — токен Telegram-бота от BotFather.
	BotToken string
	// BotUsername — username бота без @, для ссылок t.me/<bot>?start=…
	// Если пусто, веб-сервер узнаёт его у Telegram при старте.
	BotUsername string
	// HTTPAddr — адрес, на котором слушает веб-сервер (админка и каталог).
	HTTPAddr string
	// TeamTelegramIDs — Telegram ID команды, которых пускаем в админку.
	TeamTelegramIDs []int64
	// AdminTimezone — в каком часовом поясе админка показывает время.
	AdminTimezone string
}

func Load() (Config, error) {
	cfg := Config{
		Env:         getenv("APP_ENV", "local"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		BotToken:    os.Getenv("BOT_TOKEN"),
		BotUsername: strings.TrimPrefix(os.Getenv("BOT_USERNAME"), "@"),
		HTTPAddr:    getenv("HTTP_ADDR", ":8080"),
		// По умолчанию — время рынка запуска (Нячанг).
		AdminTimezone: getenv("ADMIN_TIMEZONE", "Asia/Ho_Chi_Minh"),
	}

	ids, err := parseIDs(os.Getenv("TEAM_TELEGRAM_IDS"))
	if err != nil {
		return Config{}, fmt.Errorf("TEAM_TELEGRAM_IDS: %w", err)
	}
	cfg.TeamTelegramIDs = ids

	return cfg, nil
}

// IsLocal — запуск на машине разработчика: cookie без Secure, подробные ошибки.
func (c Config) IsLocal() bool {
	return c.Env == "local"
}

// RequireDB проверяет, что задана строка подключения к базе.
func (c Config) RequireDB() error {
	if c.DatabaseURL == "" {
		return errors.New("DATABASE_URL не задан")
	}
	return nil
}

// RequireBot проверяет, что задан токен бота.
func (c Config) RequireBot() error {
	if c.BotToken == "" {
		return errors.New("BOT_TOKEN не задан")
	}
	return nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseIDs(raw string) ([]int64, error) {
	var ids []int64
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("не число: %q", part)
		}
		ids = append(ids, id)
	}
	return ids, nil
}
