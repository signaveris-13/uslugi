// Package migrations содержит SQL-миграции схемы в формате goose.
// Файлы вшиваются в бинарь, поэтому cmd/migrate работает без внешних утилит.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
