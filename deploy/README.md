# Деплой на VPS

Всё работает в Docker Compose на одном сервере:

| Сервис   | Что делает |
|----------|------------|
| postgres | база, наружу не открыта |
| migrate  | применяет миграции при каждом запуске и завершается |
| bot      | Telegram-бот (long polling) |
| web      | админка с CRM |
| worker   | фоновые задачи |
| caddy    | HTTPS: сам выпускает и продлевает сертификат Let's Encrypt |
| backup   | раз в сутки дамп базы в `deploy/backups/`, хранится 30 дней |

Ещё на сервере работает `deploy/status.sh`: cron запускает его раз в 10 минут. Он собирает сводку о здоровье системы: память, swap, диск, контейнеры и их перезапуски, ошибки в логах, базу, бэкапы, сертификат. Сводка лежит в `deploy/status/status.txt`.

Нужен сервер на Ubuntu или Debian с открытыми портами 22, 80 и 443.

## Первая установка

Всё — на сервере, от root.

1. Ключ, чтобы сервер мог читать приватный репозиторий:

   ```sh
   ssh-keygen -t ed25519 -N "" -f ~/.ssh/uslugi_deploy && cat ~/.ssh/uslugi_deploy.pub
   ```

   Добавь напечатанный ключ в репозиторий: Settings → Deploy keys → Add deploy key, без права записи.

2. Склонируй репозиторий:

   ```sh
   apt-get update && apt-get install -y git curl
   ```
   ```sh
   GIT_SSH_COMMAND="ssh -i ~/.ssh/uslugi_deploy -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new" git clone git@github.com:signaveris-13/uslugi.git /opt/uslugi
   ```
   ```sh
   git -C /opt/uslugi config core.sshCommand "ssh -i ~/.ssh/uslugi_deploy -o IdentitiesOnly=yes"
   ```

3. Запусти установку:

   ```sh
   cd /opt/uslugi && bash deploy/install.sh
   ```

   Скрипт поставит Docker, откроет порты, спросит адрес, токен бота и Telegram ID команды, сохранит их в `deploy/.env` и запустит всё.
   Без своего домена адрес будет `<IP через дефисы>.sslip.io` — это настоящий HTTPS.

Один токен бота не может работать в двух местах сразу: пока бот запущен на сервере, локальный `make run-bot` останови.

## Два окружения: Stage и Prod

| | Stage | Prod |
|---|---|---|
| Сервер | DigitalOcean, Амстердам, 1 ГБ — `146-190-16-122.sslip.io` | DigitalOcean, Сингапур, 2 ГБ — ближе к пользователям во Вьетнаме |
| Бот | `@uslugi_sandbox_bot` | отдельный прод-бот |
| `APP_ENV` | `staging` — в админке и приложении бейдж «Stage» | `production` |
| Данные | тестовые | только справочники из миграций, без тестовых объявлений |

У каждого окружения свой сервер, своя база, свой бот и свой `deploy/.env`. Код один: оба сервера обновляются из `main` командой `bash deploy/update.sh`. Сначала обновляем Stage и проверяем, потом Prod.

Чтобы пометить сервер как Stage, один раз:

```sh
cd /opt/uslugi && grep -q '^APP_ENV=' deploy/.env || echo 'APP_ENV=staging' >> deploy/.env && bash deploy/update.sh
```

Прод ставится так же, как в «Первой установке»: на шаге `install.sh` ответь `production`, укажи токен прод-бота и адрес вида `<IP через дефисы>.sslip.io`. Deploy key у каждого сервера свой.

## Обновление

```sh
cd /opt/uslugi && bash deploy/update.sh
```

## Сводка для адвайзера

Caddy отдаёт сводку по секретной ссылке `https://DOMAIN/ops/STATUS_TOKEN/status.txt`. Её читает ежедневная задача-адвайзер. Токен `update.sh` создаёт сам и пишет в `deploy/.env`, а ссылку печатает в конце. Токены, пароли и адреса баз из логов в сводку не попадают.

Новая ссылка: удали строку `STATUS_TOKEN` из `deploy/.env` и выполни `bash deploy/update.sh`. Потом обнови ссылку в задаче-адвайзере.

## Полезное

```sh
cd /opt/uslugi
docker compose -f deploy/compose.yml ps
docker compose -f deploy/compose.yml logs -f web bot
docker compose -f deploy/compose.yml restart bot
bash deploy/status.sh && cat deploy/status/status.txt   # сводка прямо сейчас
```

Свой домен: добавь A-запись на IP сервера, поменяй `DOMAIN` в `deploy/.env` и выполни `bash deploy/update.sh`.

Бэкапы лежат на том же сервере. Пока это защита от ошибок в данных, но не от потери сервера: копию в другое место настроим перед запуском.
