package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Статусы лида в порядке воронки (§8.3). Последние три — боковые.
const (
	LeadFound          = "found"
	LeadReview         = "review"
	LeadQualified      = "qualified"
	LeadInvited        = "invited"
	LeadReplied        = "replied"
	LeadInterested     = "interested"
	LeadListingCreated = "listing_created"
	LeadPublished      = "published"
	LeadDeclined       = "declined"
	LeadDoNotContact   = "do_not_contact"
	LeadInvalid        = "invalid"
)

var LeadStatuses = []string{
	LeadFound, LeadReview, LeadQualified, LeadInvited, LeadReplied, LeadInterested,
	LeadListingCreated, LeadPublished, LeadDeclined, LeadDoNotContact, LeadInvalid,
}

var LeadStatusLabels = map[string]string{
	LeadFound:          "Найден",
	LeadReview:         "На ревью",
	LeadQualified:      "Квалифицирован",
	LeadInvited:        "Приглашён",
	LeadReplied:        "Ответил",
	LeadInterested:     "Заинтересован",
	LeadListingCreated: "Объявление создано",
	LeadPublished:      "Опубликован",
	LeadDeclined:       "Отказ",
	LeadDoNotContact:   "Не беспокоить",
	LeadInvalid:        "Невалидный",
}

var LeadSources = []string{"manual", "crawler", "instagram", "referral"}

var LeadSourceLabels = map[string]string{
	"manual":    "Вручную",
	"crawler":   "Краулер",
	"instagram": "Instagram",
	"referral":  "Рекомендация",
}

// ErrDoNotContact — исходящее касание контакту со статусом «Не беспокоить».
var ErrDoNotContact = errors.New("контакт просил не беспокоить: исходящие сообщения запрещены")

// ValidationError — ошибка в данных от оператора; её показываем в форме как есть.
type ValidationError struct{ Msg string }

func (e ValidationError) Error() string { return e.Msg }

// ErrInvalidLead — в лиде не хватает данных.
var ErrInvalidLead = ValidationError{"Заполни имя, Telegram username или телефон."}

type Lead struct {
	ID               int64      `db:"id"`
	Source           string     `db:"source"`
	SourceURL        *string    `db:"source_url"`
	SourceText       *string    `db:"source_text"`
	Name             *string    `db:"name"`
	CategoryID       *int64     `db:"category_id"`
	CategoryName     *string    `db:"category_name"`
	CityID           *int64     `db:"city_id"`
	CityName         *string    `db:"city_name"`
	TelegramUsername *string    `db:"telegram_username"`
	Phone            *string    `db:"phone"`
	WhatsApp         *string    `db:"whatsapp"`
	Instagram        *string    `db:"instagram"`
	Summary          *string    `db:"summary"`
	Status           string     `db:"status"`
	AssigneeID       *int64     `db:"assignee_id"`
	AssigneeName     *string    `db:"assignee_name"`
	InviteToken      *string    `db:"invite_token"`
	InvitedAt        *time.Time `db:"invited_at"`
	ProviderID       *int64     `db:"provider_id"`
	ListingID        *int64     `db:"listing_id"`
	Notes            *string    `db:"notes"`
	Tags             []string   `db:"tags"`
	CreatedAt        time.Time  `db:"created_at"`
	UpdatedAt        time.Time  `db:"updated_at"`
}

// DisplayName — как показывать лида в списках.
func (l Lead) DisplayName() string {
	switch {
	case l.Name != nil && *l.Name != "":
		return *l.Name
	case l.TelegramUsername != nil && *l.TelegramUsername != "":
		return "@" + *l.TelegramUsername
	case l.Phone != nil && *l.Phone != "":
		return *l.Phone
	}
	return fmt.Sprintf("Лид #%d", l.ID)
}

// LeadInput — поля, которые оператор заполняет в форме.
type LeadInput struct {
	Source           string
	SourceURL        string
	SourceText       string
	Name             string
	CategoryID       int64
	CityID           int64
	TelegramUsername string
	Phone            string
	WhatsApp         string
	Instagram        string
	Summary          string
	Notes            string
	Tags             []string
	Status           string // только при создании
}

