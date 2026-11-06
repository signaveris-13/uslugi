package store

import (
	"context"
	"fmt"
)

type TeamMember struct {
	ID         int64  `db:"id"`
	TelegramID int64  `db:"telegram_id"`
	Name       string `db:"name"`
	Role       string `db:"role"`
	IsActive   bool   `db:"is_active"`
}

// BootstrapTeam заводит администраторов из TEAM_TELEGRAM_IDS, если их ещё нет,
// и включает уже существующих. Остальных участников команды не трогает.
func (s *Store) BootstrapTeam(ctx context.Context, telegramIDs []int64) error {
	for _, id := range telegramIDs {
		_, err := s.pool.Exec(ctx, `
			INSERT INTO team_member (telegram_id, name, role)
			VALUES ($1, $2, 'admin')
			ON CONFLICT (telegram_id) DO UPDATE SET is_active = true`,
			id, fmt.Sprintf("Админ %d", id))
		if err != nil {
			return fmt.Errorf("команда %d: %w", id, err)
		}
	}
	return nil
}

// ActiveTeamMemberByTelegramID возвращает активного участника команды или ErrNotFound.
func (s *Store) ActiveTeamMemberByTelegramID(ctx context.Context, telegramID int64) (TeamMember, error) {
	rows, _ := s.pool.Query(ctx, `
		SELECT id, telegram_id, name, role, is_active
		FROM team_member WHERE telegram_id = $1 AND is_active`, telegramID)
	m, err := collectOne[TeamMember](rows)
	return m, notFound(err)
}

// UpdateTeamMemberName подставляет имя из Telegram вместо заглушки «Админ <id>».
func (s *Store) UpdateTeamMemberName(ctx context.Context, id int64, name string) error {
	_, err := s.pool.Exec(ctx, `UPDATE team_member SET name = $2 WHERE id = $1`, id, name)
	return err
}

func (s *Store) ListTeam(ctx context.Context) ([]TeamMember, error) {
	rows, _ := s.pool.Query(ctx, `
		SELECT id, telegram_id, name, role, is_active
		FROM team_member WHERE is_active ORDER BY name`)
	return collectAll[TeamMember](rows)
}
