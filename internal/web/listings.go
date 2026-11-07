package web

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/signaveris-13/uslugi/internal/store"
	"github.com/signaveris-13/uslugi/internal/web/views"
)

// Telegram — что веб-серверу нужно от бота. nil — Telegram не настроен (локально без токена).
type Telegram interface {
	Notify(ctx context.Context, chatID int64, html, buttonText, buttonData string) error
	FileURL(ctx context.Context, fileID string) (string, error)
}

var listingNotices = map[string]string{
	"approved":   "Объявление опубликовано, исполнитель получил уведомление.",
	"rejected":   "Объявление отклонено, исполнитель получил причину.",
	"blocked":    "Исполнитель заблокирован, его объявления в архиве.",
	"unblocked":  "Исполнитель разблокирован.",
	"complaint":  "Жалоба закрыта.",
	"approved_n": "Объявление опубликовано, но уведомить исполнителя не удалось.",
	"rejected_n": "Объявление отклонено, но уведомить исполнителя не удалось.",
}

func (s *Server) listingsList(w http.ResponseWriter, r *http.Request, me store.TeamMember) {
	status := r.URL.Query().Get("status")
	page := s.page(r, me, "Объявления", "listings")
	switch {
	case status == "all":
		status = ""
	case status == "":
		// По умолчанию — очередь, если в ней что-то есть.
		if page.QueueCount > 0 {
			status = store.ListingModeration
		}
	case store.ListingStatusLabels[status] == "":
		status = ""
	}
	f := store.ListingFilter{Status: status, Query: r.URL.Query().Get("q")}
	rows, err := s.store.ListListings(r.Context(), f)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, views.ListingsPage(views.ListingsData{Page: page, Rows: rows, Filter: f}))
}

func (s *Server) listingShow(w http.ResponseWriter, r *http.Request, me store.TeamMember) {
	s.renderListing(w, r, me, parseID(r.PathValue("id")), "", http.StatusOK)
}