type LeadFilter struct {
	Status     string
	CityID     int64
	CategoryID int64
	Source     string
	AssigneeID int64 // 0 — все, -1 — без ответственного
	Query      string
	Limit      int
}

const leadSelect = `
	SELECT l.id, l.source, l.source_url, l.source_text, l.name,
	       l.category_id, cat.name AS category_name,
	       l.city_id, city.name AS city_name,
	       l.telegram_username::text AS telegram_username, l.phone, l.whatsapp, l.instagram,
	       l.summary, l.status, l.assignee_id, tm.name AS assignee_name,
	       l.invite_token, l.invited_at, l.provider_id, l.listing_id, l.notes, l.tags,
	       l.created_at, l.updated_at
	FROM lead l
	LEFT JOIN category cat ON cat.id = l.category_id
	LEFT JOIN city ON city.id = l.city_id
	LEFT JOIN team_member tm ON tm.id = l.assignee_id`

func (s *Store) ListLeads(ctx context.Context, f LeadFilter) ([]Lead, error) {
	var where []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.Status != "" {
		add("l.status = $%d", f.Status)
	}
	if f.CityID > 0 {
		add("l.city_id = $%d", f.CityID)
	}
	if f.CategoryID > 0 {
		add("l.category_id = $%d", f.CategoryID)
	}
	if f.Source != "" {
		add("l.source = $%d", f.Source)
	}
	switch {
	case f.AssigneeID > 0:
		add("l.assignee_id = $%d", f.AssigneeID)
	case f.AssigneeID < 0:
		where = append(where, "l.assignee_id IS NULL")
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		add(`(l.name ILIKE '%%' || $%[1]d || '%%' OR l.telegram_username::text ILIKE '%%' || $%[1]d || '%%'
		      OR l.phone ILIKE '%%' || $%[1]d || '%%' OR l.summary ILIKE '%%' || $%[1]d || '%%')`, q)
	}

	sql := leadSelect
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	sql += fmt.Sprintf(" ORDER BY l.updated_at DESC LIMIT %d", limit)

	rows, _ := s.pool.Query(ctx, sql, args...)
	return collectAll[Lead](rows)
}

func (s *Store) GetLead(ctx context.Context, id int64) (Lead, error) {
	rows, _ := s.pool.Query(ctx, leadSelect+" WHERE l.id = $1", id)
	l, err := collectOne[Lead](rows)
	return l, notFound(err)
}

// FindLeadDuplicates — другие лиды с тем же username или телефоном (FR-C-04).
func (s *Store) FindLeadDuplicates(ctx context.Context, l Lead) ([]Lead, error) {
	if l.TelegramUsername == nil && l.Phone == nil {
		return nil, nil
	}
	rows, _ := s.pool.Query(ctx, leadSelect+`
		WHERE l.id <> $1 AND (
		      ($2::citext IS NOT NULL AND l.telegram_username = $2::citext)
		   OR ($3::text IS NOT NULL AND l.phone = $3::text))
		ORDER BY l.id LIMIT 10`, l.ID, l.TelegramUsername, l.Phone)
	return collectAll[Lead](rows)
}

