// Package web — HTTP-сервер: админка с CRM. Позже здесь же будет веб-каталог.
package web

import (
	"context"
	"embed"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/a-h/templ"

	"github.com/signaveris-13/uslugi/internal/store"
	"github.com/signaveris-13/uslugi/internal/web/views"
)

//go:embed static
var staticFS embed.FS

type Server struct {
	store       *store.Store
	log         *slog.Logger
	botUsername string
	secure      bool // cookie только по HTTPS (всё, кроме локального запуска)
	tz          *time.Location
}

type Options struct {
	BotUsername  string
	SecureCookie bool
	TimeZone     *time.Location
}

func New(s *store.Store, log *slog.Logger, opt Options) *Server {
	tz := opt.TimeZone
	if tz == nil {
		tz = time.UTC
	}
	return &Server{store: s, log: log, botUsername: opt.BotUsername, secure: opt.SecureCookie, tz: tz}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /static/", http.FileServerFS(staticFS))
	mux.HandleFunc("GET /healthz", s.healthz)

	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("GET /login/status", s.loginStatus)
	mux.HandleFunc("POST /logout", s.logout)

	auth := func(h func(http.ResponseWriter, *http.Request, store.TeamMember)) http.Handler {
		return s.requireMember(h)
	}
	mux.Handle("GET /{$}", http.RedirectHandler("/leads", http.StatusFound))
	mux.Handle("GET /leads", auth(s.leadsList))
	mux.Handle("GET /leads/board", auth(s.leadsBoard))
	mux.Handle("GET /leads/new", auth(s.leadNew))
	mux.Handle("POST /leads", auth(s.leadCreate))
	mux.Handle("GET /leads/{id}", auth(s.leadShow))
	mux.Handle("POST /leads/{id}", auth(s.leadUpdate))
	mux.Handle("POST /leads/{id}/status", auth(s.leadStatus))
	mux.Handle("POST /leads/{id}/assignee", auth(s.leadAssignee))
	mux.Handle("POST /leads/{id}/invite", auth(s.leadInvite))
	mux.Handle("POST /leads/{id}/touches", auth(s.leadTouch))

	return securityHeaders(mux)
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if _, err := s.store.ListCities(ctx); err != nil {
		http.Error(w, "db down", http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) page(me store.TeamMember, title, nav string) views.Page {
	return views.Page{Title: title, Me: me, Nav: nav, TZ: s.tz}
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := c.Render(r.Context(), w); err != nil {
		s.log.Error("render", "err", err, "path", r.URL.Path)
	}
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	s.log.Error("request", "err", err, "method", r.Method, "path", r.URL.Path)
	http.Error(w, "Внутренняя ошибка", http.StatusInternalServerError)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}
