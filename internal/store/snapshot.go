package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// Snapshot — все редактируемые поля объявления разом: так их правит форма Mini App
// и так хранится правка опубликованного объявления в listing_revision (FR-P-06).
type Snapshot struct {
	Title             string          `json:"title"`
	Description       string          `json:"description"`
	PriceType         string          `json:"price_type"`
	PriceAmount       *float64        `json:"price_amount"`
	Currency          string          `json:"currency"`
	TravelsToClient   bool            `json:"travels_to_client"`
	Telegram          string          `json:"telegram"`
	WhatsApp          string          `json:"whatsapp"`
	Phone             string          `json:"phone"`
	Instagram         string          `json:"instagram"`
	Languages         []string        `json:"languages"`
	Schedule          string          `json:"schedule"`
	PrimaryCategoryID int64           `json:"primary_category_id"`
	ExtraCategoryIDs  []int64         `json:"extra_category_ids"`
	CityIDs           []int64         `json:"city_ids"`
	Photos            []SnapshotPhoto `json:"photos"`
}

// SnapshotPhoto — фото из Telegram (file_id) или загруженное в приложении (storage_key).
type SnapshotPhoto struct {
	StorageKey     string `json:"storage_key,omitempty"`
	TelegramFileID string `json:"telegram_file_id,omitempty"`
}

// Режим сохранения правки.
const (
	EditDirect   = "direct"   // черновик, отклонённое, на модерации — меняем само объявление
	EditRevision = "revision" // опубликованное или на паузе — правка ждёт модерации
)

func EditMode(status string) (string, bool) {
	switch status {
	case ListingDraft, ListingRejected, ListingModeration:
		return EditDirect, true
	case ListingPublished, ListingPaused:
		return EditRevision, true
	}
	return "", false
}

func snapshotOf(l Listing, photos []SnapshotPhoto) Snapshot {
	s := Snapshot{
		Title: deref(l.Title), Description: deref(l.Description), PriceType: deref(l.PriceType),
		PriceAmount: l.PriceAmount, Currency: deref(l.Currency), TravelsToClient: l.TravelsToClient,
		Telegram: deref(l.ContactTelegram), WhatsApp: deref(l.ContactWhatsApp), Phone: deref(l.ContactPhone),
		Instagram: deref(l.ContactInstagram), Languages: l.Languages, Schedule: deref(l.Schedule),
		Photos: photos,
	}
	if l.PrimaryCategory != nil {
		s.PrimaryCategoryID = l.PrimaryCategory.ID
	}
	for _, c := range l.ExtraCategories {
		s.ExtraCategoryIDs = append(s.ExtraCategoryIDs, c.ID)
	}
	for _, c := range l.Cities {
		s.CityIDs = append(s.CityIDs, c.ID)
	}
	if s.Languages == nil {
		s.Languages = []string{}
	}
	return s
}

func (s *Store) listingPhotos(ctx context.Context, q pgx.Tx, listingID int64) ([]SnapshotPhoto, error) {
	var rows pgx.Rows
	sql := `SELECT coalesce(storage_key, '') AS storage_key, coalesce(telegram_file_id, '') AS telegram_file_id
		FROM listing_photo WHERE listing_id = $1 ORDER BY position`
	if q != nil {
		rows, _ = q.Query(ctx, sql, listingID)
	} else {
		rows, _ = s.pool.Query(ctx, sql, listingID)
	}
	type r struct {
		StorageKey     string `db:"storage_key"`
		TelegramFileID string `db:"telegram_file_id"`
	}
	list, err := collectAll[r](rows)
	out := make([]SnapshotPhoto, 0, len(list))
	for _, p := range list {
		out = append(out, SnapshotPhoto{StorageKey: p.StorageKey, TelegramFileID: p.TelegramFileID})
	}
	return out, err
}

// Revision — правка опубликованного объявления, ждущая модерации.
type Revision struct {
	ID        int64
	ListingID int64
	Data      Snapshot
	Status    string
	Reason    *string
	CreatedAt time.Time
}

