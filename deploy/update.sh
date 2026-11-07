#!/usr/bin/env bash
# Обновить прод до последней версии из main:  bash deploy/update.sh
# Миграции применяются автоматически перед стартом бота, админки и воркера.
set -euo pipefail
cd "$(dirname "$0")/.."

git pull --ff-only

# Секрет для ссылки на сводку адвайзера: создаётся один раз.
if ! grep -q '^STATUS_TOKEN=.' deploy/.env; then
  sed -i '/^STATUS_TOKEN=/d' deploy/.env
  echo "STATUS_TOKEN=$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')" >> deploy/.env
fi
mkdir -p deploy/status
chmod +x deploy/status.sh

# Сводка о здоровье сервера раз в 10 минут (deploy/status.sh).
if [ "$(id -u)" -eq 0 ]; then
  echo "*/10 * * * * root $(pwd)/deploy/status.sh >/dev/null 2>&1" > /etc/cron.d/uslugi-status
  chmod 644 /etc/cron.d/uslugi-status
else
  echo "cron для сводки не поставлен: запусти update.sh от root" >&2
fi
docker compose -f deploy/compose.yml --env-file deploy/.env up -d --build
docker image prune -f >/dev/null
docker compose -f deploy/compose.yml --env-file deploy/.env ps

bash deploy/status.sh || true
DOMAIN=$(grep '^DOMAIN=' deploy/.env | cut -d= -f2)
TOKEN=$(grep '^STATUS_TOKEN=' deploy/.env | cut -d= -f2)
echo
echo "Сводка для адвайзера: https://$DOMAIN/ops/$TOKEN/status.txt"
