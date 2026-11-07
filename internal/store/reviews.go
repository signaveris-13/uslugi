package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Отзывы (FR-B-07). Клиент пишет отзыв, если открывал контакты мастера через Uslugi.
// Отзыв и ответ мастера видны всем только после модерации.

const (
	ReviewModeration = "moderation"
	ReviewPublished  = "published"
	ReviewRejected   = "rejected"
)

var ReviewTags = []string{"Качество", "Пунктуальность", "Цена", "Общение", "Чистота", "Выезд вовремя"}

var ReviewRejectReasons = []string{
	"Оскорбления или грубость",
	"Реклама, ссылки или контакты",
	"Отзыв не об услуге",
	"Похоже на накрутку",
	"Личные данные других людей",
}

var (
	ErrReviewNotAllowed = ValidationError{"Отзыв можно оставить, если вы писали мастеру через Uslugi."}
	ErrReviewExists     = ValidationError{"Вы уже оставили отзыв об этом мастере."}
)

type Review struct {
	ID              int64      `db:"id"`
	ListingID       int64      `db:"listing_id"`
	ListingTitle    *string    `db:"listing_title"`
	AuthorUserID    int64      `db:"author_user_id"`
	AuthorName      *string    `db:"author_name"`
	AuthorUsername  *string    `db:"author_username"`
	Rating          int        `db:"rating"`
	Tags            []string   `db:"tags"`
	Text            *string    `db:"text"`
	ShowName        bool       `db:"show_name"`
	Status          string     `db:"status"`
	RejectionReason *string    `db:"rejection_reason"`
	ReplyText       *string    `db:"reply_text"`
	ReplyStatus     *string    `db:"reply_status"`
	ReplyRejection  *string    `db:"reply_rejection"`
	DisputedAt      *time.Time `db:"disputed_at"`
	DisputeReason   *string    `db:"dispute_reason"`
	ProviderName    string     `db:"provider_name"`
	ProviderID      int64      `db:"provider_id"`
	CreatedAt       time.Time  `db:"created_at"`
}

// PublicName — подпись отзыва: имя, если автор разрешил, иначе «Клиент Uslugi».
func (r Review) PublicName() string {
	if r.ShowName && r.AuthorName != nil && strings.TrimSpace(*r.AuthorName) != "" {
		return *r.AuthorName
	}
	return "Клиент Uslugi"
}

// ReplyVisible — ответ мастера опубликован.
func (r Review) ReplyVisible() bool {
	return r.ReplyText != nil && r.ReplyStatus != nil && *r.ReplyStatus == ReviewPublished
}

const reviewSelect = `
	SELECT r.id, r.listing_id, l.title AS listing_title, r.author_user_id, u.first_name AS author_name,
	       u.username::text AS author_username, r.rating, r.tags, r.text, r.show_name, r.status,
	       r.rejection_reason, r.reply_text, r.reply_status, r.reply_rejection, r.disputed_at, r.dispute_reason,
	       p.display_name AS provider_name, p.id AS provider_id, r.created_at
	FROM review r
	JOIN listing l ON l.id = r.listing_id
	JOIN provider p ON p.id = l.provider_id
	JOIN users u ON u.id = r.author_user_id`

type ReviewInput struct {
	Rating   int
	Tags     []string
	Text     string
	ShowName bool
}

