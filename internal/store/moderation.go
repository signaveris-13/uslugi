package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Шаблоны причин отказа (§7). Модератор может дописать свою.
var RejectReasons = []string{
	"Категория не соответствует услуге",
	"Контакты не работают или указаны неверно",
	"Мало информации: опишите услуги подробнее",
	"Запрещённая или лицензируемая услуга",
	"Дубль существующего объявления",
	"Фото не относятся к услуге",
}

// ListingRow — строка в списке объявлений админки.
type ListingRow struct {
	ID           int64      `db:"id"`
	Title        *string    `db:"title"`
	Status       string     `db:"status"`
	CategoryName *string    `db:"category_name"`
	Cities       *string    `db:"cities"`
	ProviderName string     `db:"provider_name"`
	Username     *string    `db:"username"`
	SubmittedAt  *time.Time `db:"submitted_at"`
	UpdatedAt    time.Time  `db:"updated_at"`
}

type ListingFilter struct {
	Status string
	Query  string
}

// ListListings — объявления для админки. Очередь модерации — сначала самые старые.
func (s *Store) ListListings(ctx context.Context, f ListingFilter) ([]ListingRow, error) {
	var where []string
	var args []any
	if f.Status != "" {
		args = append(args, f.Status)
		where = append(where, fmt.Sprintf("l.status = $%d", len(args)))
	} else {
		where = append(where, "l.status <> 'draft'")
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		args = append(args, q)
		n := len(args)
		where = append(where, fmt.Sprintf(`(l.title ILIKE '%%' || $%[1]d || '%%' OR p.display_name ILIKE '%%' || $%[1]d || '%%'
			OR u.username::text ILIKE '%%' || $%[1]d || '%%' OR l.contact_phone ILIKE '%%' || $%[1]d || '%%')`, n))
	}
	order := "l.updated_at DESC"
	if f.Status == ListingModeration {
		order = "l.submitted_at ASC NULLS LAST"
	}
	rows, _ := s.pool.Query(ctx, `
		SELECT l.id, l.title, l.status,
		       (SELECT c.name FROM listing_category lc JOIN category c ON c.id = lc.category_id
		         WHERE lc.listing_id = l.id AND lc.is_primary) AS category_name,
		       (SELECT string_agg(ci.name, ', ' ORDER BY ci.sort_order) FROM listing_city lci JOIN city ci ON ci.id = lci.city_id
		         WHERE lci.listing_id = l.id) AS cities,
		       p.display_name AS provider_name, u.username::text AS username,
		       l.submitted_at, l.updated_at
		FROM listing l
		JOIN provider p ON p.id = l.provider_id
		JOIN users u ON u.id = p.user_id
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY `+order+` LIMIT 300`, args...)
	return collectAll[ListingRow](rows)
}

