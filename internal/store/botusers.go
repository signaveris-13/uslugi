package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// TelegramUser — то, что бот знает о пользователе из апдейта.
type TelegramUser struct {
	ID           int64
	Username     string
	FirstName    string
	LastName     string
	LanguageCode string
}

func (u TelegramUser) Name() string {
	return strings.TrimSpace(u.FirstName + " " + u.LastName)
}

type User struct {
	ID         int64   `db:"id"`
	TelegramID int64   `db:"telegram_id"`
	Username   *string `db:"username"`
	FirstName  *string `db:"first_name"`
	CityID     *int64  `db:"city_id"`
	IsBlocked  bool    `db:"is_blocked"`
}

const userCols = `id, telegram_id, username::text AS username, first_name, city_id, is_blocked`

// UpsertBotUser заводит пользователя при первом сообщении и обновляет имя и username.
// startSource пишется только при создании: это метка первого перехода (src_<tag>).
func (s *Store) UpsertBotUser(ctx context.Context, tg TelegramUser, startSource string) (User, bool, error) {
	var created bool
	var u User
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		rows, _ := tx.Query(ctx, `
			INSERT INTO users (telegram_id, username, first_name, language_code, start_source)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (telegram_id) DO UPDATE
			   SET username = EXCLUDED.username, first_name = EXCLUDED.first_name,
			       language_code = EXCLUDED.language_code, last_seen_at = now()
			RETURNING `+userCols+`, (xmax = 0) AS created`,
			tg.ID, nz(tg.Username), nz(tg.Name()), nz(tg.LanguageCode), nz(startSource))
		type row struct {
			User
			Created bool `db:"created"`
		}
		r, err := collectOne[row](rows)
		if err != nil {
			return err
		}
		u, created = r.User, r.Created
		return nil
	})
	return u, created, err
}

func (s *Store) SetUserCity(ctx context.Context, userID, cityID int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET city_id = $2 WHERE id = $1`, userID, cityID)
	return err
}

// BotState — на какой шаг формы бот ждёт ответа.
type BotState struct {
	Step            string `db:"step"`
	ListingID       *int64 `db:"listing_id"`
	Field           string `db:"field"`
	ReturnToPreview bool   `db:"return_to_preview"`
}

func (s *Store) GetBotState(ctx context.Context, userID int64) (BotState, error) {
	rows, _ := s.pool.Query(ctx, `
		SELECT coalesce(step, '') AS step, listing_id, coalesce(field, '') AS field, return_to_preview
		FROM bot_state WHERE user_id = $1`, userID)
	st, err := collectOne[BotState](rows)
	if errors.Is(err, pgx.ErrNoRows) {
		return BotState{}, nil
	}
	return st, err
}

func (s *Store) SetBotState(ctx context.Context, userID int64, st BotState) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO bot_state (user_id, step, listing_id, field, return_to_preview, updated_at)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (user_id) DO UPDATE SET step = EXCLUDED.step, listing_id = EXCLUDED.listing_id,
		       field = EXCLUDED.field, return_to_preview = EXCLUDED.return_to_preview, updated_at = now()`,
		userID, nz(st.Step), st.ListingID, nz(st.Field), st.ReturnToPreview)
	return err
}

// BotText — текст бота из админки или запасной вариант.
func (s *Store) BotText(ctx context.Context, key, fallback string) string {
	var v string
	if err := s.pool.QueryRow(ctx, `SELECT value FROM bot_text WHERE key = $1`, key).Scan(&v); err != nil {
		return fallback
	}
	return v
}

// IntSetting — числовая настройка из app_setting.
func (s *Store) IntSetting(ctx context.Context, key string, fallback int) int {
	var v int
	if err := s.pool.QueryRow(ctx, `SELECT (value #>> '{}')::int FROM app_setting WHERE key = $1`, key).Scan(&v); err != nil {
		return fallback
	}
	return v
}

// LogEvent пишет событие аналитики (§11). Ошибки не мешают сценарию — их только возвращаем.
func (s *Store) LogEvent(ctx context.Context, typ string, userID int64, objectType string, objectID int64, source string, props map[string]any) error {
	if props == nil {
		props = map[string]any{}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO event (type, user_id, object_type, object_id, source, props)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		typ, nzID(userID), nz(objectType), nzID(objectID), nz(source), props)
	return err
}

// Справочники для бота ---------------------------------------------------

type ParentCategory struct {
	ID    int64  `db:"id"`
	Name  string `db:"name"`
	Emoji string `db:"emoji"`
}

// ListParentCategories — категории верхнего уровня, включённые в стране города.
func (s *Store) ListParentCategories(ctx context.Context, cityID int64) ([]ParentCategory, error) {
	rows, _ := s.pool.Query(ctx, `
		SELECT p.id, p.name, coalesce(p.emoji, '') AS emoji
		FROM category p
		JOIN category_country cc ON cc.category_id = p.id
		JOIN city ON city.country_id = cc.country_id AND city.id = $1
		WHERE p.parent_id IS NULL AND p.is_active
		  AND EXISTS (SELECT 1 FROM category s WHERE s.parent_id = p.id AND s.is_active)
		ORDER BY p.sort_order`, cityID)
	return collectAll[ParentCategory](rows)
}

