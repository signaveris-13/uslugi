package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Каталог для Mini App: поиск, фильтры, счётчики и статистика исполнителя.

// CatalogCard — карточка в списке Mini App.
type CatalogCard struct {
	ID              int64      `db:"id"`
	Title           *string    `db:"title"`
	PriceType       *string    `db:"price_type"`
	PriceAmount     *float64   `db:"price_amount"`
	Currency        *string    `db:"currency"`
	PublishedAt     *time.Time `db:"published_at"`
	CategoryName    string     `db:"category_name"`
	ProviderName    string     `db:"provider_name"`
	TravelsToClient bool       `db:"travels_to_client"`
	CityName        string     `db:"city_name"`
	PhotoKey        *string    `db:"photo_key"`
	PhotoFileID     *string    `db:"photo_file_id"`
	PhotoCount      int        `db:"photo_count"`
}

func (c CatalogCard) Listing() Listing {
	return Listing{ID: c.ID, Title: c.Title, PriceType: c.PriceType, PriceAmount: c.PriceAmount, Currency: c.Currency}
}

// IsNew — опубликовано за последние 14 дней: бейдж «Новый» (FR-B-04).
func (c CatalogCard) IsNew() bool {
	return c.PublishedAt != nil && time.Since(*c.PublishedAt) < 14*24*time.Hour
}

func (c CatalogCard) Photo() SnapshotPhoto {
	var p SnapshotPhoto
	if c.PhotoKey != nil {
		p.StorageKey = *c.PhotoKey
	}
	if c.PhotoFileID != nil {
		p.TelegramFileID = *c.PhotoFileID
	}
	return p
}

// CatalogQuery — что ищет клиент. Пустые поля не фильтруют.
type CatalogQuery struct {
	CityID     int64
	ParentID   int64 // категория верхнего уровня
	CategoryID int64 // подкатегория
	Text       string
	Travels    bool  // только с выездом
	MaxPrice   int64 // в валюте объявления; «по договорённости» в фильтр не попадает
	Language   string
	Offset     int
	Limit      int
}

// Filtered — включён хотя бы один фильтр из шторки.
func (q CatalogQuery) Filtered() bool { return q.Travels || q.MaxPrice > 0 || q.Language != "" }

const cardSelect = `
	SELECT l.id, l.title, l.price_type, l.price_amount::float8 AS price_amount, l.currency, l.published_at,
	       coalesce(pc.name, '') AS category_name, p.display_name AS provider_name, l.travels_to_client,
	       coalesce(ci.name, '') AS city_name,
	       ph.storage_key AS photo_key, ph.telegram_file_id AS photo_file_id,
	       (SELECT count(*) FROM listing_photo WHERE listing_id = l.id)::int AS photo_count
	FROM listing l
	JOIN provider p ON p.id = l.provider_id
	LEFT JOIN LATERAL (SELECT c.name FROM listing_category lc JOIN category c ON c.id = lc.category_id
	                   WHERE lc.listing_id = l.id AND lc.is_primary) pc ON true
	LEFT JOIN LATERAL (SELECT c.name FROM listing_city lc JOIN city c ON c.id = lc.city_id
	                   WHERE lc.listing_id = l.id ORDER BY c.sort_order LIMIT 1) ci ON true
	LEFT JOIN LATERAL (SELECT storage_key, telegram_file_id FROM listing_photo
	                   WHERE listing_id = l.id ORDER BY position LIMIT 1) ph ON true`

