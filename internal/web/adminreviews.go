package web

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/signaveris-13/uslugi/internal/store"
	"github.com/signaveris-13/uslugi/internal/web/views"
)

// Админка: модерация отзывов и ответы в поддержку.

var reviewNotices = map[string]string{
	"ok":   "Готово, автор получил уведомление.",
	"ok_n": "Готово, но уведомить автора не удалось.",
}

func (s *Server) reviewsPage(w http.ResponseWriter, r *http.Request, me store.TeamMember) {
	s.renderReviews(w, r, me, "", http.StatusOK)
}

func (s *Server) renderReviews(w http.ResponseWriter, r *http.Request, me store.TeamMember, errText string, status int) {
	ctx := r.Context()
	queue, err := s.store.ReviewQueue(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d := views.AdminReviewsData{Page: s.page(r, me, "Отзывы", "reviews"), Queue: queue, Error: errText,
		Notice: reviewNotices[r.URL.Query().Get("msg")]}
	want, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	for i := range queue {
		if queue[i].ID == want || (want == 0 && i == 0) {
			d.Selected = &queue[i]
			break
		}
	}
	if d.Selected == nil && len(queue) > 0 {
		d.Selected = &queue[0]
	}
	if d.Selected != nil {
		d.Checks = s.store.ReviewChecks(ctx, d.Selected.Review)
	}
	s.render(w, r, status, views.ReviewsPage(d))
}

func (s *Server) reviewDecide(w http.ResponseWriter, r *http.Request, me store.TeamMember) {
	id := parseID(r.PathValue("id"))
	kind := r.FormValue("kind")
	approve := r.FormValue("approve") == "1"
	reason := strings.TrimSpace(r.FormValue("reason"))
	if c := strings.TrimSpace(r.FormValue("comment")); c != "" {
		if reason != "" {
			reason += ". " + c
		} else {
			reason = c
		}
	}
	res, err := s.store.ModerateReview(r.Context(), me.ID, id, kind, approve, reason)
	var ve store.ValidationError
	switch {
	case errors.As(err, &ve):
		r.URL.RawQuery = "id=" + strconv.FormatInt(id, 10)
		s.renderReviews(w, r, me, ve.Msg, http.StatusUnprocessableEntity)
		return
	case errors.Is(err, store.ErrNotFound):
		s.renderReviews(w, r, me, "Это уже решено — обновите страницу.", http.StatusConflict)
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	title := html.EscapeString(res.ListingTitle)
	var text, button, screen string
	switch {
	case kind == "review" && approve:
		text = fmt.Sprintf("⭐ Ваш отзыв о «%s» опубликован. Спасибо — так другим проще выбрать мастера.", title)
		button, screen = "Открыть объявление", fmt.Sprintf("l_%d", res.ListingID)
	case kind == "review":
		text = fmt.Sprintf("Ваш отзыв о «%s» не прошёл проверку.\n\n<b>Причина:</b> %s\n\nМожно исправить и отправить снова.", title, html.EscapeString(reason))
		button, screen = "Исправить отзыв", fmt.Sprintf("l_%d", res.ListingID)
	case kind == "reply" && approve:
		text = fmt.Sprintf("Ваш ответ на отзыв о «%s» опубликован.", title)
		button, screen = "Отзывы обо мне", "reviews"
	case kind == "reply":
		text = fmt.Sprintf("Ваш ответ на отзыв о «%s» не прошёл проверку.\n\n<b>Причина:</b> %s", title, html.EscapeString(reason))
		button, screen = "Отзывы обо мне", "reviews"
	case kind == "dispute" && approve:
		text = fmt.Sprintf("Мы проверили отзыв о «%s» и сняли его. Спасибо, что сообщили.", title)
		button, screen = "Отзывы обо мне", "reviews"
	default:
		text = fmt.Sprintf("Мы проверили отзыв о «%s»: нарушений не нашли, отзыв остаётся. Вы можете ответить на него — ответ увидят клиенты.", title)
		button, screen = "Ответить на отзыв", "reviews"
	}
	msg := "ok"
	if !s.notifyScreen(r, res.NotifyTelegramID, text, button, screen) {
		msg = "ok_n"
	}
	http.Redirect(w, r, "/reviews?msg="+msg, http.StatusSeeOther)
}

// Поддержка ---------------------------------------------------------------------

func (s *Server) supportList(w http.ResponseWriter, r *http.Request, me store.TeamMember) {
	status := r.URL.Query().Get("status")
	switch status {
	case "answered", "closed":
	case "all":
		status = ""
	default:
		status = "open"
	}
	list, err := s.store.ListTickets(r.Context(), status)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, views.SupportPage(views.AdminSupportData{Page: s.page(r, me, "Поддержка", "support"), Tickets: list, Status: status}))
}

func (s *Server) supportShow(w http.ResponseWriter, r *http.Request, me store.TeamMember) {
	notice := ""
	switch r.URL.Query().Get("msg") {
	case "sent":
		notice = "Ответ отправлен — пользователь получил уведомление."
	case "sent_n":
		notice = "Ответ сохранён, но уведомить пользователя не удалось."
	case "closed":
		notice = "Обращение закрыто."
	}
	s.renderTicket(w, r, me, parseID(r.PathValue("id")), "", notice, http.StatusOK)
}

func (s *Server) renderTicket(w http.ResponseWriter, r *http.Request, me store.TeamMember, id int64, errText, notice string, status int) {
	t, msgs, err := s.store.Ticket(r.Context(), id, 0)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, status, views.SupportTicketPage(views.AdminTicketData{
		Page: s.page(r, me, fmt.Sprintf("Обращение №%d", id), "support"), Ticket: t, Messages: msgs, Error: errText, Notice: notice,
	}))
}

func (s *Server) supportAnswer(w http.ResponseWriter, r *http.Request, me store.TeamMember) {
	id := parseID(r.PathValue("id"))
	text := r.FormValue("text")
	tgID, err := s.store.AnswerTicket(r.Context(), me.ID, id, text)
	var ve store.ValidationError
	switch {
	case errors.As(err, &ve):
		s.renderTicket(w, r, me, id, ve.Msg, "", http.StatusUnprocessableEntity)
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	note := fmt.Sprintf("💬 Ответ поддержки по обращению №%d:\n\n%s", id, html.EscapeString(strings.TrimSpace(text)))
	msg := "sent"
	if !s.notifyScreen(r, tgID, note, "Открыть обращение", fmt.Sprintf("support_%d", id)) {
		msg = "sent_n"
	}
	http.Redirect(w, r, fmt.Sprintf("/support/%d?msg=%s", id, msg), http.StatusSeeOther)
}

func (s *Server) supportClose(w http.ResponseWriter, r *http.Request, me store.TeamMember) {
	id := parseID(r.PathValue("id"))
	if err := s.store.CloseTicket(r.Context(), id, 0); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/support/%d?msg=closed", id), http.StatusSeeOther)
}
