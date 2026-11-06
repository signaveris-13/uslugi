# Uslugi

Каталог услуг русскоязычных мастеров для русскоязычных за границей. Старт — Нячанг.
Описание продукта и задачи — в Notion.

## Стек

- Go — весь бэкенд: бот, веб, фоновые задачи.
- Postgres; запросы через pgx, миграции через goose (вшиты в бинарь).
- Админка с CRM и веб-каталог — templ + htmx (появятся следующими задачами).
- Telegram: [go-telegram/bot](https://github.com/go-telegram/bot) для бота, gotd/td для MTProto (краулер, аккаунт аутрича).

## Структура

```
cmd/bot       Telegram-бот
cmd/web       HTTP: админка, CRM, веб-каталог
cmd/worker    фоновые задачи: краулер, напоминания, follow-up
cmd/migrate   применение миграций
internal/     общий код: config, db
migrations/   SQL-миграции goose
```

## Запуск локально

Нужны Go 1.24+ и Docker.

```sh
cp .env.example .env   # вписать BOT_TOKEN тестового бота
make up                # Postgres в Docker
make migrate           # схема базы
make run-bot           # бот; напиши ему /start
```

Веб-сервер: `make run-web`, проверка — `curl localhost:8080/healthz`.
Все команды: `make help`.

## Правила

- Секреты только в `.env`, он в `.gitignore`. Файлы `*.session` (сессии Telegram-аккаунтов) не коммитятся никогда.
- Новая миграция — новый файл `migrations/000NN_<что>.sql` с секциями `-- +goose Up` и `-- +goose Down`.