func (s *Store) CountModerationQueue(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM listing WHERE status = 'moderation'`).Scan(&n)
	return n, err
}

// ProviderInfo — кто разместил объявление.
type ProviderInfo struct {
	ID          int64      `db:"id"`
	DisplayName string     `db:"display_name"`
	TelegramID  int64      `db:"telegram_id"`
	Username    *string    `db:"username"`
	IsBlocked   bool       `db:"is_blocked"`
	CreatedAt   time.Time  `db:"created_at"`
	TermsAt     *time.Time `db:"terms_accepted_at"`
	Listings    int        `db:"listings"`
	LeadID      *int64     `db:"lead_id"`
}

func (s *Store) ProviderInfo(ctx context.Context, providerID int64) (ProviderInfo, error) {
	rows, _ := s.pool.Query(ctx, `
		SELECT p.id, p.display_name, u.telegram_id, u.username::text AS username, p.is_blocked, p.created_at,
		       p.terms_accepted_at,
		       (SELECT count(*) FROM listing WHERE provider_id = p.id AND status <> 'draft')::int AS listings,
		       (SELECT id FROM lead WHERE provider_id = p.id ORDER BY updated_at DESC LIMIT 1) AS lead_id
		FROM provider p JOIN users u ON u.id = p.user_id WHERE p.id = $1`, providerID)
	pi, err := collectOne[ProviderInfo](rows)
	return pi, notFound(err)
}

// ListingDuplicates — другие объявления с теми же контактами (§7: предупреждение модератору).
func (s *Store) ListingDuplicates(ctx context.Context, l Listing) ([]ListingRow, error) {
	if l.ContactTelegram == nil && l.ContactPhone == nil && l.ContactWhatsApp == nil {
		return nil, nil
	}
	rows, _ := s.pool.Query(ctx, `
		SELECT l.id, l.title, l.status, NULL::text AS category_name, NULL::text AS cities,
		       p.display_name AS provider_name, u.username::text AS username, l.submitted_at, l.updated_at
		FROM listing l JOIN provider p ON p.id = l.provider_id JOIN users u ON u.id = p.user_id
		WHERE l.id <> $1 AND l.status NOT IN ('draft', 'archived') AND (
		      ($2::citext IS NOT NULL AND l.contact_telegram = $2::citext)
		   OR ($3::text IS NOT NULL AND (l.contact_phone = $3 OR l.contact_whatsapp = $3))
		   OR ($4::text IS NOT NULL AND (l.contact_phone = $4 OR l.contact_whatsapp = $4)))
		ORDER BY l.id LIMIT 10`, l.ID, l.ContactTelegram, l.ContactPhone, l.ContactWhatsApp)
	return collectAll[ListingRow](rows)
}

// ModerationResult — кому и что сообщить после решения модератора.
type ModerationResult struct {
	TelegramID int64
	Title      string
}

// ApproveListing публикует объявление (FR-P-05). Лид исполнителя — «Опубликован».
func (s *Store) ApproveListing(ctx context.Context, actorID, listingID int64) (ModerationResult, error) {
	var res ModerationResult
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var title *string
		err := tx.QueryRow(ctx, `
			UPDATE listing l SET status = 'published', published_at = coalesce(published_at, now()),
			       actualized_at = now(), rejection_reason = NULL, updated_at = now()
			FROM provider p JOIN users u ON u.id = p.user_id
			WHERE l.id = $1 AND l.status = 'moderation' AND p.id = l.provider_id
			RETURNING u.telegram_id, l.title`, listingID).Scan(&res.TelegramID, &title)
		if err != nil {
			return fmt.Errorf("одобрить можно только объявление на модерации: %w", notFound(err))
		}
		res.Title = deref(title)
		if _, err := tx.Exec(ctx, `
			UPDATE lead SET status = 'published', updated_at = now()
			WHERE listing_id = $1 AND status NOT IN ('declined', 'do_not_contact', 'invalid')`, listingID); err != nil {
			return err
		}
		return audit(ctx, tx, actorID, "listing.approve", "listing", listingID, nil)
	})
	return res, err
}

// RejectListing отклоняет объявление с причиной; исполнитель исправляет его в боте.
func (s *Store) RejectListing(ctx context.Context, actorID, listingID int64, reason string) (ModerationResult, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ModerationResult{}, ValidationError{"Укажи причину отказа: исполнитель увидит её в боте."}
	}
	var res ModerationResult
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var title *string
		err := tx.QueryRow(ctx, `
			UPDATE listing l SET status = 'rejected', rejection_reason = $2, updated_at = now()
			FROM provider p JOIN users u ON u.id = p.user_id
			WHERE l.id = $1 AND l.status IN ('moderation', 'published') AND p.id = l.provider_id
			RETURNING u.telegram_id, l.title`, listingID, reason).Scan(&res.TelegramID, &title)
		if err != nil {
			return fmt.Errorf("отклонить можно объявление на модерации или опубликованное: %w", notFound(err))
		}
		res.Title = deref(title)
		return audit(ctx, tx, actorID, "listing.reject", "listing", listingID, map[string]any{"reason": reason})
	})
	return res, err
}

// BlockProvider блокирует исполнителя и снимает все его объявления (§7).
func (s *Store) BlockProvider(ctx context.Context, actorID, providerID int64, reason string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE provider SET is_blocked = true, blocked_reason = $2 WHERE id = $1`, providerID, nz(strings.TrimSpace(reason)))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if _, err := tx.Exec(ctx, `
			UPDATE listing SET status = 'archived', updated_at = now()
			WHERE provider_id = $1 AND status IN ('moderation', 'published', 'paused', 'rejected')`, providerID); err != nil {
			return err
		}
		return audit(ctx, tx, actorID, "provider.block", "provider", providerID, map[string]any{"reason": reason})
	})
}

func (s *Store) UnblockProvider(ctx context.Context, actorID, providerID int64) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE provider SET is_blocked = false, blocked_reason = NULL WHERE id = $1`, providerID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return audit(ctx, tx, actorID, "provider.unblock", "provider", providerID, nil)
	})
}

// ListingHistory — решения модераторов по объявлению.
type ListingEvent struct {
	Action    string    `db:"action"`
	ActorName *string   `db:"actor_name"`
	Reason    *string   `db:"reason"`
	CreatedAt time.Time `db:"created_at"`
}

func (s *Store) ListingHistory(ctx context.Context, listingID int64) ([]ListingEvent, error) {
	rows, _ := s.pool.Query(ctx, `
		SELECT a.action, tm.name AS actor_name, a.details->>'reason' AS reason, a.created_at
		FROM audit_log a LEFT JOIN team_member tm ON tm.id = a.actor_id
		WHERE a.object_type = 'listing' AND a.object_id = $1
		ORDER BY a.created_at DESC LIMIT 20`, listingID)
	return collectAll[ListingEvent](rows)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
