package store

import "github.com/jackc/pgx/v5"

// collectOne читает одну строку в структуру по тегам db.
func collectOne[T any](rows pgx.Rows) (T, error) {
	return pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[T])
}

// collectAll читает все строки в слайс структур по тегам db.
func collectAll[T any](rows pgx.Rows) ([]T, error) {
	return pgx.CollectRows(rows, pgx.RowToStructByName[T])
}
