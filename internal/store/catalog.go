package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Каталог для клиента: только опубликованные объявления в городе пользователя.
// Объявление попадает во все свои подкатегории — основную и дополнительные.

// CatalogItem — категория или подкатегория со счётчиком объявлений.
type CatalogItem struct {
	ID    int64  `db:"id"`
	Name  string `db:"name"`
	Emoji string `db:"emoji"`
	Count int    `db:"count"`
}

// CatalogCategories — категории города со счётчиком; пустые скрыты (FR-B-02).
func (s *Store) CatalogCategories(ctx context.Context, cityID int64) ([]CatalogItem, error) {
	rows, _ := s.pool.Query(ctx, `
		SELECT p.id, p.name, coalesce(p.emoji, '') AS emoji, count(DISTINCT l.id)::int AS count
		FROM category p
		JOIN category sub ON sub.parent_id = p.id AND sub.is_active
		JOIN listing_category lc ON lc.category_id = sub.id
		JOIN listing l ON l.id = lc.listing_id AND l.status = 'published'
		JOIN listing_city lci ON lci.listing_id = l.id AND lci.city_id = $1
		WHERE p.is_active
		GROUP BY p.id
		ORDER BY p.sort_order`, cityID)
	return collectAll[CatalogItem](rows)
}

// CatalogSubcategories — подкатегории категории со счётчиком в городе; пустые скрыты.
func (s *Store) CatalogSubcategories(ctx context.Context, parentID, cityID int64) ([]CatalogItem, error) {
	rows, _ := s.pool.Query(ctx, `
		SELECT sub.id, sub.name, '' AS emoji, count(DISTINCT l.id)::int AS count
		FROM category sub
		JOIN listing_category lc ON lc.category_id = sub.id
		JOIN listing l ON l.id = lc.listing_id AND l.status = 'published'
		JOIN listing_city lci ON lci.listing_id = l.id AND lci.city_id = $2
		WHERE sub.parent_id = $1 AND sub.is_active
		GROUP BY sub.id
		ORDER BY sub.sort_order`, parentID, cityID)
	return collectAll[CatalogItem](rows)
}

func (s *Store) CategoryName(ctx context.Context, id int64) (string, string, error) {
	var name, emoji string
	err := s.pool.QueryRow(ctx, `
		SELECT c.name, coalesce(coalesce(c.emoji, p.emoji), '') FROM category c LEFT JOIN category p ON p.id = c.parent_id
		WHERE c.id = $1`, id).Scan(&name, &emoji)
	return name, emoji, notFound(err)
}

// CatalogRow — строка списка карточек.
type CatalogRow struct {
	ID          int64      `db:"id"`
	Title       *string    `db:"title"`
	PriceType   *string    `db:"price_type"`
	PriceAmount *float64   `db:"price_amount"`
	Currency    *string    `db:"currency"`
	PublishedAt *time.Time `db:"published_at"`
}

func (r CatalogRow) Listing() Listing {
	return Listing{ID: r.ID, Title: r.Title, PriceType: r.PriceType, PriceAmount: r.PriceAmount, Currency: r.Currency}
}

// CatalogListings — объявления подкатегории в городе: сначала свежие и поднятые (FR-B-03).
func (s *Store) CatalogListings(ctx context.Context, categoryID, cityID int64, offset, limit int) ([]CatalogRow, int, error) {
	var total int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM listing l
		JOIN listing_category lc ON lc.listing_id = l.id AND lc.category_id = $1
		JOIN listing_city lci ON lci.listing_id = l.id AND lci.city_id = $2
		WHERE l.status = 'published'`, categoryID, cityID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, _ := s.pool.Query(ctx, `
		SELECT l.id, l.title, l.price_type, l.price_amount::float8 AS price_amount, l.currency, l.published_at
		FROM listing l
		JOIN listing_category lc ON lc.listing_id = l.id AND lc.category_id = $1
		JOIN listing_city lci ON lci.listing_id = l.id AND lci.city_id = $2
		CROSS JOIN LATERAL (SELECT greatest(l.published_at, l.bumped_at) AS sort_key) k
		WHERE l.status = 'published'
		ORDER BY sort_key DESC NULLS LAST, l.id DESC
		OFFSET $3 LIMIT $4`, categoryID, cityID, offset, limit)
	list, err := collectAll[CatalogRow](rows)
	return list, total, err
}

// PublishedListing — карточка для клиента; черновики и снятые не показываем.
func (s *Store) PublishedListing(ctx context.Context, id int64) (Listing, error) {
	l, err := s.GetListing(ctx, id, 0)
	if err != nil {
		return Listing{}, err
	}
	if l.Status != ListingPublished {
		return Listing{}, ErrNotFound
	}
	return l, nil
}

// ListingProviderName — имя исполнителя для кнопки «Написать».
func (s *Store) ListingProviderName(ctx context.Context, listingID int64) string {
	var n string
	_ = s.pool.QueryRow(ctx, `SELECT p.display_name FROM listing l JOIN provider p ON p.id = l.provider_id WHERE l.id = $1`, listingID).Scan(&n)
	return n
}

// Избранное (FR-B-06) ------------------------------------------------------

// ToggleFavorite добавляет или убирает объявление из избранного. Возвращает новое состояние.
func (s *Store) ToggleFavorite(ctx context.Context, userID, listingID int64) (bool, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM favorite WHERE user_id = $1 AND listing_id = $2`, userID, listingID)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() > 0 {
		return false, nil
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO favorite (user_id, listing_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, userID, listingID)
	return err == nil, err
}

