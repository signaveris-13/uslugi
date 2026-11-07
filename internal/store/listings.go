package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Статусы объявления (FR-P-05).
const (
	ListingDraft      = "draft"
	ListingModeration = "moderation"
	ListingPublished  = "published"
	ListingRejected   = "rejected"
	ListingPaused     = "paused"
	ListingArchived   = "archived"
)

var ListingStatusLabels = map[string]string{
	ListingDraft:      "Черновик",
	ListingModeration: "На модерации",
	ListingPublished:  "Опубликовано",
	ListingRejected:   "Отклонено",
	ListingPaused:     "На паузе",
	ListingArchived:   "В архиве",
}

// Языки обслуживания в порядке кнопок.
var LanguageCodes = []string{"ru", "en", "vi", "uk"}

var LanguageLabels = map[string]string{
	"ru": "Русский", "en": "English", "vi": "Tiếng Việt", "uk": "Українська",
}

const (
	MaxPhotos    = 10
	MaxExtraCats = 2
	MaxTitleLen  = 60
	MaxDescLen   = 1000
)

var ErrNotOwner = errors.New("объявление принадлежит другому исполнителю")

type Listing struct {
	ID               int64      `db:"id"`
	ProviderID       int64      `db:"provider_id"`
	Status           string     `db:"status"`
	Title            *string    `db:"title"`
	Description      *string    `db:"description"`
	PriceType        *string    `db:"price_type"`
	PriceAmount      *float64   `db:"price_amount"`
	Currency         *string    `db:"currency"`
	TravelsToClient  bool       `db:"travels_to_client"`
	ContactTelegram  *string    `db:"contact_telegram"`
	ContactWhatsApp  *string    `db:"contact_whatsapp"`
	ContactPhone     *string    `db:"contact_phone"`
	ContactInstagram *string    `db:"contact_instagram"`
	Languages        []string   `db:"languages"`
	Schedule         *string    `db:"schedule"`
	RejectionReason  *string    `db:"rejection_reason"`
	DraftStep        *string    `db:"draft_step"`
	SubmittedAt      *time.Time `db:"submitted_at"`
	PublishedAt      *time.Time `db:"published_at"`
	CreatedAt        time.Time  `db:"created_at"`

	// Заполняется GetListing.
	PrimaryCategory *Option         `db:"-"`
	ExtraCategories []Option        `db:"-"`
	Cities          []Option        `db:"-"`
	Photos          []SnapshotPhoto `db:"-"` // загруженные в бот (file_id) и в приложение (storage_key)
}

const listingCols = `id, provider_id, status, title, description, price_type, price_amount::float8 AS price_amount,
	currency, travels_to_client, contact_telegram::text AS contact_telegram, contact_whatsapp, contact_phone,
	contact_instagram, languages, schedule, rejection_reason, draft_step, submitted_at, published_at, created_at`

// ListingDraftInput — что известно заранее (из лида или из профиля Telegram).
type ListingDraftInput struct {
	CategoryID  int64
	CityID      int64
	Title       string
	Description string
	Telegram    string
	Phone       string
	WhatsApp    string
	Instagram   string
	Currency    string
}