// CanReview — пользователь писал мастеру через Uslugi и это не его объявление.
func (s *Store) CanReview(ctx context.Context, userID, listingID int64) bool {
	var ok bool
	_ = s.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM event WHERE type = 'contact_clicked' AND object_type = 'listing'
		                 AND object_id = $2 AND user_id = $1)
		   AND NOT EXISTS (SELECT 1 FROM listing l JOIN provider p ON p.id = l.provider_id
		                   WHERE l.id = $2 AND p.user_id = $1)`, userID, listingID).Scan(&ok)
	return ok
}

// MyReview — отзыв пользователя об объявлении, если есть.
func (s *Store) MyReview(ctx context.Context, userID, listingID int64) (Review, error) {
	rows, _ := s.pool.Query(ctx, reviewSelect+" WHERE r.author_user_id = $1 AND r.listing_id = $2", userID, listingID)
	r, err := collectOne[Review](rows)
	return r, notFound(err)
}

// CreateReview — новый отзыв на модерацию. Отклонённый можно переписать: он вернётся в очередь.
func (s *Store) CreateReview(ctx context.Context, userID, listingID int64, in ReviewInput) (int64, error) {
	if in.Rating < 1 || in.Rating > 5 {
		return 0, ValidationError{"Поставьте оценку от 1 до 5."}
	}
	in.Text = strings.TrimSpace(in.Text)
	if r := []rune(in.Text); len(r) > 1000 {
		return 0, ValidationError{"Отзыв — до 1000 символов."}
	}
	var tags []string
	for _, t := range in.Tags {
		for _, ok := range ReviewTags {
			if t == ok {
				tags = append(tags, t)
			}
		}
	}
	if tags == nil {
		tags = []string{}
	}
	if !s.CanReview(ctx, userID, listingID) {
		return 0, ErrReviewNotAllowed
	}
	if old, err := s.MyReview(ctx, userID, listingID); err == nil && old.Status != ReviewRejected {
		return 0, ErrReviewExists
	}
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO review (listing_id, author_user_id, rating, tags, text, show_name)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (listing_id, author_user_id) DO UPDATE
		   SET rating = EXCLUDED.rating, tags = EXCLUDED.tags, text = EXCLUDED.text, show_name = EXCLUDED.show_name,
		       status = 'moderation', rejection_reason = NULL, created_at = now(), updated_at = now()
		RETURNING id`, listingID, userID, in.Rating, tags, nz(in.Text), in.ShowName).Scan(&id)
	return id, err
}

// ReviewSummary — средняя оценка и число опубликованных отзывов.
type ReviewSummary struct {
	Count   int
	Average float64
	ByStars [6]int // 1…5
}

func (s *Store) ListingReviewSummary(ctx context.Context, listingID int64) (ReviewSummary, error) {
	return s.reviewSummary(ctx, "r.listing_id = $1", listingID)
}

func (s *Store) ProviderReviewSummary(ctx context.Context, providerID int64) (ReviewSummary, error) {
	return s.reviewSummary(ctx, "l.provider_id = $1", providerID)
}

func (s *Store) reviewSummary(ctx context.Context, cond string, arg int64) (ReviewSummary, error) {
	var sum ReviewSummary
	rows, err := s.pool.Query(ctx, `
		SELECT r.rating, count(*) FROM review r JOIN listing l ON l.id = r.listing_id
		WHERE r.status = 'published' AND `+cond+` GROUP BY r.rating`, arg)
	if err != nil {
		return sum, err
	}
	defer rows.Close()
	total := 0
	for rows.Next() {
		var rating, n int
		if err := rows.Scan(&rating, &n); err != nil {
			return sum, err
		}
		if rating >= 1 && rating <= 5 {
			sum.ByStars[rating] = n
		}
		sum.Count += n
		total += rating * n
	}
	if sum.Count > 0 {
		sum.Average = float64(total) / float64(sum.Count)
	}
	return sum, rows.Err()
}

// ListingReviews — опубликованные отзывы объявления; свой отзыв автор видит всегда.
func (s *Store) ListingReviews(ctx context.Context, listingID, viewerID int64, limit int) ([]Review, error) {
	rows, _ := s.pool.Query(ctx, reviewSelect+`
		WHERE r.listing_id = $1 AND (r.status = 'published' OR r.author_user_id = $2)
		ORDER BY (r.author_user_id = $2) DESC, r.created_at DESC LIMIT $3`, listingID, viewerID, limit)
	return collectAll[Review](rows)
}