func (s *Store) PendingRevision(ctx context.Context, listingID int64) (Revision, error) {
	var r Revision
	var raw []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, listing_id, data, status, rejection_reason, created_at FROM listing_revision
		WHERE listing_id = $1 AND status = 'pending'`, listingID).Scan(&r.ID, &r.ListingID, &raw, &r.Status, &r.Reason, &r.CreatedAt)
	if err != nil {
		return Revision{}, notFound(err)
	}
	if err := json.Unmarshal(raw, &r.Data); err != nil {
		return Revision{}, err
	}
	return r, nil
}

// EditorState — что показывает форма: объявление и версия полей для правки.
type EditorState struct {
	Listing  Listing
	Snapshot Snapshot
	Mode     string
	// Правка опубликованного уже отправлена и ждёт модерации.
	RevisionPending bool
}

// LoadEditor — объявление исполнителя для формы. Для опубликованного берём ожидающую правку, если есть.
func (s *Store) LoadEditor(ctx context.Context, listingID, providerID int64) (EditorState, error) {
	l, err := s.GetListing(ctx, listingID, providerID)
	if err != nil {
		return EditorState{}, err
	}
	mode, ok := EditMode(l.Status)
	if !ok {
		return EditorState{}, ErrNotEditable
	}
	st := EditorState{Listing: l, Mode: mode}
	if mode == EditRevision {
		if r, err := s.PendingRevision(ctx, l.ID); err == nil {
			st.Snapshot = r.Data
			st.RevisionPending = true
			return st, nil
		} else if !errors.Is(err, ErrNotFound) {
			return EditorState{}, err
		}
	}
	photos, err := s.listingPhotos(ctx, nil, l.ID)
	if err != nil {
		return EditorState{}, err
	}
	st.Snapshot = snapshotOf(l, photos)
	return st, nil
}

var ErrNotEditable = errors.New("объявление в этом статусе нельзя править")

// Normalize и Check — проверки формы. Check возвращает ValidationError с понятным текстом.
func (sn *Snapshot) Normalize() {
	trim := strings.TrimSpace
	sn.Title, sn.Description, sn.Schedule = trim(sn.Title), trim(sn.Description), trim(sn.Schedule)
	sn.Telegram = strings.TrimPrefix(trim(sn.Telegram), "@")
	sn.WhatsApp, sn.Phone, sn.Instagram = trim(sn.WhatsApp), trim(sn.Phone), trim(sn.Instagram)
	if sn.PriceType == "negotiable" {
		sn.PriceAmount = nil
	}
	sn.ExtraCategoryIDs = uniqIDs(sn.ExtraCategoryIDs, sn.PrimaryCategoryID)
	sn.CityIDs = uniqIDs(sn.CityIDs, 0)
	var langs []string
	seen := map[string]bool{}
	for _, l := range sn.Languages {
		if _, ok := LanguageLabels[l]; ok && !seen[l] {
			langs = append(langs, l)
			seen[l] = true
		}
	}
	if langs == nil {
		langs = []string{}
	}
	sn.Languages = langs
}

func uniqIDs(ids []int64, exclude int64) []int64 {
	out := []int64{}
	seen := map[int64]bool{}
	for _, id := range ids {
		if id > 0 && id != exclude && !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	return out
}

// CheckLimits — ограничения, которые нельзя нарушить даже в черновике.
func (sn Snapshot) CheckLimits() error {
	switch {
	case utf8.RuneCountInString(sn.Title) > MaxTitleLen:
		return ValidationError{fmt.Sprintf("Название длиннее %d символов.", MaxTitleLen)}
	case utf8.RuneCountInString(sn.Description) > MaxDescLen:
		return ValidationError{fmt.Sprintf("Описание длиннее %d символов.", MaxDescLen)}
	case utf8.RuneCountInString(sn.Schedule) > 200:
		return ValidationError{"График длиннее 200 символов."}
	case len(sn.ExtraCategoryIDs) > MaxExtraCats:
		return ValidationError{fmt.Sprintf("Дополнительных услуг — не больше %d.", MaxExtraCats)}
	case len(sn.Photos) > MaxPhotos:
		return ValidationError{fmt.Sprintf("Фото — не больше %d.", MaxPhotos)}
	case sn.PriceType != "" && sn.PriceType != "fixed" && sn.PriceType != "from" && sn.PriceType != "negotiable":
		return ValidationError{"Неизвестный тип цены."}
	case sn.Currency != "" && sn.Currency != "VND" && sn.Currency != "USD":
		return ValidationError{"Валюта — донги или доллары."}
	case sn.PriceAmount != nil && (*sn.PriceAmount <= 0 || *sn.PriceAmount >= 1e12):
		return ValidationError{"Проверь сумму."}
	}
	for _, v := range []string{sn.Telegram, sn.WhatsApp, sn.Phone, sn.Instagram} {
		if utf8.RuneCountInString(v) > 100 {
			return ValidationError{"Слишком длинный контакт."}
		}
	}
	return nil
}

// Missing — что не заполнено для отправки на модерацию (как Listing.MissingFields).
func (sn Snapshot) Missing() []string {
	var miss []string
	if sn.PrimaryCategoryID == 0 {
		miss = append(miss, "категория")
	}
	if len(sn.CityIDs) == 0 {
		miss = append(miss, "город")
	}
	if sn.Title == "" {
		miss = append(miss, "название")
	}
	if sn.Description == "" {
		miss = append(miss, "описание")
	}
	if sn.PriceType == "" || (sn.PriceType != "negotiable" && (sn.PriceAmount == nil || sn.Currency == "")) {
		miss = append(miss, "цена")
	}
	if sn.Telegram == "" && sn.Phone == "" && sn.WhatsApp == "" {
		miss = append(miss, "контакт")
	}
	if len(sn.Languages) == 0 {
		miss = append(miss, "языки")
	}
	return miss
}

// checkRefs — категории существуют и это подкатегории, города активны.
func checkRefs(ctx context.Context, tx pgx.Tx, sn Snapshot) error {
	cats := append([]int64{}, sn.ExtraCategoryIDs...)
	if sn.PrimaryCategoryID != 0 {
		cats = append(cats, sn.PrimaryCategoryID)
	}
	if len(cats) > 0 {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM category WHERE id = ANY($1) AND parent_id IS NOT NULL AND is_active`, cats).Scan(&n); err != nil {
			return err
		}
		if n != len(cats) {
			return ValidationError{"Выбери подкатегории из списка."}
		}
	}
	if len(sn.CityIDs) > 0 {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM city WHERE id = ANY($1) AND is_active`, sn.CityIDs).Scan(&n); err != nil {
			return err
		}
		if n != len(sn.CityIDs) {
			return ValidationError{"Выбери города из списка."}
		}
	}
	return nil
}

// applySnapshot переписывает объявление из снимка: поля, категории, города, фото.
func applySnapshot(ctx context.Context, tx pgx.Tx, listingID int64, sn Snapshot) error {
	if _, err := tx.Exec(ctx, `
		UPDATE listing SET title = $2, description = $3, price_type = $4, price_amount = $5, currency = $6,
		       travels_to_client = $7, contact_telegram = $8, contact_whatsapp = $9, contact_phone = $10,
		       contact_instagram = $11, languages = $12, schedule = $13, updated_at = now()
		WHERE id = $1`,
		listingID, nz(sn.Title), nz(sn.Description), nz(sn.PriceType), sn.PriceAmount, nz(sn.Currency),
		sn.TravelsToClient, nz(sn.Telegram), nz(sn.WhatsApp), nz(sn.Phone), nz(sn.Instagram), sn.Languages, nz(sn.Schedule),
	); err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM listing_category WHERE listing_id = $1`,
		`DELETE FROM listing_city WHERE listing_id = $1`,
		`DELETE FROM listing_photo WHERE listing_id = $1`,
	} {
		if _, err := tx.Exec(ctx, q, listingID); err != nil {
			return err
		}
	}
	if sn.PrimaryCategoryID != 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO listing_category (listing_id, category_id, is_primary) VALUES ($1, $2, true)`, listingID, sn.PrimaryCategoryID); err != nil {
			return err
		}
	}
	for _, c := range sn.ExtraCategoryIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO listing_category (listing_id, category_id, is_primary) VALUES ($1, $2, false) ON CONFLICT DO NOTHING`, listingID, c); err != nil {
			return err
		}
	}
	for _, c := range sn.CityIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO listing_city (listing_id, city_id) VALUES ($1, $2)`, listingID, c); err != nil {
			return err
		}
	}
	for i, p := range sn.Photos {
		if _, err := tx.Exec(ctx, `
			INSERT INTO listing_photo (listing_id, position, storage_key, telegram_file_id) VALUES ($1, $2, $3, $4)`,
			listingID, i, nz(p.StorageKey), nz(p.TelegramFileID)); err != nil {
			return err
		}
	}
	return nil
}

// SaveEditor сохраняет форму: в само объявление или в правку на модерацию.
func (s *Store) SaveEditor(ctx context.Context, listingID, providerID int64, sn Snapshot) (EditorState, error) {
	sn.Normalize()
	if err := sn.CheckLimits(); err != nil {
		return EditorState{}, err
	}
	l, err := s.GetListing(ctx, listingID, providerID)
	if err != nil {
		return EditorState{}, err
	}
	mode, ok := EditMode(l.Status)
	if !ok {
		return EditorState{}, ErrNotEditable
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := checkRefs(ctx, tx, sn); err != nil {
			return err
		}
		if mode == EditDirect {
			return applySnapshot(ctx, tx, listingID, sn)
		}
		raw, err := json.Marshal(sn)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO listing_revision (listing_id, data) VALUES ($1, $2)
			ON CONFLICT (listing_id) WHERE status = 'pending' DO UPDATE SET data = EXCLUDED.data, created_at = now()`,
			listingID, raw)
		return err
	})
	if err != nil {
		return EditorState{}, err
	}
	return s.LoadEditor(ctx, listingID, providerID)
}