func (s *Store) CreateLead(ctx context.Context, actorID int64, in LeadInput) (int64, error) {
	in = normalizeLead(in)
	if err := validateLead(in); err != nil {
		return 0, err
	}
	status := in.Status
	if !validStatus(status) {
		status = LeadQualified
	}

	if err := s.checkSubcategory(ctx, in.CategoryID); err != nil {
		return 0, err
	}

	var id int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			INSERT INTO lead (source, source_url, source_text, name, category_id, city_id,
			                  telegram_username, phone, whatsapp, instagram, summary, notes, tags,
			                  status, assignee_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
			RETURNING id`,
			in.Source, nz(in.SourceURL), nz(in.SourceText), nz(in.Name), nzID(in.CategoryID), nzID(in.CityID),
			nz(in.TelegramUsername), nz(in.Phone), nz(in.WhatsApp), nz(in.Instagram), nz(in.Summary), nz(in.Notes),
			in.Tags, status, actorID,
		).Scan(&id)
		if err != nil {
			return err
		}
		return audit(ctx, tx, actorID, "lead.create", "lead", id, nil)
	})
	return id, err
}

func (s *Store) UpdateLead(ctx context.Context, actorID, id int64, in LeadInput) error {
	in = normalizeLead(in)
	if err := validateLead(in); err != nil {
		return err
	}
	if err := s.checkSubcategory(ctx, in.CategoryID); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE lead SET source = $2, source_url = $3, source_text = $4, name = $5,
			       category_id = $6, city_id = $7, telegram_username = $8, phone = $9,
			       whatsapp = $10, instagram = $11, summary = $12, notes = $13, tags = $14,
			       updated_at = now()
			WHERE id = $1`,
			id, in.Source, nz(in.SourceURL), nz(in.SourceText), nz(in.Name), nzID(in.CategoryID), nzID(in.CityID),
			nz(in.TelegramUsername), nz(in.Phone), nz(in.WhatsApp), nz(in.Instagram), nz(in.Summary), nz(in.Notes),
			in.Tags)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return audit(ctx, tx, actorID, "lead.update", "lead", id, nil)
	})
}

func (s *Store) SetLeadStatus(ctx context.Context, actorID, id int64, status string) error {
	if !validStatus(status) {
		return fmt.Errorf("неизвестный статус %q", status)
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var old string
		err := tx.QueryRow(ctx, `SELECT status FROM lead WHERE id = $1 FOR UPDATE`, id).Scan(&old)
		if err != nil {
			return notFound(err)
		}
		if old == status {
			return nil
		}
		if err := setStatus(ctx, tx, id, status); err != nil {
			return err
		}
		return audit(ctx, tx, actorID, "lead.status", "lead", id, map[string]any{"from": old, "to": status})
	})
}

func (s *Store) SetLeadAssignee(ctx context.Context, actorID, id int64, assigneeID int64) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE lead SET assignee_id = $2, updated_at = now() WHERE id = $1`, id, nzID(assigneeID))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return audit(ctx, tx, actorID, "lead.assignee", "lead", id, map[string]any{"assignee_id": assigneeID})
	})
}

// EnsureInviteToken выдаёт токен для ссылки ?start=inv_<token> (FR-P-10),
// создавая его при первом вызове.
func (s *Store) EnsureInviteToken(ctx context.Context, actorID, id int64) (string, error) {
	var token string
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var existing *string
		if err := tx.QueryRow(ctx, `SELECT invite_token FROM lead WHERE id = $1 FOR UPDATE`, id).Scan(&existing); err != nil {
			return notFound(err)
		}
		if existing != nil {
			token = *existing
			return nil
		}
		t, err := randomToken(12)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE lead SET invite_token = $2, updated_at = now() WHERE id = $1`, id, t); err != nil {
			return err
		}
		token = t
		return audit(ctx, tx, actorID, "lead.invite_link", "lead", id, nil)
	})
	return token, err
}

// Touch — касание по лиду: сообщение, которое оператор отправил или получил.
type Touch struct {
	ID         int64     `db:"id"`
	Direction  string    `db:"direction"`
	Text       *string   `db:"text"`
	AuthorName *string   `db:"author_name"`
	SentAt     time.Time `db:"sent_at"`
}

func (s *Store) ListTouches(ctx context.Context, leadID int64) ([]Touch, error) {
	rows, _ := s.pool.Query(ctx, `
		SELECT m.id, m.direction, m.text, tm.name AS author_name, m.sent_at
		FROM lead_message m LEFT JOIN team_member tm ON tm.id = m.author_id
		WHERE m.lead_id = $1 ORDER BY m.sent_at, m.id`, leadID)
	return collectAll[Touch](rows)
}