// ProviderReviews — отзывы обо всех объявлениях исполнителя, кроме отклонённых.
func (s *Store) ProviderReviews(ctx context.Context, providerID int64) ([]Review, error) {
	rows, _ := s.pool.Query(ctx, reviewSelect+`
		WHERE l.provider_id = $1 AND r.status = 'published'
		ORDER BY (r.reply_text IS NULL) DESC, r.created_at DESC LIMIT 100`, providerID)
	return collectAll[Review](rows)
}

// ReplyToReview — ответ мастера; уходит на модерацию.
func (s *Store) ReplyToReview(ctx context.Context, providerID, reviewID int64, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return ValidationError{"Напишите ответ."}
	}
	if len([]rune(text)) > 1000 {
		return ValidationError{"Ответ — до 1000 символов."}
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE review r SET reply_text = $3, reply_status = 'moderation', reply_rejection = NULL,
		       reply_at = now(), updated_at = now()
		FROM listing l WHERE l.id = r.listing_id AND l.provider_id = $1 AND r.id = $2 AND r.status = 'published'`,
		providerID, reviewID, text)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DisputeReview — мастер просит модератора проверить отзыв.
func (s *Store) DisputeReview(ctx context.Context, providerID, reviewID int64, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ValidationError{"Объясните, что не так с отзывом."}
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE review r SET disputed_at = now(), dispute_reason = $3, updated_at = now()
		FROM listing l WHERE l.id = r.listing_id AND l.provider_id = $1 AND r.id = $2 AND r.status = 'published'`,
		providerID, reviewID, reason)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// Модерация -------------------------------------------------------------------

// ReviewQueueItem — что ждёт модератора: новый отзыв, ответ мастера или спор.
type ReviewQueueItem struct {
	Review
	Kind string // review, reply, dispute
}

func (s *Store) ReviewQueue(ctx context.Context) ([]ReviewQueueItem, error) {
	rows, _ := s.pool.Query(ctx, reviewSelect+`
		WHERE r.status = 'moderation' OR r.reply_status = 'moderation' OR (r.disputed_at IS NOT NULL AND r.status = 'published')
		ORDER BY coalesce(r.disputed_at, r.reply_at, r.created_at) LIMIT 200`)
	list, err := collectAll[Review](rows)
	if err != nil {
		return nil, err
	}
	out := make([]ReviewQueueItem, 0, len(list))
	for _, r := range list {
		kind := "review"
		switch {
		case r.Status == ReviewModeration:
		case r.ReplyStatus != nil && *r.ReplyStatus == ReviewModeration:
			kind = "reply"
		default:
			kind = "dispute"
		}
		out = append(out, ReviewQueueItem{Review: r, Kind: kind})
	}
	return out, nil
}

func (s *Store) CountReviewQueue(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM review
		WHERE status = 'moderation' OR reply_status = 'moderation' OR (disputed_at IS NOT NULL AND status = 'published')`).Scan(&n)
	return n, err
}

func (s *Store) GetReview(ctx context.Context, id int64) (Review, error) {
	rows, _ := s.pool.Query(ctx, reviewSelect+" WHERE r.id = $1", id)
	r, err := collectOne[Review](rows)
	return r, notFound(err)
}

// ReviewChecks — подсказки модератору.
type ReviewChecks struct {
	ContactedAt      *time.Time
	AuthorReviews    int // всего отзывов автора
	ProviderLast24h  int // отзывов о мастере за сутки
	HasLinksOrPhones bool
}

func (s *Store) ReviewChecks(ctx context.Context, r Review) ReviewChecks {
	var c ReviewChecks
	_ = s.pool.QueryRow(ctx, `
		SELECT min(created_at) FROM event WHERE type = 'contact_clicked' AND object_type = 'listing'
		AND object_id = $1 AND user_id = $2`, r.ListingID, r.AuthorUserID).Scan(&c.ContactedAt)
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM review WHERE author_user_id = $1`, r.AuthorUserID).Scan(&c.AuthorReviews)
	_ = s.pool.QueryRow(ctx, `
		SELECT count(*) FROM review rv JOIN listing l ON l.id = rv.listing_id
		WHERE l.provider_id = $1 AND rv.created_at > now() - interval '24 hours'`, r.ProviderID).Scan(&c.ProviderLast24h)
	text := strings.ToLower(deref(r.Text) + " " + deref(r.ReplyText))
	c.HasLinksOrPhones = strings.Contains(text, "http") || strings.Contains(text, "t.me") || strings.Contains(text, "@") ||
		len(onlyDigitsStr(text)) >= 8
	return c
}