func (s *Store) CreateListingDraft(ctx context.Context, providerID int64, in ListingDraftInput) (int64, error) {
	var id int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		title := in.Title
		if r := []rune(title); len(r) > MaxTitleLen {
			title = string(r[:MaxTitleLen])
		}
		desc := in.Description
		if r := []rune(desc); len(r) > MaxDescLen {
			desc = string(r[:MaxDescLen])
		}
		err := tx.QueryRow(ctx, `
			INSERT INTO listing (provider_id, title, description, contact_telegram, contact_phone,
			                     contact_whatsapp, contact_instagram, currency, languages)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, '{ru}') RETURNING id`,
			providerID, nz(title), nz(desc), nz(in.Telegram), nz(in.Phone), nz(in.WhatsApp), nz(in.Instagram), nz(in.Currency),
		).Scan(&id)
		if err != nil {
			return err
		}
		if in.CategoryID > 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO listing_category (listing_id, category_id, is_primary)
				SELECT $1, id, true FROM category WHERE id = $2 AND parent_id IS NOT NULL`, id, in.CategoryID); err != nil {
				return err
			}
		}
		if in.CityID > 0 {
			if _, err := tx.Exec(ctx, `INSERT INTO listing_city (listing_id, city_id) VALUES ($1, $2)`, id, in.CityID); err != nil {
				return err
			}
		}
		return nil
	})
	return id, err
}

// GetListing загружает объявление со связями и проверяет владельца (providerID 0 — без проверки).
func (s *Store) GetListing(ctx context.Context, id, providerID int64) (Listing, error) {
	rows, _ := s.pool.Query(ctx, `SELECT `+listingCols+` FROM listing WHERE id = $1`, id)
	l, err := collectOne[Listing](rows)
	if err != nil {
		return Listing{}, notFound(err)
	}
	if providerID != 0 && l.ProviderID != providerID {
		return Listing{}, ErrNotOwner
	}

	rows, _ = s.pool.Query(ctx, `
		SELECT c.id, c.name, lc.is_primary FROM listing_category lc JOIN category c ON c.id = lc.category_id
		WHERE lc.listing_id = $1 ORDER BY lc.is_primary DESC, c.sort_order`, id)
	type catRow struct {
		Option
		IsPrimary bool `db:"is_primary"`
	}
	cats, err := collectAll[catRow](rows)
	if err != nil {
		return Listing{}, err
	}
	for _, c := range cats {
		if c.IsPrimary {
			o := c.Option
			l.PrimaryCategory = &o
		} else {
			l.ExtraCategories = append(l.ExtraCategories, c.Option)
		}
	}

	rows, _ = s.pool.Query(ctx, `
		SELECT c.id, c.name FROM listing_city lc JOIN city c ON c.id = lc.city_id
		WHERE lc.listing_id = $1 ORDER BY c.sort_order`, id)
	if l.Cities, err = collectAll[Option](rows); err != nil {
		return Listing{}, err
	}

	if l.Photos, err = s.listingPhotos(ctx, nil, id); err != nil {
		return Listing{}, err
	}
	return l, nil
}

// Поля формы --------------------------------------------------------------

// SetPrimaryCategory ставит основную подкатегорию и сбрасывает дополнительные из другой категории.
func (s *Store) SetPrimaryCategory(ctx context.Context, listingID, categoryID int64) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM listing_category WHERE listing_id = $1`, listingID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO listing_category (listing_id, category_id, is_primary)
			SELECT $1, id, true FROM category WHERE id = $2 AND parent_id IS NOT NULL`, listingID, categoryID)
		return touchListing(ctx, tx, listingID, err)
	})
}

// ToggleExtraCategory добавляет или убирает дополнительную подкатегорию (не больше двух).
func (s *Store) ToggleExtraCategory(ctx context.Context, listingID, categoryID int64) (bool, error) {
	var limitHit bool
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM listing_category WHERE listing_id = $1 AND category_id = $2 AND NOT is_primary`, listingID, categoryID)
		if err != nil || tag.RowsAffected() > 0 {
			return touchListing(ctx, tx, listingID, err)
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM listing_category WHERE listing_id = $1 AND NOT is_primary`, listingID).Scan(&n); err != nil {
			return err
		}
		if n >= MaxExtraCats {
			limitHit = true
			return nil
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO listing_category (listing_id, category_id, is_primary)
			SELECT $1, id, false FROM category WHERE id = $2 AND parent_id IS NOT NULL
			ON CONFLICT DO NOTHING`, listingID, categoryID)
		return touchListing(ctx, tx, listingID, err)
	})
	return limitHit, err
}

func (s *Store) ToggleListingCity(ctx context.Context, listingID, cityID int64) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM listing_city WHERE listing_id = $1 AND city_id = $2`, listingID, cityID)
		if err != nil || tag.RowsAffected() > 0 {
			return touchListing(ctx, tx, listingID, err)
		}
		_, err = tx.Exec(ctx, `INSERT INTO listing_city (listing_id, city_id) VALUES ($1, $2)`, listingID, cityID)
		return touchListing(ctx, tx, listingID, err)
	})
}

func (s *Store) ToggleTravels(ctx context.Context, listingID int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE listing SET travels_to_client = NOT travels_to_client, updated_at = now() WHERE id = $1`, listingID)
	return err
}

