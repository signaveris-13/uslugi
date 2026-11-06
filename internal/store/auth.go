package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

const (
	LoginTTL   = 10 * time.Minute
	SessionTTL = 30 * 24 * time.Hour
)

// Ошибки подтверждения входа.
var (
	ErrNotTeamMember = errors.New("нет в команде")
	ErrLoginExpired  = errors.New("ссылка для входа устарела")
)

// LoginState — что знает браузер о своём токене входа.
type LoginState int

const (
	LoginPending LoginState = iota
	LoginConfirmed
	LoginExpired
)

// CreateLogin заводит одноразовый токен для входа через бота.
func (s *Store) CreateLogin(ctx context.Context) (string, error) {
	token, err := randomToken(18)
	if err != nil {
		return "", err
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO admin_login (token, expires_at) VALUES ($1, $2)`,
		token, time.Now().Add(LoginTTL))
	return token, err
}

// ConfirmLogin вызывает бот, когда участник команды подтвердил вход.
func (s *Store) ConfirmLogin(ctx context.Context, token string, telegramID int64) (TeamMember, error) {
	m, err := s.ActiveTeamMemberByTelegramID(ctx, telegramID)
	if errors.Is(err, ErrNotFound) {
		return TeamMember{}, ErrNotTeamMember
	}
	if err != nil {
		return TeamMember{}, err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE admin_login SET team_member_id = $2, confirmed_at = now()
		WHERE token = $1 AND confirmed_at IS NULL AND expires_at > now()`,
		token, m.ID)
	if err != nil {
		return TeamMember{}, err
	}
	if tag.RowsAffected() == 0 {
		return TeamMember{}, ErrLoginExpired
	}
	return m, nil
}

// ExchangeLogin проверяет токен из браузера. Если вход подтверждён,
// помечает токен использованным и выдаёт значение cookie новой сессии.
func (s *Store) ExchangeLogin(ctx context.Context, token string) (LoginState, string, error) {
	var memberID *int64
	var expiresAt time.Time
	var used *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT team_member_id, expires_at, used_at FROM admin_login WHERE token = $1`, token,
	).Scan(&memberID, &expiresAt, &used)
	if err != nil {
		if errors.Is(notFound(err), ErrNotFound) {
			return LoginExpired, "", nil
		}
		return 0, "", err
	}
	if used != nil || time.Now().After(expiresAt) {
		return LoginExpired, "", nil
	}
	if memberID == nil {
		return LoginPending, "", nil
	}

	tag, err := s.pool.Exec(ctx,
		`UPDATE admin_login SET used_at = now() WHERE token = $1 AND used_at IS NULL`, token)
	if err != nil {
		return 0, "", err
	}
	if tag.RowsAffected() == 0 {
		return LoginExpired, "", nil // параллельный запрос успел раньше
	}

	sid, err := randomToken(32)
	if err != nil {
		return 0, "", err
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO admin_session (token_hash, team_member_id, expires_at) VALUES ($1, $2, $3)`,
		hashToken(sid), *memberID, time.Now().Add(SessionTTL))
	if err != nil {
		return 0, "", err
	}
	return LoginConfirmed, sid, nil
}

// SessionMember возвращает участника команды по cookie сессии или ErrNotFound.
func (s *Store) SessionMember(ctx context.Context, sid string) (TeamMember, error) {
	rows, _ := s.pool.Query(ctx, `
		UPDATE admin_session ss SET last_seen_at = now()
		FROM team_member m
		WHERE ss.token_hash = $1 AND ss.expires_at > now()
		  AND m.id = ss.team_member_id AND m.is_active
		RETURNING m.id, m.telegram_id, m.name, m.role, m.is_active`, hashToken(sid))
	m, err := collectOne[TeamMember](rows)
	return m, notFound(err)
}

func (s *Store) DeleteSession(ctx context.Context, sid string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM admin_session WHERE token_hash = $1`, hashToken(sid))
	return err
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}