// AddTouch отмечает касание (Q-07: оператор пишет из Telegram и фиксирует здесь).
// Первое исходящее переводит лида в «Приглашён», входящее после приглашения — в «Ответил».
func (s *Store) AddTouch(ctx context.Context, actorID, leadID int64, direction, text string) error {
	if direction != "outgoing" && direction != "incoming" {
		return fmt.Errorf("неизвестное направление %q", direction)
	}
	text = strings.TrimSpace(text)
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM lead WHERE id = $1 FOR UPDATE`, leadID).Scan(&status); err != nil {
			return notFound(err)
		}
		if direction == "outgoing" && status == LeadDoNotContact {
			return ErrDoNotContact
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO lead_message (lead_id, direction, text, author_id) VALUES ($1, $2, $3, $4)`,
			leadID, direction, nz(text), actorID); err != nil {
			return err
		}

		next := status
		switch {
		case direction == "outgoing" && (status == LeadFound || status == LeadReview || status == LeadQualified):
			next = LeadInvited
		case direction == "incoming" && status == LeadInvited:
			next = LeadReplied
		}
		if next != status {
			if err := setStatus(ctx, tx, leadID, next); err != nil {
				return err
			}
		} else if _, err := tx.Exec(ctx, `UPDATE lead SET updated_at = now() WHERE id = $1`, leadID); err != nil {
			return err
		}
		return audit(ctx, tx, actorID, "lead.touch", "lead", leadID,
			map[string]any{"direction": direction, "status_from": status, "status_to": next})
	})
}

// checkSubcategory: у лида, как и у объявления, категория — это подкатегория.
func (s *Store) checkSubcategory(ctx context.Context, id int64) error {
	if id == 0 {
		return nil
	}
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM category WHERE id = $1 AND parent_id IS NOT NULL)`, id).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return ValidationError{"Выбери подкатегорию из списка."}
	}
	return nil
}

func setStatus(ctx context.Context, tx pgx.Tx, id int64, status string) error {
	_, err := tx.Exec(ctx, `
		UPDATE lead SET status = $2, updated_at = now(),
		       invited_at = CASE WHEN $2 = 'invited' AND invited_at IS NULL THEN now() ELSE invited_at END
		WHERE id = $1`, id, status)
	return err
}

func validStatus(s string) bool {
	_, ok := LeadStatusLabels[s]
	return ok
}

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9_]{4,32}$`)

func normalizeLead(in LeadInput) LeadInput {
	trim := strings.TrimSpace
	in.Name, in.SourceURL, in.SourceText = trim(in.Name), trim(in.SourceURL), trim(in.SourceText)
	in.Phone, in.WhatsApp, in.Instagram = trim(in.Phone), trim(in.WhatsApp), trim(in.Instagram)
	in.Summary, in.Notes = trim(in.Summary), trim(in.Notes)

	u := trim(in.TelegramUsername)
	for _, p := range []string{"https://", "http://", "t.me/", "telegram.me/", "@"} {
		u = strings.TrimPrefix(u, p)
	}
	in.TelegramUsername = strings.TrimSuffix(u, "/")

	if in.Source == "" {
		in.Source = "manual"
	}
	var tags []string
	for _, t := range in.Tags {
		if t = trim(t); t != "" {
			tags = append(tags, t)
		}
	}
	in.Tags = tags
	if in.Tags == nil {
		in.Tags = []string{}
	}
	return in
}

func validateLead(in LeadInput) error {
	if in.Name == "" && in.TelegramUsername == "" && in.Phone == "" {
		return ErrInvalidLead
	}
	if in.TelegramUsername != "" && !usernameRe.MatchString(in.TelegramUsername) {
		return ValidationError{fmt.Sprintf("Username %q: только латиница, цифры и _, от 4 до 32 символов.", in.TelegramUsername)}
	}
	if _, ok := LeadSourceLabels[in.Source]; !ok {
		return ValidationError{fmt.Sprintf("Неизвестный источник %q.", in.Source)}
	}
	return nil
}

// nz превращает пустую строку в NULL.
func nz(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// nzID превращает 0 в NULL.
func nzID(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}
