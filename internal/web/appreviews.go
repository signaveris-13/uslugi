package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/signaveris-13/uslugi/internal/store"
	"github.com/signaveris-13/uslugi/internal/web/views"
)

// Отзывы и поддержка в Mini App.

func (s *Server) appReviewRoutes(mux *http.ServeMux) {
	mux.Handle("GET /app/l/{id}/reviews", s.appUser(s.appReviews))
	mux.Handle("GET /app/l/{id}/review", s.appUser(s.appReviewForm))
	mux.Handle("POST /app/l/{id}/review", s.appUser(s.appReviewPost))
	mux.Handle("GET /app/reviews", s.appUser(s.appProviderReviews))
	mux.Handle("POST /app/reviews/{id}/reply", s.appUser(s.appReviewReply))
	mux.Handle("POST /app/reviews/{id}/dispute", s.appUser(s.appReviewDispute))
	mux.Handle("GET /app/support", s.appUser(s.appSupport))
	mux.Handle("POST /app/support", s.appUser(s.appSupportPost))
	mux.Handle("GET /app/support/{id}", s.appUser(s.appThread))
	mux.Handle("POST /app/support/{id}", s.appUser(s.appThreadPost))
	mux.Handle("POST /app/support/{id}/close", s.appUser(s.appThreadClose))
}

// canLeaveReview — писал мастеру и ещё нет действующего отзыва.
func (s *Server) canLeaveReview(r *http.Request, u store.User, listingID int64) bool {
	if !s.store.CanReview(r.Context(), u.ID, listingID) {
		return false
	}
	if mine, err := s.store.MyReview(r.Context(), u.ID, listingID); err == nil && mine.Status != store.ReviewRejected {
		return false
	}
	return true
}

func (s *Server) appReviews(w http.ResponseWriter, r *http.Request, u store.User) {
	ctx := r.Context()
	l, err := s.store.PublishedListing(ctx, s.listingID(r))
	if err != nil {
		s.renderHomeNotice(w, r, u, "Это объявление больше не опубликовано.")
		return
	}
	sum, err := s.store.ListingReviewSummary(ctx, l.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, err := s.store.ListingReviews(ctx, l.ID, u.ID, 100)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, views.AppReviews(views.AppReviewsData{
		Listing: l, ProviderName: s.store.ListingProviderName(ctx, l.ID), Summary: sum, Reviews: list,
		ViewerID: u.ID, CanReview: s.canLeaveReview(r, u, l.ID),
	}))
}

func (s *Server) appReviewForm(w http.ResponseWriter, r *http.Request, u store.User) {
	s.renderReviewForm(w, r, u, views.AppReviewFormData{Rating: 5, ShowName: true}, "")
}

func (s *Server) renderReviewForm(w http.ResponseWriter, r *http.Request, u store.User, d views.AppReviewFormData, errText string) {
	ctx := r.Context()
	l, err := s.store.PublishedListing(ctx, s.listingID(r))
	if err != nil {
		s.renderHomeNotice(w, r, u, "Это объявление больше не опубликовано.")
		return
	}
	if !s.canLeaveReview(r, u, l.ID) && errText == "" {
		s.renderListingCard(w, r, u, l.ID, "", store.ErrReviewNotAllowed.Msg)
		return
	}
	d.Listing, d.ProviderName, d.Error = l, s.store.ListingProviderName(ctx, l.ID), errText
	d.AuthorName = deref(u.FirstName)
	if d.AuthorName == "" {
		d.AuthorName = "Клиент"
	}
	s.render(w, r, http.StatusOK, views.AppReviewForm(d))
}

func (s *Server) appReviewPost(w http.ResponseWriter, r *http.Request, u store.User) {
	_ = r.ParseForm()
	rating, _ := strconv.Atoi(r.FormValue("rating"))
	in := store.ReviewInput{Rating: rating, Tags: r.Form["tag"], Text: r.FormValue("text"), ShowName: r.FormValue("show_name") == "1"}
	id := s.listingID(r)
	if _, err := s.store.CreateReview(r.Context(), u.ID, id, in); err != nil {
		var ve store.ValidationError
		if errors.As(err, &ve) {
			s.renderReviewForm(w, r, u, views.AppReviewFormData{Rating: rating, Tags: in.Tags, Text: in.Text, ShowName: in.ShowName}, ve.Msg)
			return
		}
		s.fail(w, r, err)
		return
	}
	_ = s.store.LogEvent(r.Context(), "review_submitted", u.ID, "listing", id, "miniapp", map[string]any{"rating": rating})
	s.render(w, r, http.StatusOK, views.AppReviewSent(id))
}

func (s *Server) appProviderReviews(w http.ResponseWriter, r *http.Request, u store.User) {
	s.renderProviderReviews(w, r, u, "", "", 0)
}

