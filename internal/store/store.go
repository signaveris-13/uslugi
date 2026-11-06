// Package store — доступ к Postgres: запросы для бота, админки и воркера.
package store

import (
	"crypto/rand"
	"encoding/base64"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound — запись не найдена.
var ErrNotFound = errors.New("не найдено")

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// randomToken возвращает случайную строку из n байт в base64url без паддинга.
// Подходит для deep link Telegram: там разрешены только A-Z, a-z, 0-9, _ и -.
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
