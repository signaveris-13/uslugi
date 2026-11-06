-- Стартовая миграция скелета. Таблицы модели данных (T-10) добавляются
-- следующими миграциями.

-- +goose Up
-- citext — регистронезависимый текст: Telegram username и email сравниваем без учёта регистра.
CREATE EXTENSION IF NOT EXISTS citext;

-- +goose Down
DROP EXTENSION IF EXISTS citext;