// SubmitEditor — «Отправить на модерацию» из приложения.
// Черновик и отклонённое уходят в очередь; правка опубликованного уже ждёт модерации после сохранения.
func (s *Store) SubmitEditor(ctx context.Context, listingID, providerID int64) error {
	st, err := s.LoadEditor(ctx, listingID, providerID)
	if err != nil {
		return err
	}
	if miss := st.Snapshot.Missing(); len(miss) > 0 {
		return ErrIncomplete{Missing: miss}
	}
	if st.Mode == EditRevision {
		if !st.RevisionPending {
			return ValidationError{"Изменений нет — объявление и так опубликовано."}
		}
		return nil
	}
	if st.Listing.Status == ListingModeration {
		return nil
	}
	return s.SubmitListing(ctx, listingID, providerID)
}

// AddEditorPhoto добавляет загруженное фото в конец (в объявление или в правку).
func (s *Store) AddEditorPhoto(ctx context.Context, listingID, providerID int64, storageKey string) (EditorState, error) {
	st, err := s.LoadEditor(ctx, listingID, providerID)
	if err != nil {
		return EditorState{}, err
	}
	if len(st.Snapshot.Photos) >= MaxPhotos {
		return EditorState{}, ValidationError{fmt.Sprintf("Фото — не больше %d.", MaxPhotos)}
	}
	st.Snapshot.Photos = append(st.Snapshot.Photos, SnapshotPhoto{StorageKey: storageKey})
	return s.SaveEditor(ctx, listingID, providerID, st.Snapshot)
}

