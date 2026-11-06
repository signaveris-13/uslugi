package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgconn"
)

// execer — общий интерфейс пула и транзакции для записи.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// audit пишет действие участника команды в журнал (§12).
func audit(ctx context.Context, q execer, actorID int64, action, objectType string, objectID int64, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	_, err := q.Exec(ctx, `
		INSERT INTO audit_log (actor_id, action, object_type, object_id, details)
		VALUES ($1, $2, $3, $4, $5)`, actorID, action, objectType, objectID, details)
	return err
}
