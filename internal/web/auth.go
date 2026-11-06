package web

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/signaveris-13/uslugi/internal/store"
	"github.com/signaveris-13/uslugi/internal/web/views"
)

const (
	sessionCookie = "uslugi_session"
	loginCookie   = "uslugi_login"
)

// requireMember пускает только участников команды с живой сессией.
func (s *Server) requireMember(h func(http.ResponseWriter, *http.Request, store.TeamMember)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err == nil {
			m, err := s.store.SessionMember(r.Context(), c.Value)
			if err == nil {
				h(w, r, m)
				return
			}
			if !errors.Is(err, store.ErrNotFound) {
				s.fail(w, r, err)
				return
			}
		}
		// htmx-запрос с протухшей сессией: просим браузер целиком перейти на вход.
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Redirect", "/login")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if _, err := s.store.SessionMember(r.Context(), c.Value); err == nil {
			http.Redirect(w, r, "/leads", http.StatusSeeOther)
			return
		}
	}

	link := ""
	if s.botUsername != "" {
		token, err := s.store.CreateLogin(r.Context())
		if err != nil {
			s.fail(w, r, err)
			return
		}
		http.SetCookie(w, s.cookie(loginCookie, token, store.LoginTTL))
		link = "https://t.me/" + url.PathEscape(s.botUsername) + "?start=login_" + token
	}
	s.render(w, r, http.StatusOK, views.LoginPage(link))
}

// loginStatus опрашивается страницей входа каждые 2 секунды.
func (s *Server) loginStatus(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(loginCookie)
	if err != nil {
		s.render(w, r, http.StatusOK, views.LoginExpired())
		return
	}
	state, sid, err := s.store.ExchangeLogin(r.Context(), c.Value)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	switch state {
	case store.LoginConfirmed:
		http.SetCookie(w, s.cookie(sessionCookie, sid, store.SessionTTL))
		http.SetCookie(w, s.cookie(loginCookie, "", -1))
		w.Header().Set("HX-Redirect", "/leads")
		w.WriteHeader(http.StatusNoContent)
	case store.LoginPending:
		s.render(w, r, http.StatusOK, views.LoginPending())
	default:
		s.render(w, r, http.StatusOK, views.LoginExpired())
	}
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.store.DeleteSession(r.Context(), c.Value); err != nil {
			s.log.Error("logout", "err", err)
		}
	}
	http.SetCookie(w, s.cookie(sessionCookie, "", -1))
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// cookie — HttpOnly и SameSite=Lax: формы с чужих сайтов не получат сессию.
func (s *Server) cookie(name, value string, ttl time.Duration) *http.Cookie {
	c := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	}
	if ttl < 0 {
		c.MaxAge = -1
	} else {
		c.MaxAge = int(ttl.Seconds())
	}
	return c
}