// RemoveEditorPhoto убирает фото по номеру в снимке.
func (s *Store) RemoveEditorPhoto(ctx context.Context, listingID, providerID int64, index int) (EditorState, error) {
	st, err := s.LoadEditor(ctx, listingID, providerID)
	if err != nil {
		return EditorState{}, err
	}
	if index < 0 || index >= len(st.Snapshot.Photos) {
		return st, nil
	}
	st.Snapshot.Photos = append(st.Snapshot.Photos[:index], st.Snapshot.Photos[index+1:]...)
	return s.SaveEditor(ctx, listingID, providerID, st.Snapshot)
}

// Модерация правок --------------------------------------------------------------

func (s *Store) CountPendingRevisions(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM listing_revision r JOIN listing l ON l.id = r.listing_id
		WHERE r.status = 'pending' AND l.status IN ('published', 'paused')`).Scan(&n)
	return n, err
}

// ListRevisionListings — объявления с правкой на модерации, старые сверху.
func (s *Store) ListRevisionListings(ctx context.Context) ([]ListingRow, error) {
	rows, _ := s.pool.Query(ctx, `
		SELECT l.id, l.title, l.status,
		       (SELECT c.name FROM listing_category lc JOIN category c ON c.id = lc.category_id
		         WHERE lc.listing_id = l.id AND lc.is_primary) AS category_name,
		       (SELECT string_agg(ci.name, ', ' ORDER BY ci.sort_order) FROM listing_city lci JOIN city ci ON ci.id = lci.city_id
		         WHERE lci.listing_id = l.id) AS cities,
		       p.display_name AS provider_name, u.username::text AS username,
		       r.created_at AS submitted_at, l.updated_at
		FROM listing_revision r
		JOIN listing l ON l.id = r.listing_id
		JOIN provider p ON p.id = l.provider_id
		JOIN users u ON u.id = p.user_id
		WHERE r.status = 'pending' AND l.status IN ('published', 'paused')
		ORDER BY r.created_at`)
	return collectAll[ListingRow](rows)
}

// CurrentSnapshot — как объявление выглядит сейчас (для сравнения с правкой).
func (s *Store) CurrentSnapshot(ctx context.Context, l Listing) (Snapshot, error) {
	photos, err := s.listingPhotos(ctx, nil, l.ID)
	if err != nil {
		return Snapshot{}, err
	}
	return snapshotOf(l, photos), nil
}

// ApproveRevision применяет правку к опубликованному объявлению.
func (s *Store) ApproveRevision(ctx context.Context, actorID, listingID int64) (ModerationResult, error) {
	var res ModerationResult
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var revID int64
		var raw []byte
		err := tx.QueryRow(ctx, `
			SELECT r.id, r.data FROM listing_revision r JOIN listing l ON l.id = r.listing_id
			WHERE r.listing_id = $1 AND r.status = 'pending' AND l.status IN ('published', 'paused')
			FOR UPDATE OF r`, listingID).Scan(&revID, &raw)
		if err != nil {
			return notFound(err)
		}
		var sn Snapshot
		if err := json.Unmarshal(raw, &sn); err != nil {
			return err
		}
		if err := checkRefs(ctx, tx, sn); err != nil {
			return err
		}
		if err := applySnapshot(ctx, tx, listingID, sn); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE listing SET version = version + 1 WHERE id = $1`, listingID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE listing_revision SET status = 'approved', reviewed_by = $2, reviewed_at = now() WHERE id = $1`, revID, actorID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			SELECT u.telegram_id FROM listing l JOIN provider p ON p.id = l.provider_id JOIN users u ON u.id = p.user_id
			WHERE l.id = $1`, listingID).Scan(&res.TelegramID); err != nil {
			return err
		}
		res.Title = sn.Title
		return audit(ctx, tx, actorID, "revision.approve", "listing", listingID, nil)
	})
	return res, err
}

