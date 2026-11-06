# Переменные из .env (если файл есть) попадают в окружение всех команд.
ifneq (,$(wildcard .env))
include .env
export
endif

.PHONY: help up down migrate migrate-down migrate-status run-bot run-web run-worker test vet tidy

help: ## Показать команды
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  make %-15s %s\n", $$1, $$2}'

up: ## Поднять локальный Postgres
	docker compose up -d --wait postgres

down: ## Остановить Postgres (данные сохраняются)
	docker compose down

migrate: ## Применить миграции
	go run ./cmd/migrate up

migrate-down: ## Откатить последнюю миграцию
	go run ./cmd/migrate down

migrate-status: ## Какие миграции применены
	go run ./cmd/migrate status

run-bot: ## Запустить бота (long polling)
	go run ./cmd/bot

run-web: ## Запустить веб-сервер (админка, каталог)
	go run ./cmd/web

run-worker: ## Запустить фоновые задачи
	go run ./cmd/worker

test: ## Тесты
	go test ./...

vet: ## Статический анализ
	go vet ./...

tidy: ## Обновить go.mod и go.sum
	go mod tidy