// SearchCatalog — опубликованные объявления по запросу: сначала свежие и поднятые.
func (s *Store) SearchCatalog(ctx context.Context, q CatalogQuery) ([]CatalogCard, int, error) {
	where := []string{"l.status = 'published'"}
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if q.CityID > 0 {
		where = append(where, "EXISTS (SELECT 1 FROM listing_city lc WHERE lc.listing_id = l.id AND lc.city_id = "+arg(q.CityID)+")")
	}
	if q.CategoryID > 0 {
		where = append(where, "EXISTS (SELECT 1 FROM listing_category lc WHERE lc.listing_id = l.id AND lc.category_id = "+arg(q.CategoryID)+")")
	} else if q.ParentID > 0 {
		where = append(where, `EXISTS (SELECT 1 FROM listing_category lc JOIN category c ON c.id = lc.category_id
			WHERE lc.listing_id = l.id AND c.parent_id = `+arg(q.ParentID)+")")
	}
	if t := strings.TrimSpace(q.Text); t != "" {
		pat := arg("%" + escapeLike(t) + "%")
		where = append(where, `(l.title ILIKE `+pat+` OR l.description ILIKE `+pat+` OR EXISTS (
			SELECT 1 FROM listing_category lc JOIN category c ON c.id = lc.category_id
			LEFT JOIN category par ON par.id = c.parent_id
			WHERE lc.listing_id = l.id AND (c.name ILIKE `+pat+` OR par.name ILIKE `+pat+`)))`)
	}
	if q.Travels {
		where = append(where, "l.travels_to_client")
	}
	if q.MaxPrice > 0 {
		where = append(where, "l.price_type IN ('fixed', 'from') AND l.price_amount <= "+arg(q.MaxPrice))
	}
	if q.Language != "" {
		where = append(where, arg(q.Language)+" = ANY (l.languages)")
	}
	cond := " WHERE " + strings.Join(where, " AND ")

	var total int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM listing l"+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := q.Limit
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	sql := cardSelect + cond + `
		ORDER BY greatest(l.published_at, l.bumped_at) DESC NULLS LAST, l.id DESC
		OFFSET ` + arg(q.Offset) + ` LIMIT ` + arg(limit)
	rows, _ := s.pool.Query(ctx, sql, args...)
	list, err := collectAll[CatalogCard](rows)
	return list, total, err
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// FavoriteCards — избранное пользователя карточками, вместе со снятыми (их можно убрать).
type FavoriteCard struct {
	CatalogCard
	Status string `db:"status"`
}

func (s *Store) FavoriteCards(ctx context.Context, userID int64) ([]FavoriteCard, error) {
	rows, _ := s.pool.Query(ctx, `
		SELECT c.*, l.status FROM (`+cardSelect+`
			JOIN favorite f ON f.listing_id = l.id AND f.user_id = $1
			WHERE l.status IN ('published', 'paused', 'archived')
		) c JOIN listing l ON l.id = c.id
		JOIN favorite f ON f.listing_id = l.id AND f.user_id = $1
		ORDER BY (l.status = 'published') DESC, f.created_at DESC LIMIT 50`, userID)
	return collectAll[FavoriteCard](rows)
}

// SearchSubcategories — подкатегории, чьё название подходит под запрос, со счётчиком в городе.
func (s *Store) SearchSubcategories(ctx context.Context, cityID int64, text string) ([]CatalogItem, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	rows, _ := s.pool.Query(ctx, `
		SELECT sub.id, sub.name, coalesce(p.emoji, '') AS emoji, count(DISTINCT l.id)::int AS count
		FROM category sub
		JOIN category p ON p.id = sub.parent_id AND p.is_active
		JOIN listing_category lc ON lc.category_id = sub.id
		JOIN listing l ON l.id = lc.listing_id AND l.status = 'published'
		JOIN listing_city lci ON lci.listing_id = l.id AND lci.city_id = $1
		WHERE sub.is_active AND (sub.name ILIKE $2 OR p.name ILIKE $2)
		GROUP BY sub.id, p.emoji
		ORDER BY count(DISTINCT l.id) DESC, sub.sort_order LIMIT 5`, cityID, "%"+escapeLike(text)+"%")
	return collectAll[CatalogItem](rows)
}

// CountCityListings — сколько объявлений опубликовано в городе.
func (s *Store) CountCityListings(ctx context.Context, cityID int64) int {
	var n int
	_ = s.pool.QueryRow(ctx, `
		SELECT count(*) FROM listing l
		WHERE l.status = 'published' AND EXISTS (SELECT 1 FROM listing_city lc WHERE lc.listing_id = l.id AND lc.city_id = $1)`,
		cityID).Scan(&n)
	return n
}

// CityCount — город и число объявлений в нём, для экрана выбора города.
type CityCount struct {
	ID      int64  `db:"id"`
	Name    string `db:"name"`
	Country string `db:"country"`
	Count   int    `db:"count"`
}

func (s *Store) CityCounts(ctx context.Context) ([]CityCount, error) {
	rows, _ := s.pool.Query(ctx, `
		SELECT c.id, c.name, co.name AS country,
		       (SELECT count(*) FROM listing_city lc JOIN listing l ON l.id = lc.listing_id AND l.status = 'published'
		        WHERE lc.city_id = c.id)::int AS count
		FROM city c JOIN country co ON co.id = c.country_id
		WHERE c.is_active AND co.is_active
		ORDER BY co.name, c.sort_order`)
	return collectAll[CityCount](rows)
}

// Stats — просмотры, открытия контактов и добавления в избранное за период.
type Stats struct {
	Views     int
	Contacts  int
	Favorites int
}

// ListingStats — статистика объявлений исполнителя; listingID = 0 — по всем.
func (s *Store) ListingStats(ctx context.Context, providerID, listingID int64, days int) (Stats, error) {
	var st Stats
	err := s.pool.QueryRow(ctx, `
		WITH mine AS (SELECT id FROM listing WHERE provider_id = $1 AND ($2 = 0 OR id = $2))
		SELECT
		  (SELECT count(*) FROM event WHERE type = 'listing_viewed' AND object_type = 'listing'
		     AND object_id IN (SELECT id FROM mine) AND created_at > now() - make_interval(days => $3::int))::int,
		  (SELECT count(*) FROM event WHERE type = 'contact_clicked' AND object_type = 'listing'
		     AND object_id IN (SELECT id FROM mine) AND created_at > now() - make_interval(days => $3::int))::int,
		  (SELECT count(*) FROM favorite WHERE listing_id IN (SELECT id FROM mine)
		     AND created_at > now() - make_interval(days => $3::int))::int`,
		providerID, listingID, days).Scan(&st.Views, &st.Contacts, &st.Favorites)
	return st, err
}

// Управление своим объявлением ---------------------------------------------

// PauseListing снимает опубликованное объявление с показа; оно не занимает место в каталоге.
func (s *Store) PauseListing(ctx context.Context, listingID, providerID int64) error {
	return s.setOwnStatus(ctx, listingID, providerID, ListingPublished, ListingPaused)
}

// ResumeListing возвращает объявление с паузы: оно уже прошло модерацию.
func (s *Store) ResumeListing(ctx context.Context, listingID, providerID int64) error {
	return s.setOwnStatus(ctx, listingID, providerID, ListingPaused, ListingPublished)
}

// ArchiveListing — «Удалить»: объявление уходит в архив, в каталоге и у исполнителя не видно.
func (s *Store) ArchiveListing(ctx context.Context, listingID, providerID int64) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE listing SET status = 'archived', updated_at = now()
		WHERE id = $1 AND provider_id = $2 AND status <> 'archived'`, listingID, providerID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ActualizeListing — «Да, актуально» (FR-P-07).
func (s *Store) ActualizeListing(ctx context.Context, listingID, providerID int64) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE listing SET actualized_at = now(), updated_at = now()
		WHERE id = $1 AND provider_id = $2 AND status IN ('published', 'paused')`, listingID, providerID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) setOwnStatus(ctx context.Context, listingID, providerID int64, from, to string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE listing SET status = $4, updated_at = now()
		WHERE id = $1 AND provider_id = $2 AND status = $3`, listingID, providerID, from, to)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