// RejectRevision отклоняет правку; опубликованная версия остаётся как была.
func (s *Store) RejectRevision(ctx context.Context, actorID, listingID int64, reason string) (ModerationResult, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ModerationResult{}, ValidationError{"Укажи причину отказа: исполнитель увидит её в боте."}
	}
	var res ModerationResult
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var raw []byte
		err := tx.QueryRow(ctx, `
			UPDATE listing_revision SET status = 'rejected', rejection_reason = $2, reviewed_by = $3, reviewed_at = now()
			WHERE listing_id = $1 AND status = 'pending' RETURNING data`, listingID, reason, actorID).Scan(&raw)
		if err != nil {
			return notFound(err)
		}
		var sn Snapshot
		_ = json.Unmarshal(raw, &sn)
		res.Title = sn.Title
		if err := tx.QueryRow(ctx, `
			SELECT u.telegram_id FROM listing l JOIN provider p ON p.id = l.provider_id JOIN users u ON u.id = p.user_id
			WHERE l.id = $1`, listingID).Scan(&res.TelegramID); err != nil {
			return err
		}
		return audit(ctx, tx, actorID, "revision.reject", "listing", listingID, map[string]any{"reason": reason})
	})
	return res, err
}

// Фото ---------------------------------------------------------------------

// ListingPhoto — фото объявления для показа и кеширования file_id.
type ListingPhoto struct {
	ID             int64   `db:"id"`
	ListingID      int64   `db:"listing_id"`
	StorageKey     *string `db:"storage_key"`
	TelegramFileID *string `db:"telegram_file_id"`
}

