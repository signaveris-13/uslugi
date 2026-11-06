# Uslugi

Каталог услуг русскоязычных мастеров для русскоязычных за границей. Старт — Нячанг.
Описание продукта и задачи — в Notion.

## Стек

- Go — весь бэкенд: бот, веб, фоновые задачи.
- Postgres; запросы через pgx, миграции через goose (вшиты в бинарь).
- Админка с CRM и веб-каталог — [templ](https://templ.guide) + [htmx](https://htmx.org), без Node.
- Telegram: [go-telegram/bot](https://github.com/go-telegram/bot) для бота, gotd/td для MTProto (краулер, аккаунт аутрича).

## Структура

```
cmd/bot          Telegram-бот
cmd/web          HTTP: админка, CRM, позже веб-каталог
cmd/worker       фоновые задачи: краулер, напоминания, follow-up
cmd/migrate      применение миграций
internal/botapp  сценарии бота
internal/store   запросы к базе
internal/web     обработчики админки, views/ — шаблоны templ, static/ — CSS, JS, htmx
migrations/      SQL-миграции goose
```

## Запуск локально

Нужны Go 1.24+ и Postgres (на Mac проще всего [Postgres.app](https://postgresapp.com)).

```sh
cp .env.example .env
```

В `.env` впиши `BOT_TOKEN` и свой Telegram ID в `TEAM_TELEGRAM_IDS` (бот пишет его в ответ на /start).

```sh
make migrate
make run-bot
```

Админка — во второй вкладке терминала:

```sh
make run-web
```

Открой http://localhost:8080 → «Войти через Telegram» → подтверди в боте. Для входа бот должен быть запущен.

Все команды: `make help`.

## Правила

- Секреты только в `.env`, он в `.gitignore`. Файлы `*.session` (сессии Telegram-аккаунтов) не коммитятся никогда.
- Новая миграция — новый файл `migrations/000NN_<что>.sql` с секциями `-- +goose Up` и `-- +goose Down`. Уже применённые миграции не правим.
- Шаблоны правим в `*.templ`, потом `make generate`. Файлы `*_templ.go` генерируются и коммитятся; CI проверяет, что они свежие.
- Тесты `internal/store` идут на настоящем Postgres из `DATABASE_URL`.