func (s *Store) IsFavorite(ctx context.Context, userID, listingID int64) bool {
	var ok bool
	_ = s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM favorite WHERE user_id = $1 AND listing_id = $2)`, userID, listingID).Scan(&ok)
	return ok
}

// Favorites — избранное пользователя; снятые с публикации объявления не показываем.
func (s *Store) Favorites(ctx context.Context, userID int64) ([]CatalogRow, error) {
	rows, _ := s.pool.Query(ctx, `
		SELECT l.id, l.title, l.price_type, l.price_amount::float8 AS price_amount, l.currency, l.published_at
		FROM favorite f JOIN listing l ON l.id = f.listing_id
		WHERE f.user_id = $1 AND l.status = 'published'
		ORDER BY f.created_at DESC LIMIT 50`, userID)
	return collectAll[CatalogRow](rows)
}

// Жалобы (FR-B-08) -----------------------------------------------------------

var ComplaintReasons = []string{"fraud", "outdated", "wrong_contacts", "forbidden", "other"}

var ComplaintReasonLabels = map[string]string{
	"fraud":          "Мошенничество",
	"outdated":       "Неактуально",
	"wrong_contacts": "Неверные контакты",
	"forbidden":      "Запрещённая услуга",
	"other":          "Другое",
}

var ErrDuplicateComplaint = errors.New("жалоба уже отправлена")

// CreateComplaint — жалоба клиента попадает в очередь модерации.
// Одна открытая жалоба от пользователя на объявление.
func (s *Store) CreateComplaint(ctx context.Context, userID, listingID int64, reason, comment string) error {
	if _, ok := ComplaintReasonLabels[reason]; !ok {
		return ValidationError{"неизвестная причина жалобы"}
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM complaint WHERE listing_id = $1 AND reporter_user_id = $2 AND status = 'new')`,
		listingID, userID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return ErrDuplicateComplaint
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO complaint (listing_id, reporter_user_id, reason, comment) VALUES ($1, $2, $3, $4)`,
		listingID, userID, reason, nz(strings.TrimSpace(comment)))
	return err
}

// Complaint — жалоба для модератора.
type Complaint struct {
	ID           int64      `db:"id"`
	ListingID    int64      `db:"listing_id"`
	ListingTitle *string    `db:"listing_title"`
	Reason       string     `db:"reason"`
	Comment      *string    `db:"comment"`
	Status       string     `db:"status"`
	Reporter     *string    `db:"reporter"`
	Resolution   *string    `db:"resolution"`
	CreatedAt    time.Time  `db:"created_at"`
	ResolvedAt   *time.Time `db:"resolved_at"`
}

const complaintSelect = `
	SELECT c.id, c.listing_id, l.title AS listing_title, c.reason, c.comment, c.status,
	       coalesce('@' || u.username::text, u.first_name) AS reporter, c.resolution, c.created_at, c.resolved_at
	FROM complaint c JOIN listing l ON l.id = c.listing_id LEFT JOIN users u ON u.id = c.reporter_user_id`

// ListComplaints — жалобы по статусу (пусто — все), новые сверху.
func (s *Store) ListComplaints(ctx context.Context, status string) ([]Complaint, error) {
	q := complaintSelect
	var args []any
	if status != "" {
		q += " WHERE c.status = $1"
		args = append(args, status)
	}
	rows, _ := s.pool.Query(ctx, q+" ORDER BY c.created_at DESC LIMIT 300", args...)
	return collectAll[Complaint](rows)
}

func (s *Store) ListingComplaints(ctx context.Context, listingID int64) ([]Complaint, error) {
	rows, _ := s.pool.Query(ctx, complaintSelect+" WHERE c.listing_id = $1 ORDER BY c.created_at DESC", listingID)
	return collectAll[Complaint](rows)
}

func (s *Store) CountNewComplaints(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM complaint WHERE status = 'new'`).Scan(&n)
	return n, err
}

// ResolveComplaint закрывает жалобу: resolved — меры приняты, dismissed — жалоба не подтвердилась.
func (s *Store) ResolveComplaint(ctx context.Context, actorID, complaintID int64, status, resolution string) (int64, error) {
	if status != "resolved" && status != "dismissed" {
		return 0, ValidationError{"неизвестное решение по жалобе"}
	}
	var listingID int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			UPDATE complaint SET status = $2, resolution = $3, moderator_id = $4, resolved_at = now()
			WHERE id = $1 AND status = 'new' RETURNING listing_id`,
			complaintID, status, nz(strings.TrimSpace(resolution)), actorID).Scan(&listingID)
		if err != nil {
			return notFound(err)
		}
		return audit(ctx, tx, actorID, "complaint."+status, "listing", listingID,
			map[string]any{"complaint_id": complaintID, "reason": resolution})
	})
	return listingID, err
}
