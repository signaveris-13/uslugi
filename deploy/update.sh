#!/usr/bin/env bash
# Обновить прод до последней версии из main:  bash deploy/update.sh
# Миграции применяются автоматически перед стартом бота, админки и воркера.
set -euo pipefail
cd "$(dirname "$0")/.."

git pull --ff-only
docker compose -f deploy/compose.yml --env-file deploy/.env up -d --build
docker image prune -f >/dev/null
docker compose -f deploy/compose.yml --env-file deploy/.env ps