// SetListingText меняет текстовое поле объявления: title, description, schedule или контакт.
func (s *Store) SetListingText(ctx context.Context, listingID int64, field, value string) error {
	col, ok := map[string]string{
		"title": "title", "description": "description", "schedule": "schedule",
		"whatsapp": "contact_whatsapp", "phone": "contact_phone", "instagram": "contact_instagram",
		"telegram": "contact_telegram",
	}[field]
	if !ok {
		return fmt.Errorf("неизвестное поле %q", field)
	}
	_, err := s.pool.Exec(ctx, `UPDATE listing SET `+col+` = $2, updated_at = now() WHERE id = $1`, listingID, nz(strings.TrimSpace(value)))
	return err
}

// SetPrice: priceType fixed | from | negotiable. Для «по договорённости» сумма и валюта сбрасываются.
func (s *Store) SetPriceType(ctx context.Context, listingID int64, priceType string) error {
	if priceType != "fixed" && priceType != "from" && priceType != "negotiable" {
		return fmt.Errorf("неизвестный тип цены %q", priceType)
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE listing SET price_type = $2, updated_at = now(),
		       price_amount = CASE WHEN $2 = 'negotiable' THEN NULL ELSE price_amount END
		WHERE id = $1`, listingID, priceType)
	return err
}

func (s *Store) SetPriceAmount(ctx context.Context, listingID int64, amount float64) error {
	_, err := s.pool.Exec(ctx, `UPDATE listing SET price_amount = $2, updated_at = now() WHERE id = $1`, listingID, amount)
	return err
}

func (s *Store) SetCurrency(ctx context.Context, listingID int64, currency string) error {
	_, err := s.pool.Exec(ctx, `UPDATE listing SET currency = $2, updated_at = now() WHERE id = $1`, listingID, currency)
	return err
}

// AddListingPhoto добавляет фото в конец. Возвращает, сколько фото стало, и false, если лимит исчерпан.
func (s *Store) AddListingPhoto(ctx context.Context, listingID int64, fileID string) (int, bool, error) {
	var n int
	var added bool
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM listing WHERE id = $1 FOR UPDATE`, listingID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM listing_photo WHERE listing_id = $1`, listingID).Scan(&n); err != nil {
			return err
		}
		if n >= MaxPhotos {
			return nil
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO listing_photo (listing_id, position, telegram_file_id)
			VALUES ($1, coalesce((SELECT max(position) + 1 FROM listing_photo WHERE listing_id = $1), 0), $2)`,
			listingID, fileID); err != nil {
			return err
		}
		n++
		added = true
		return nil
	})
	return n, added, err
}

func (s *Store) ClearListingPhotos(ctx context.Context, listingID int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM listing_photo WHERE listing_id = $1`, listingID)
	return err
}

func (s *Store) ToggleLanguage(ctx context.Context, listingID int64, code string) error {
	if _, ok := LanguageLabels[code]; !ok {
		return fmt.Errorf("неизвестный язык %q", code)
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE listing SET updated_at = now(),
		       languages = CASE WHEN $2 = ANY(languages) THEN array_remove(languages, $2) ELSE array_append(languages, $2) END
		WHERE id = $1`, listingID, code)
	return err
}

func (s *Store) SetDraftStep(ctx context.Context, listingID int64, step string) error {
	_, err := s.pool.Exec(ctx, `UPDATE listing SET draft_step = $2 WHERE id = $1 AND status = 'draft'`, listingID, nz(step))
	return err
}