func (s *Store) ListingPhotoRows(ctx context.Context, listingID int64) ([]ListingPhoto, error) {
	rows, _ := s.pool.Query(ctx, `
		SELECT id, listing_id, storage_key, telegram_file_id FROM listing_photo WHERE listing_id = $1 ORDER BY position`, listingID)
	return collectAll[ListingPhoto](rows)
}

// CachePhotoFileID запоминает file_id фото, загруженного в приложении, после первой отправки ботом.
func (s *Store) CachePhotoFileID(ctx context.Context, storageKey, fileID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE listing_photo SET telegram_file_id = $2 WHERE storage_key = $1 AND telegram_file_id IS NULL`, storageKey, fileID)
	return err
}

// ProviderOwnsStorageKey — фото принадлежит объявлению исполнителя (для проверки доступа).
func (s *Store) PhotoVisibleTo(ctx context.Context, storageKey string, providerID int64) bool {
	var ok bool
	_ = s.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM listing_photo ph JOIN listing l ON l.id = ph.listing_id WHERE ph.storage_key = $1 AND l.provider_id = $2)
		    OR EXISTS (SELECT 1 FROM listing_revision r JOIN listing l ON l.id = r.listing_id
		               WHERE l.provider_id = $2 AND r.status = 'pending' AND r.data::text LIKE '%' || $1 || '%')`,
		storageKey, providerID).Scan(&ok)
	return ok
}

// ProviderCard — карточка в «Мои объявления» Mini App.
type ProviderCard struct {
	ID              int64    `db:"id"`
	Title           *string  `db:"title"`
	Status          string   `db:"status"`
	PriceType       *string  `db:"price_type"`
	PriceAmount     *float64 `db:"price_amount"`
	Currency        *string  `db:"currency"`
	PhotoKey        *string  `db:"photo_key"`
	PhotoFileID     *string  `db:"photo_file_id"`
	RevisionPending bool     `db:"revision_pending"`
	RejectionReason *string  `db:"rejection_reason"`
}

func (c ProviderCard) Listing() Listing {
	return Listing{ID: c.ID, Title: c.Title, Status: c.Status, PriceType: c.PriceType, PriceAmount: c.PriceAmount, Currency: c.Currency}
}

func (s *Store) ProviderCards(ctx context.Context, providerID int64) ([]ProviderCard, error) {
	rows, _ := s.pool.Query(ctx, `
		SELECT l.id, l.title, l.status, l.price_type, l.price_amount::float8 AS price_amount, l.currency,
		       ph.storage_key AS photo_key, ph.telegram_file_id AS photo_file_id,
		       EXISTS (SELECT 1 FROM listing_revision r WHERE r.listing_id = l.id AND r.status = 'pending') AS revision_pending,
		       l.rejection_reason
		FROM listing l
		LEFT JOIN LATERAL (SELECT storage_key, telegram_file_id FROM listing_photo WHERE listing_id = l.id ORDER BY position LIMIT 1) ph ON true
		WHERE l.provider_id = $1 AND l.status <> 'archived'
		ORDER BY l.created_at DESC`, providerID)
	return collectAll[ProviderCard](rows)
}
