#!/usr/bin/env bash
# Первая установка Uslugi на чистый VPS (Ubuntu или Debian). Запускать от root
# из папки с репозиторием:  bash deploy/install.sh
# Повторный запуск безопасен: уже созданный deploy/.env не перезаписывается.
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT=$(pwd)
ENV_FILE="$ROOT/deploy/.env"

if [ "$(id -u)" -ne 0 ]; then
  echo "Запусти от root: sudo bash deploy/install.sh" >&2
  exit 1
fi

echo "==> Docker"
if ! command -v docker >/dev/null 2>&1; then
  curl -fsSL https://get.docker.com | sh
else
  echo "уже установлен: $(docker --version)"
fi

echo "==> Файрвол"
if command -v ufw >/dev/null 2>&1; then
  ufw allow OpenSSH >/dev/null
  ufw allow 80/tcp >/dev/null
  ufw allow 443/tcp >/dev/null
  ufw --force enable >/dev/null
  echo "открыты 22, 80, 443"
else
  echo "ufw нет — проверь, что у провайдера открыты порты 80 и 443"
fi

echo "==> Настройки"
if [ ! -f "$ENV_FILE" ]; then
  IP=$(curl -fsS https://api.ipify.org || true)
  DEFAULT_DOMAIN=""
  [ -n "$IP" ] && DEFAULT_DOMAIN="${IP//./-}.sslip.io"

  read -r -p "Окружение: staging или production [production]: " APP_ENV
  APP_ENV=${APP_ENV:-production}
  case "$APP_ENV" in staging|production) ;; *) echo "Нужно staging или production" >&2; exit 1 ;; esac
  read -r -p "Адрес админки [$DEFAULT_DOMAIN]: " DOMAIN
  DOMAIN=${DOMAIN:-$DEFAULT_DOMAIN}
  read -r -p "Токен бота от @BotFather: " BOT_TOKEN
  read -r -p "Telegram ID команды через запятую: " TEAM_IDS

  umask 077
  cat > "$ENV_FILE" <<EOF
APP_ENV=$APP_ENV
DOMAIN=$DOMAIN
POSTGRES_PASSWORD=$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')
BOT_TOKEN=$BOT_TOKEN
BOT_USERNAME=
TEAM_TELEGRAM_IDS=$TEAM_IDS
ADMIN_TIMEZONE=Asia/Ho_Chi_Minh
SUPPORT_CHAT=@uslugi_support
EOF
  echo "сохранено в deploy/.env"
else
  echo "deploy/.env уже есть — оставляю как есть"
fi

mkdir -p "$ROOT/deploy/backups"
# Бэкапы пишет контейнер postgres (uid 70)
chown 70:70 "$ROOT/deploy/backups"

echo "==> Сводка для адвайзера"
if ! grep -q '^STATUS_TOKEN=.' "$ENV_FILE"; then
  sed -i '/^STATUS_TOKEN=/d' "$ENV_FILE"
  echo "STATUS_TOKEN=$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')" >> "$ENV_FILE"
fi
mkdir -p "$ROOT/deploy/status"
chmod +x "$ROOT/deploy/status.sh"
echo "*/10 * * * * root $ROOT/deploy/status.sh >/dev/null 2>&1" > /etc/cron.d/uslugi-status
chmod 644 /etc/cron.d/uslugi-status

echo "==> Сборка и запуск"
docker compose -f deploy/compose.yml --env-file "$ENV_FILE" up -d --build
bash "$ROOT/deploy/status.sh" || true

DOMAIN=$(grep '^DOMAIN=' "$ENV_FILE" | cut -d= -f2)
echo
echo "Готово. Админка: https://$DOMAIN"
echo "Первый выпуск сертификата занимает до минуты."
echo "Логи: docker compose -f deploy/compose.yml logs -f web bot"