// Option — пункт справочника для кнопок бота.
type Option struct {
	ID   int64  `db:"id"`
	Name string `db:"name"`
}

func (s *Store) ListChildCategories(ctx context.Context, parentID int64) ([]Option, error) {
	rows, _ := s.pool.Query(ctx, `
		SELECT id, name FROM category WHERE parent_id = $1 AND is_active ORDER BY sort_order`, parentID)
	return collectAll[Option](rows)
}

// CategoryParentID — родитель подкатегории.
func (s *Store) CategoryParentID(ctx context.Context, id int64) (int64, error) {
	var p *int64
	if err := s.pool.QueryRow(ctx, `SELECT parent_id FROM category WHERE id = $1`, id).Scan(&p); err != nil {
		return 0, notFound(err)
	}
	if p == nil {
		return 0, ErrNotFound
	}
	return *p, nil
}

func (s *Store) CityName(ctx context.Context, id int64) string {
	var n string
	_ = s.pool.QueryRow(ctx, `SELECT name FROM city WHERE id = $1`, id).Scan(&n)
	return n
}

// Исполнители --------------------------------------------------------------

type Provider struct {
	ID              int64      `db:"id"`
	UserID          int64      `db:"user_id"`
	DisplayName     string     `db:"display_name"`
	TermsAcceptedAt *time.Time `db:"terms_accepted_at"`
	ListingLimit    *int       `db:"listing_limit"`
	IsBlocked       bool       `db:"is_blocked"`
}

const providerCols = `id, user_id, display_name, terms_accepted_at, listing_limit, is_blocked`

func (s *Store) ProviderByUser(ctx context.Context, userID int64) (Provider, error) {
	rows, _ := s.pool.Query(ctx, `SELECT `+providerCols+` FROM provider WHERE user_id = $1`, userID)
	p, err := collectOne[Provider](rows)
	return p, notFound(err)
}

// EnsureProvider делает пользователя исполнителем. Если в CRM есть лид с тем же
// username и он ещё ни к кому не привязан, связывает его (воронка аутрича).
func (s *Store) EnsureProvider(ctx context.Context, u User, displayName string) (Provider, error) {
	if p, err := s.ProviderByUser(ctx, u.ID); err == nil {
		return p, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Provider{}, err
	}
	if displayName == "" {
		displayName = "Исполнитель"
	}
	var p Provider
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		rows, _ := tx.Query(ctx, `
			INSERT INTO provider (user_id, display_name) VALUES ($1, $2)
			ON CONFLICT (user_id) DO UPDATE SET display_name = provider.display_name
			RETURNING `+providerCols, u.ID, displayName)
		var err error
		if p, err = collectOne[Provider](rows); err != nil {
			return err
		}
		if u.Username != nil {
			_, err = tx.Exec(ctx, `
				UPDATE lead SET provider_id = $1, telegram_user_id = $2, updated_at = now()
				WHERE id = (SELECT id FROM lead WHERE telegram_username = $3::citext AND provider_id IS NULL
				            ORDER BY updated_at DESC LIMIT 1)`, p.ID, u.TelegramID, *u.Username)
		}
		return err
	})
	return p, err
}

func (s *Store) AcceptTerms(ctx context.Context, providerID int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE provider SET terms_accepted_at = now() WHERE id = $1 AND terms_accepted_at IS NULL`, providerID)
	return err
}

// ProviderListingLimit — сколько активных объявлений можно держать (FR-P-08).
func (s *Store) ProviderListingLimit(ctx context.Context, p Provider) int {
	if p.ListingLimit != nil {
		return *p.ListingLimit
	}
	return s.IntSetting(ctx, "listing_limit", 3)
}

// Приглашения из CRM (FR-P-10) ----------------------------------------------

// LeadByInviteToken находит лид по токену из ссылки ?start=inv_<token>.
func (s *Store) LeadByInviteToken(ctx context.Context, token string) (Lead, error) {
	rows, _ := s.pool.Query(ctx, leadSelect+" WHERE l.invite_token = $1", token)
	l, err := collectOne[Lead](rows)
	return l, notFound(err)
}

// AttachLeadToProvider связывает лид с исполнителем, который пришёл по приглашению,
// и продвигает воронку до «Заинтересован», если лид был раньше по воронке.
func (s *Store) AttachLeadToProvider(ctx context.Context, leadID, providerID, telegramUserID int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE lead SET provider_id = $2, telegram_user_id = $3, updated_at = now(),
		       status = CASE WHEN status IN ('found', 'review', 'qualified', 'invited', 'replied') THEN 'interested' ELSE status END
		WHERE id = $1`, leadID, providerID, telegramUserID)
	return err
}

func (s *Store) SetLeadListing(ctx context.Context, leadID, listingID int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE lead SET listing_id = $2, updated_at = now() WHERE id = $1`, leadID, listingID)
	return err
}