func (s *Server) renderProviderReviews(w http.ResponseWriter, r *http.Request, u store.User, notice, errText string, errID int64) {
	ctx := r.Context()
	p, err := s.store.ProviderByUser(ctx, u.ID)
	if err != nil {
		s.appProfile(w, r, u)
		return
	}
	sum, err := s.store.ProviderReviewSummary(ctx, p.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, err := s.store.ProviderReviews(ctx, p.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, views.AppProviderReviews(views.AppProviderReviewsData{
		Summary: sum, Reviews: list, Notice: notice, Error: errText, ErrorID: errID,
	}))
}

func (s *Server) reviewAction(w http.ResponseWriter, r *http.Request, u store.User, act func(providerID, reviewID int64) error, notice string) {
	p, err := s.store.ProviderByUser(r.Context(), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	err = act(p.ID, id)
	var ve store.ValidationError
	switch {
	case errors.As(err, &ve):
		s.renderProviderReviews(w, r, u, "", ve.Msg, id)
	case err != nil:
		s.fail(w, r, err)
	default:
		s.renderProviderReviews(w, r, u, notice, "", 0)
	}
}

func (s *Server) appReviewReply(w http.ResponseWriter, r *http.Request, u store.User) {
	s.reviewAction(w, r, u, func(pid, rid int64) error {
		return s.store.ReplyToReview(r.Context(), pid, rid, r.FormValue("text"))
	}, "Ответ отправлен на проверку.")
}

func (s *Server) appReviewDispute(w http.ResponseWriter, r *http.Request, u store.User) {
	s.reviewAction(w, r, u, func(pid, rid int64) error {
		return s.store.DisputeReview(r.Context(), pid, rid, r.FormValue("reason"))
	}, "Модератор проверит отзыв и напишет вам в бот.")
}

// Поддержка ---------------------------------------------------------------------

func (s *Server) appSupport(w http.ResponseWriter, r *http.Request, u store.User) {
	s.renderSupport(w, r, u, views.AppSupportData{Topic: r.URL.Query().Get("topic")})
}

func (s *Server) renderSupport(w http.ResponseWriter, r *http.Request, u store.User, d views.AppSupportData) {
	ctx := r.Context()
	tickets, err := s.store.UserTickets(ctx, u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d.Tickets = tickets
	if p, err := s.store.ProviderByUser(ctx, u.ID); err == nil {
		d.Listings, _ = s.store.ProviderCards(ctx, p.ID)
	}
	if store.SupportTopicLabels[d.Topic] == "" {
		d.Topic = "other"
		if len(d.Listings) > 0 {
			d.Topic = "listing"
		}
	}
	s.render(w, r, http.StatusOK, views.AppSupport(d))
}

func (s *Server) appSupportPost(w http.ResponseWriter, r *http.Request, u store.User) {
	topic, text := r.FormValue("topic"), strings.TrimSpace(r.FormValue("text"))
	listingID, _ := strconv.ParseInt(r.FormValue("listing_id"), 10, 64)
	id, err := s.store.CreateTicket(r.Context(), u.ID, topic, listingID, text)
	var ve store.ValidationError
	if errors.As(err, &ve) {
		s.renderSupport(w, r, u, views.AppSupportData{Topic: topic, Text: text, Error: ve.Msg})
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	_ = s.store.LogEvent(r.Context(), "support_ticket", u.ID, "support_ticket", id, "miniapp", map[string]any{"topic": topic})
	s.notifySupport(id, text, true)
	s.renderThread(w, r, u, id, "")
}

func (s *Server) ticketID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}

func (s *Server) appThread(w http.ResponseWriter, r *http.Request, u store.User) {
	s.renderThread(w, r, u, s.ticketID(r), "")
}

func (s *Server) renderThread(w http.ResponseWriter, r *http.Request, u store.User, id int64, errText string) {
	t, msgs, err := s.store.Ticket(r.Context(), id, u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, views.AppSupportThread(views.AppThreadData{Ticket: t, Messages: msgs, Error: errText}))
}

func (s *Server) appThreadPost(w http.ResponseWriter, r *http.Request, u store.User) {
	id := s.ticketID(r)
	text := r.FormValue("text")
	err := s.store.AddUserMessage(r.Context(), id, u.ID, text)
	var ve store.ValidationError
	switch {
	case errors.As(err, &ve):
		s.renderThread(w, r, u, id, ve.Msg)
	case err != nil:
		s.fail(w, r, err)
	default:
		s.notifySupport(id, text, false)
		s.renderThread(w, r, u, id, "")
	}
}

func (s *Server) appThreadClose(w http.ResponseWriter, r *http.Request, u store.User) {
	id := s.ticketID(r)
	if err := s.store.CloseTicket(r.Context(), id, u.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.renderThread(w, r, u, id, "")
}