func onlyDigitsStr(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ReviewDecision — кому и что сообщить после решения модератора.
type ReviewDecision struct {
	NotifyTelegramID int64
	ListingID        int64
	ListingTitle     string
}

// ModerateReview: kind review|reply|dispute, approve true — опубликовать / оставить отзыв.
func (s *Store) ModerateReview(ctx context.Context, actorID, reviewID int64, kind string, approve bool, reason string) (ReviewDecision, error) {
	reason = strings.TrimSpace(reason)
	if !approve && kind != "dispute" && reason == "" {
		return ReviewDecision{}, ValidationError{"Укажите причину отказа — автор увидит её в боте."}
	}
	var d ReviewDecision
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var q string
		switch {
		case kind == "review" && approve:
			q = `UPDATE review SET status = 'published', reviewed_by = $2, reviewed_at = now(), updated_at = now()
			     WHERE id = $1 AND status = 'moderation'`
		case kind == "review":
			q = `UPDATE review SET status = 'rejected', rejection_reason = $3, reviewed_by = $2, reviewed_at = now(), updated_at = now()
			     WHERE id = $1 AND status = 'moderation'`
		case kind == "reply" && approve:
			q = `UPDATE review SET reply_status = 'published', reviewed_by = $2, reviewed_at = now(), updated_at = now()
			     WHERE id = $1 AND reply_status = 'moderation'`
		case kind == "reply":
			q = `UPDATE review SET reply_status = 'rejected', reply_rejection = $3, reviewed_by = $2, reviewed_at = now(), updated_at = now()
			     WHERE id = $1 AND reply_status = 'moderation'`
		case kind == "dispute" && approve:
			// Спор принят: отзыв снимаем.
			q = `UPDATE review SET status = 'rejected', rejection_reason = coalesce(nullif($3, ''), 'Снят по жалобе мастера'),
			     disputed_at = NULL, reviewed_by = $2, reviewed_at = now(), updated_at = now()
			     WHERE id = $1 AND disputed_at IS NOT NULL`
		case kind == "dispute":
			q = `UPDATE review SET disputed_at = NULL, reviewed_by = $2, reviewed_at = now(), updated_at = now()
			     WHERE id = $1 AND disputed_at IS NOT NULL`
		default:
			return ValidationError{"Неизвестное действие."}
		}
		args := []any{reviewID, actorID}
		if strings.Contains(q, "$3") {
			args = append(args, reason)
		}
		tag, err := tx.Exec(ctx, q, args...)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		var title *string
		if kind == "review" {
			err = tx.QueryRow(ctx, `SELECT u.telegram_id, l.id, l.title FROM review r JOIN users u ON u.id = r.author_user_id
				JOIN listing l ON l.id = r.listing_id WHERE r.id = $1`, reviewID).Scan(&d.NotifyTelegramID, &d.ListingID, &title)
		} else {
			err = tx.QueryRow(ctx, `SELECT u.telegram_id, l.id, l.title FROM review r JOIN listing l ON l.id = r.listing_id
				JOIN provider p ON p.id = l.provider_id JOIN users u ON u.id = p.user_id WHERE r.id = $1`, reviewID).Scan(&d.NotifyTelegramID, &d.ListingID, &title)
		}
		if err != nil {
			return err
		}
		d.ListingTitle = deref(title)
		action := "review." + kind
		if approve {
			action += ".approve"
		} else {
			action += ".reject"
		}
		return audit(ctx, tx, actorID, action, "review", reviewID, map[string]any{"reason": reason})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return d, err
}