func touchListing(ctx context.Context, tx pgx.Tx, id int64, err error) error {
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE listing SET updated_at = now() WHERE id = $1`, id)
	return err
}

// Проверка и отправка -------------------------------------------------------

// MissingFields — чего не хватает для отправки на модерацию, по-человечески.
func (l Listing) MissingFields() []string {
	var miss []string
	if l.PrimaryCategory == nil {
		miss = append(miss, "категория")
	}
	if len(l.Cities) == 0 {
		miss = append(miss, "город")
	}
	if l.Title == nil {
		miss = append(miss, "название")
	}
	if l.Description == nil {
		miss = append(miss, "описание")
	}
	switch {
	case l.PriceType == nil:
		miss = append(miss, "цена")
	case *l.PriceType != "negotiable" && (l.PriceAmount == nil || l.Currency == nil):
		miss = append(miss, "цена")
	}
	if l.ContactTelegram == nil && l.ContactPhone == nil && l.ContactWhatsApp == nil {
		miss = append(miss, "контакт")
	}
	if len(l.Languages) == 0 {
		miss = append(miss, "языки")
	}
	return miss
}

// ErrIncomplete — в объявлении не хватает обязательных полей.
type ErrIncomplete struct{ Missing []string }

func (e ErrIncomplete) Error() string {
	return "не заполнено: " + strings.Join(e.Missing, ", ")
}

// SubmitListing отправляет черновик или отклонённое объявление на модерацию.
// Если исполнитель пришёл из CRM, лид переходит в «Объявление создано».
func (s *Store) SubmitListing(ctx context.Context, listingID, providerID int64) error {
	l, err := s.GetListing(ctx, listingID, providerID)
	if err != nil {
		return err
	}
	if miss := l.MissingFields(); len(miss) > 0 {
		return ErrIncomplete{Missing: miss}
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE listing SET status = 'moderation', submitted_at = now(), draft_step = NULL,
			       rejection_reason = NULL, updated_at = now()
			WHERE id = $1 AND status IN ('draft', 'rejected')`, listingID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("объявление %d уже не черновик", listingID)
		}
		_, err = tx.Exec(ctx, `
			UPDATE lead SET listing_id = coalesce(listing_id, $2), updated_at = now(),
			       status = CASE WHEN status IN ('found', 'review', 'qualified', 'invited', 'replied', 'interested')
			                     THEN 'listing_created' ELSE status END
			WHERE provider_id = $1 AND (listing_id IS NULL OR listing_id = $2)`, providerID, listingID)
		return err
	})
}

func (s *Store) DeleteDraft(ctx context.Context, listingID, providerID int64) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE lead SET listing_id = NULL WHERE listing_id = $1`, listingID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM listing WHERE id = $1 AND provider_id = $2 AND status = 'draft'`, listingID, providerID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// ListingSummary — строка в «Мои объявления».
type ListingSummary struct {
	ID     int64   `db:"id"`
	Title  *string `db:"title"`
	Status string  `db:"status"`
}

func (s *Store) ListProviderListings(ctx context.Context, providerID int64) ([]ListingSummary, error) {
	rows, _ := s.pool.Query(ctx, `
		SELECT id, title, status FROM listing WHERE provider_id = $1 AND status <> 'archived'
		ORDER BY created_at DESC`, providerID)
	return collectAll[ListingSummary](rows)
}

// ProviderDraft — незаконченный черновик исполнителя, если есть.
func (s *Store) ProviderDraft(ctx context.Context, providerID int64) (int64, string, error) {
	var id int64
	var step *string
	err := s.pool.QueryRow(ctx, `
		SELECT id, draft_step FROM listing WHERE provider_id = $1 AND status = 'draft'
		ORDER BY updated_at DESC LIMIT 1`, providerID).Scan(&id, &step)
	if err != nil {
		return 0, "", notFound(err)
	}
	if step == nil {
		return id, "", nil
	}
	return id, *step, nil
}

// CountActiveListings — объявления, которые занимают лимит: на модерации, опубликованные, на паузе.
func (s *Store) CountActiveListings(ctx context.Context, providerID int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM listing WHERE provider_id = $1 AND status IN ('moderation', 'published', 'paused')`,
		providerID).Scan(&n)
	return n, err
}

// EditableStatus — объявление в этом статусе исполнитель правит через форму.
// Правка опубликованного идёт через listing_revision (FR-P-06) — это следующий шаг.
func EditableStatus(status string) bool {
	return status == ListingDraft || status == ListingRejected
}

// CountryCurrencyByCity — валюта страны, к которой относится город.
func (s *Store) CountryCurrencyByCity(ctx context.Context, cityID int64) string {
	var c string
	_ = s.pool.QueryRow(ctx, `SELECT co.currency FROM city JOIN country co ON co.id = city.country_id WHERE city.id = $1`, cityID).Scan(&c)
	return c
}

// ListActiveCitiesInCountryOf — активные города той же страны, что и город пользователя.
func (s *Store) ListActiveCitiesInCountryOf(ctx context.Context, cityID int64) ([]Option, error) {
	rows, _ := s.pool.Query(ctx, `
		SELECT c.id, c.name FROM city c
		WHERE c.is_active AND c.country_id = (SELECT country_id FROM city WHERE id = $1)
		ORDER BY c.sort_order`, cityID)
	return collectAll[Option](rows)
}