func (s *Server) renderListing(w http.ResponseWriter, r *http.Request, me store.TeamMember, id int64, errText string, status int) {
	ctx := r.Context()
	l, err := s.store.GetListing(ctx, id, 0)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p, err := s.store.ProviderInfo(ctx, l.ProviderID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	dups, err := s.store.ListingDuplicates(ctx, l)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	hist, err := s.store.ListingHistory(ctx, l.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	complaints, err := s.store.ListingComplaints(ctx, l.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, status, views.ListingPage(views.ListingData{
		Page: s.page(r, me, l.TitleOr(), "listings"), Listing: l, Provider: p, Duplicates: dups, History: hist,
		Complaints: complaints,
		Error:      errText, Notice: listingNotices[r.URL.Query().Get("msg")],
	}))
}

func (s *Server) listingApprove(w http.ResponseWriter, r *http.Request, me store.TeamMember) {
	id := parseID(r.PathValue("id"))
	res, err := s.store.ApproveListing(r.Context(), me.ID, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.renderListing(w, r, me, id, "Это объявление уже не на модерации — обнови страницу.", http.StatusConflict)
			return
		}
		s.fail(w, r, err)
		return
	}
	msg := "approved"
	text := fmt.Sprintf("✅ Объявление «%s» опубликовано — клиенты уже могут его найти.", html.EscapeString(res.Title))
	if !s.notify(r, res.TelegramID, text) {
		msg = "approved_n"
	}
	http.Redirect(w, r, fmt.Sprintf("/listings/%d?msg=%s", id, msg), http.StatusSeeOther)
}

func (s *Server) listingReject(w http.ResponseWriter, r *http.Request, me store.TeamMember) {
	id := parseID(r.PathValue("id"))
	reason := strings.TrimSpace(r.FormValue("reason"))
	if c := strings.TrimSpace(r.FormValue("comment")); c != "" {
		if reason != "" {
			reason += ". " + c
		} else {
			reason = c
		}
	}
	res, err := s.store.RejectListing(r.Context(), me.ID, id, reason)
	var ve store.ValidationError
	switch {
	case errors.As(err, &ve):
		s.renderListing(w, r, me, id, ve.Msg, http.StatusUnprocessableEntity)
		return
	case errors.Is(err, store.ErrNotFound):
		s.renderListing(w, r, me, id, "Это объявление нельзя отклонить в текущем статусе — обнови страницу.", http.StatusConflict)
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	msg := "rejected"
	text := fmt.Sprintf("⚠️ Объявление «%s» не прошло модерацию.\n\n<b>Причина:</b> %s\n\nИсправь его в «Мои объявления» и отправь снова.",
		html.EscapeString(res.Title), html.EscapeString(reason))
	if !s.notify(r, res.TelegramID, text) {
		msg = "rejected_n"
	}
	http.Redirect(w, r, fmt.Sprintf("/listings/%d?msg=%s", id, msg), http.StatusSeeOther)
}

func (s *Server) providerBlock(w http.ResponseWriter, r *http.Request, me store.TeamMember) {
	if err := s.store.BlockProvider(r.Context(), me.ID, parseID(r.PathValue("id")), r.FormValue("reason")); err != nil {
		s.fail(w, r, err)
		return
	}
	redirectBack(w, r, "blocked")
}

func (s *Server) providerUnblock(w http.ResponseWriter, r *http.Request, me store.TeamMember) {
	if err := s.store.UnblockProvider(r.Context(), me.ID, parseID(r.PathValue("id"))); err != nil {
		s.fail(w, r, err)
		return
	}
	redirectBack(w, r, "unblocked")
}

func redirectBack(w http.ResponseWriter, r *http.Request, msg string) {
	back := r.FormValue("back")
	if !strings.HasPrefix(back, "/listings/") {
		back = "/listings"
	}
	http.Redirect(w, r, back+"?msg="+msg, http.StatusSeeOther)
}

// notify пишет исполнителю в бот. Возвращает false, если не получилось.
func (s *Server) notify(r *http.Request, chatID int64, text string) bool {
	if s.tg == nil {
		s.log.Warn("Telegram не настроен — уведомление не отправлено", "chat_id", chatID)
		return false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.tg.Notify(ctx, chatID, text, "📋 Мои объявления", "m:mine"); err != nil {
		s.log.Warn("уведомление исполнителю", "err", err, "chat_id", chatID)
		return false
	}
	return true
}

var fileIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{10,200}$`)

// telegramMedia отдаёт фото объявления, скачивая его у Telegram. Ссылка Telegram
// содержит токен бота, поэтому браузер ходит сюда, а не туда напрямую.
func (s *Server) telegramMedia(w http.ResponseWriter, r *http.Request, _ store.TeamMember) {
	fileID := r.PathValue("fileID")
	if !fileIDRe.MatchString(fileID) || s.tg == nil {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	u, err := s.tg.FileURL(ctx, fileID)
	if err != nil {
		s.log.Warn("фото из Telegram", "err", err)
		http.NotFound(w, r)
		return
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "Telegram недоступен", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.NotFound(w, r)
		return
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "image/") {
		ct = "image/jpeg"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	if resp.ContentLength > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 20<<20))
}

func (s *Server) complaintsList(w http.ResponseWriter, r *http.Request, me store.TeamMember) {
	status := "new"
	if r.URL.Query().Get("status") == "all" {
		status = ""
	}
	list, err := s.store.ListComplaints(r.Context(), status)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, views.ComplaintsPage(views.ComplaintsData{
		Page: s.page(r, me, "Жалобы", "listings"), Complaints: list, Status: status,
	}))
}

func (s *Server) complaintResolve(w http.ResponseWriter, r *http.Request, me store.TeamMember) {
	_, err := s.store.ResolveComplaint(r.Context(), me.ID, parseID(r.PathValue("id")), r.FormValue("status"), r.FormValue("resolution"))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, err)
		return
	}
	redirectBack(w, r, "complaint")
}
