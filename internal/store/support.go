package store

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Поддержка: обращения из Mini App, ответы команды из админки.

var SupportTopics = []string{"listing", "reviews", "provider", "bug", "idea", "other"}

var SupportTopicLabels = map[string]string{
	"listing":  "Моё объявление",
	"reviews":  "Отзывы",
	"provider": "Проблема с мастером",
	"bug":      "Ошибка в приложении",
	"idea":     "Идея или предложение",
	"other":    "Другое",
}

var SupportStatusLabels = map[string]string{
	"open":     "Ждёт ответа",
	"answered": "Ответили",
	"closed":   "Закрыто",
}

type SupportTicket struct {
	ID           int64     `db:"id"`
	UserID       int64     `db:"user_id"`
	UserName     *string   `db:"user_name"`
	Username     *string   `db:"username"`
	TelegramID   int64     `db:"telegram_id"`
	Topic        string    `db:"topic"`
	ListingID    *int64    `db:"listing_id"`
	ListingTitle *string   `db:"listing_title"`
	Status       string    `db:"status"`
	FirstText    string    `db:"first_text"`
	CreatedAt    time.Time `db:"created_at"`
	UpdatedAt    time.Time `db:"updated_at"`
}

type SupportMessage struct {
	ID         int64     `db:"id"`
	FromTeam   bool      `db:"from_team"`
	AuthorName *string   `db:"author_name"`
	Text       string    `db:"text"`
	CreatedAt  time.Time `db:"created_at"`
}

const ticketSelect = `
	SELECT t.id, t.user_id, u.first_name AS user_name, u.username::text AS username, u.telegram_id,
	       t.topic, t.listing_id, l.title AS listing_title, t.status,
	       coalesce((SELECT m.text FROM support_message m WHERE m.ticket_id = t.id ORDER BY m.created_at LIMIT 1), '') AS first_text,
	       t.created_at, t.updated_at
	FROM support_ticket t
	JOIN users u ON u.id = t.user_id
	LEFT JOIN listing l ON l.id = t.listing_id`

func checkSupportText(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ValidationError{"Напишите, что случилось."}
	}
	if len([]rune(text)) > 2000 {
		return "", ValidationError{"Сообщение — до 2000 символов."}
	}
	return text, nil
}

// CreateTicket — новое обращение. listingID проверяем: только своё объявление.
func (s *Store) CreateTicket(ctx context.Context, userID int64, topic string, listingID int64, text string) (int64, error) {
	if SupportTopicLabels[topic] == "" {
		return 0, ValidationError{"Выберите тему обращения."}
	}
	text, err := checkSupportText(text)
	if err != nil {
		return 0, err
	}
	var lid *int64
	if listingID > 0 {
		var ok bool
		_ = s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM listing l JOIN provider p ON p.id = l.provider_id
			WHERE l.id = $1 AND p.user_id = $2)`, listingID, userID).Scan(&ok)
		if ok {
			lid = &listingID
		}
	}
	var id int64
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO support_ticket (user_id, topic, listing_id) VALUES ($1, $2, $3) RETURNING id`,
			userID, topic, lid).Scan(&id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO support_message (ticket_id, text) VALUES ($1, $2)`, id, text)
		return err
	})
	return id, err
}

func (s *Store) UserTickets(ctx context.Context, userID int64) ([]SupportTicket, error) {
	rows, _ := s.pool.Query(ctx, ticketSelect+" WHERE t.user_id = $1 ORDER BY t.updated_at DESC LIMIT 30", userID)
	return collectAll[SupportTicket](rows)
}

// Ticket — обращение; userID = 0 — для команды (без проверки владельца).
func (s *Store) Ticket(ctx context.Context, id, userID int64) (SupportTicket, []SupportMessage, error) {
	rows, _ := s.pool.Query(ctx, ticketSelect+" WHERE t.id = $1 AND ($2 = 0 OR t.user_id = $2)", id, userID)
	t, err := collectOne[SupportTicket](rows)
	if err != nil {
		return SupportTicket{}, nil, notFound(err)
	}
	rows, _ = s.pool.Query(ctx, `
		SELECT m.id, m.from_team, tm.name AS author_name, m.text, m.created_at
		FROM support_message m LEFT JOIN team_member tm ON tm.id = m.team_member_id
		WHERE m.ticket_id = $1 ORDER BY m.created_at`, id)
	msgs, err := collectAll[SupportMessage](rows)
	return t, msgs, err
}

// AddUserMessage — пользователь дописал в обращение: оно снова ждёт ответа.
func (s *Store) AddUserMessage(ctx context.Context, ticketID, userID int64, text string) error {
	text, err := checkSupportText(text)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE support_ticket SET status = 'open', updated_at = now() WHERE id = $1 AND user_id = $2`, ticketID, userID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		_, err = tx.Exec(ctx, `INSERT INTO support_message (ticket_id, text) VALUES ($1, $2)`, ticketID, text)
		return err
	})
}

func (s *Store) CloseTicket(ctx context.Context, ticketID, userID int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE support_ticket SET status = 'closed', updated_at = now()
		WHERE id = $1 AND ($2 = 0 OR user_id = $2)`, ticketID, userID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// Команда ---------------------------------------------------------------------

func (s *Store) ListTickets(ctx context.Context, status string) ([]SupportTicket, error) {
	rows, _ := s.pool.Query(ctx, ticketSelect+`
		WHERE ($1 = '' OR t.status = $1)
		ORDER BY (t.status = 'open') DESC, t.updated_at LIMIT 200`, status)
	return collectAll[SupportTicket](rows)
}

func (s *Store) CountOpenTickets(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM support_ticket WHERE status = 'open'`).Scan(&n)
	return n, err
}

// AnswerTicket — ответ команды; возвращает Telegram ID автора для уведомления.
func (s *Store) AnswerTicket(ctx context.Context, actorID, ticketID int64, text string) (int64, error) {
	text, err := checkSupportText(text)
	if err != nil {
		return 0, err
	}
	var tgID int64
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			UPDATE support_ticket t SET status = 'answered', updated_at = now() FROM users u
			WHERE t.id = $1 AND u.id = t.user_id RETURNING u.telegram_id`, ticketID).Scan(&tgID); err != nil {
			return notFound(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO support_message (ticket_id, from_team, team_member_id, text) VALUES ($1, true, $2, $3)`,
			ticketID, actorID, text); err != nil {
			return err
		}
		return audit(ctx, tx, actorID, "support.answer", "support_ticket", ticketID, nil)
	})
	return tgID, err
}
