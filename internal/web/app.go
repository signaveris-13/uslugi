package web

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/signaveris-13/uslugi/internal/media"
	"github.com/signaveris-13/uslugi/internal/store"
	"github.com/signaveris-13/uslugi/internal/web/views"
)

// Mini App: исполнитель заводит и правит объявления в интерфейсе внутри Telegram.
// Экран — HTML-фрагмент, htmx подставляет его в #app. Авторизация — заголовок X-App-Token.

type appHandler func(w http.ResponseWriter, r *http.Request, u store.User)

func (s *Server) appRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /app", s.appShell)
	mux.HandleFunc("GET /app/{$}", s.appShell)
	mux.HandleFunc("POST /app/auth", s.appAuth)
	mux.HandleFunc("GET /app/photo/{key}", s.appPhoto)
	mux.HandleFunc("GET /app/tgphoto/{fileID}", s.appTelegramPhoto)
	mux.Handle("GET /app/my", s.appUser(s.appMy))
	mux.Handle("GET /app/city", s.appUser(s.appCityPage))
	mux.Handle("POST /app/city", s.appUser(s.appCity))
	s.appClientRoutes(mux)
	mux.Handle("POST /app/listings", s.appUser(s.appNewListing))
	mux.Handle("GET /app/listings/{id}", s.appUser(s.appEditor))
	mux.Handle("POST /app/listings/{id}", s.appUser(s.appEditorPost))
}

func (s *Server) appShell(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, r, http.StatusOK, views.AppShell(s.devLogin, s.envLabel))
}

// appAuth меняет initData от Telegram на токен приложения.
func (s *Server) appAuth(w http.ResponseWriter, r *http.Request) {
	var tg store.TelegramUser
	var err error
	if raw := r.FormValue("init_data"); raw != "" && s.botToken != "" {
		tg, err = ValidateInitData(raw, s.botToken, time.Now())
	} else if s.devLogin && r.FormValue("dev_telegram_id") != "" {
		tg.ID, err = strconv.ParseInt(r.FormValue("dev_telegram_id"), 10, 64)
		tg.Username = strings.TrimPrefix(strings.TrimSpace(r.FormValue("dev_username")), "@")
		tg.FirstName = "Dev"
	} else {
		err = errBadInitData
	}
	if err != nil || tg.ID <= 0 {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "Открой приложение из бота в Telegram.", "dev": s.devLogin})
		return
	}
	u, _, err := s.store.UpsertBotUser(r.Context(), tg, "miniapp")
	if err != nil {
		s.log.Error("mini app: пользователь", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Не получилось войти, попробуй ещё раз."})
		return
	}
	if u.IsBlocked {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Доступ для этого аккаунта закрыт."})
		return
	}
	_ = s.store.LogEvent(r.Context(), "miniapp_opened", u.ID, "", 0, "", nil)
	writeJSON(w, http.StatusOK, map[string]any{"token": s.issueAppToken(u.ID, time.Now())})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) appUser(h appHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, ok := s.parseAppToken(r.Header.Get("X-App-Token"), time.Now())
		if !ok {
			// miniapp.js по 401 заново проходит вход через initData.
			w.Header().Set("HX-Trigger", "app-reauth")
			http.Error(w, "Нужно войти заново", http.StatusUnauthorized)
			return
		}
		u, err := s.store.UserByID(r.Context(), uid)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				w.Header().Set("HX-Trigger", "app-reauth")
				http.Error(w, "Нужно войти заново", http.StatusUnauthorized)
				return
			}
			s.fail(w, r, err)
			return
		}
		if u.IsBlocked {
			http.Error(w, "Доступ для этого аккаунта закрыт", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		h(w, r, u)
	})
}

// Мои объявления ---------------------------------------------------------------

func (s *Server) appMy(w http.ResponseWriter, r *http.Request, u store.User) {
	s.renderMy(w, r, u, "", "")
}

func (s *Server) renderMy(w http.ResponseWriter, r *http.Request, u store.User, notice, errText string) {
	ctx := r.Context()
	if u.CityID == nil {
		s.renderCity(w, r, u, "my")
		return
	}
	d := views.AppMyData{Notice: notice, Error: errText}
	p, err := s.store.ProviderByUser(ctx, u.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, err)
		return
	}
	if err == nil {
		cards, err := s.store.ProviderCards(ctx, p.ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		now := time.Now()
		for _, c := range cards {
			card := views.AppCard{ProviderCard: c}
			if c.PhotoKey != nil || c.PhotoFileID != nil {
				card.PhotoURL = s.photoLink(store.SnapshotPhoto{StorageKey: deref(c.PhotoKey), TelegramFileID: deref(c.PhotoFileID)}, now)
			}
			d.Cards = append(d.Cards, card)
		}
		d.Active, _ = s.store.CountActiveListings(ctx, p.ID)
		d.Limit = s.store.ProviderListingLimit(ctx, p)
	}
	s.render(w, r, http.StatusOK, views.AppMy(d))
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (s *Server) appCity(w http.ResponseWriter, r *http.Request, u store.User) {
	id, err := strconv.ParseInt(r.FormValue("city_id"), 10, 64)
	if err != nil {
		http.Error(w, "Город не выбран", http.StatusBadRequest)
		return
	}
	if err := s.store.SetUserCity(r.Context(), u.ID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	u.CityID = &id
	switch r.FormValue("next") {
	case "my":
		s.renderMy(w, r, u, "", "")
	case "profile":
		s.appProfile(w, r, u)
	default:
		s.renderHome(w, r, u)
	}
}

// appNewListing — «Новое объявление»: те же правила, что в боте (черновик, лимит, блокировка).
func (s *Server) appNewListing(w http.ResponseWriter, r *http.Request, u store.User) {
	ctx := r.Context()
	if u.CityID == nil {
		s.renderMy(w, r, u, "", "Сначала выбери город.")
		return
	}
	p, err := s.store.EnsureProvider(ctx, u, deref(u.FirstName))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if p.IsBlocked {
		s.renderMy(w, r, u, "", "Размещение объявлений для этого аккаунта недоступно.")
		return
	}
	if id, _, err := s.store.ProviderDraft(ctx, p.ID); err == nil {
		s.renderEditor(w, r, u, p, id, nil, "", "Продолжаем незаконченный черновик.")
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, err)
		return
	}
	active, err := s.store.CountActiveListings(ctx, p.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if limit := s.store.ProviderListingLimit(ctx, p); active >= limit {
		s.renderMy(w, r, u, "", "Сейчас можно держать до "+strconv.Itoa(limit)+" объявлений, и у тебя их уже "+strconv.Itoa(active)+". Сними одно с публикации, чтобы добавить новое.")
		return
	}
	id, err := s.store.CreateListingDraft(ctx, p.ID, store.ListingDraftInput{
		CityID:   *u.CityID,
		Telegram: deref(u.Username),
		Currency: s.store.CountryCurrencyByCity(ctx, *u.CityID),
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	_ = s.store.LogEvent(ctx, "listing_draft_created", u.ID, "listing", id, "miniapp", nil)
	s.renderEditor(w, r, u, p, id, nil, "", "")
}

// Форма -----------------------------------------------------------------------

func (s *Server) appProvider(w http.ResponseWriter, r *http.Request, u store.User) (store.Provider, int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return store.Provider{}, 0, false
	}
	p, err := s.store.ProviderByUser(r.Context(), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return store.Provider{}, 0, false
	}
	if p.IsBlocked {
		http.Error(w, "Размещение объявлений для этого аккаунта недоступно", http.StatusForbidden)
		return store.Provider{}, 0, false
	}
	return p, id, true
}

func (s *Server) appEditor(w http.ResponseWriter, r *http.Request, u store.User) {
	p, id, ok := s.appProvider(w, r, u)
	if !ok {
		return
	}
	s.renderEditor(w, r, u, p, id, nil, "", "")
}

// renderEditor показывает форму. override — несохранённые значения, если сохранение не прошло проверку.
func (s *Server) renderEditor(w http.ResponseWriter, r *http.Request, u store.User, p store.Provider, listingID int64, override *store.Snapshot, errText, notice string) {
	ctx := r.Context()
	st, err := s.store.LoadEditor(ctx, listingID, p.ID)
	if errors.Is(err, store.ErrNotEditable) {
		s.renderMy(w, r, u, "", "Это объявление сейчас нельзя править.")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if override != nil {
		override.Photos = st.Snapshot.Photos
		st.Snapshot = *override
	}
	d := views.AppEditorData{State: st, Username: deref(u.Username), Error: errText, Notice: notice}
	cats, err := s.store.ListSubcategories(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d.Groups = views.GroupCategories(cats)
	if st.Snapshot.PrimaryCategoryID != 0 {
		if parent, err := s.store.CategoryParentID(ctx, st.Snapshot.PrimaryCategoryID); err == nil {
			sib, err := s.store.ListChildCategories(ctx, parent)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			for _, c := range sib {
				if c.ID != st.Snapshot.PrimaryCategoryID {
					d.Siblings = append(d.Siblings, c)
				}
			}
		}
	}
	cityID := int64(0)
	if u.CityID != nil {
		cityID = *u.CityID
	} else if len(st.Snapshot.CityIDs) > 0 {
		cityID = st.Snapshot.CityIDs[0]
	}
	if d.Cities, err = s.store.ListActiveCitiesInCountryOf(ctx, cityID); err != nil {
		s.fail(w, r, err)
		return
	}
	now := time.Now()
	for _, ph := range st.Snapshot.Photos {
		d.PhotoURLs = append(d.PhotoURLs, s.photoLink(ph, now))
	}
	if a := st.Snapshot.PriceAmount; a != nil {
		d.PriceInput = store.AmountInput(*a)
	}
	if p.TermsAcceptedAt == nil && st.Mode == store.EditDirect {
		d.TermsNeeded = true
		d.TermsText = s.store.BotText(ctx, "terms", "Правила площадки: Uslugi — площадка, а не сторона сделки.")
	}
	s.render(w, r, http.StatusOK, views.AppEditor(d))
}

// snapshotFromForm собирает поля формы поверх текущей версии (фото форма не присылает).
func snapshotFromForm(r *http.Request, cur store.Snapshot, username string) (store.Snapshot, string) {
	f := r.Form
	sn := cur
	sn.Title = f.Get("title")
	sn.Description = f.Get("description")
	sn.Schedule = f.Get("schedule")
	sn.PrimaryCategoryID, _ = strconv.ParseInt(f.Get("primary_category_id"), 10, 64)
	sn.ExtraCategoryIDs = parseIDs(f["extra_category_id"])
	sn.CityIDs = parseIDs(f["city_id"])
	sn.TravelsToClient = f.Get("travels") == "1"
	sn.Languages = nil
	for _, c := range f["language"] {
		if slices.Contains(store.LanguageCodes, c) {
			sn.Languages = append(sn.Languages, c)
		}
	}
	sn.Telegram = ""
	if f.Get("show_telegram") == "1" {
		sn.Telegram = username
	}
	sn.WhatsApp = store.NormalizeContact("whatsapp", f.Get("whatsapp"))
	sn.Phone = store.NormalizeContact("phone", f.Get("phone"))
	sn.Instagram = store.NormalizeContact("instagram", f.Get("instagram"))
	if c := f.Get("currency"); c == "VND" || c == "USD" {
		sn.Currency = c
	}

	var problem string
	sn.PriceType = f.Get("price_type")
	sn.PriceAmount = nil
	switch sn.PriceType {
	case "fixed", "from":
		if raw := strings.TrimSpace(f.Get("price_amount")); raw != "" {
			if v, ok := store.ParseAmount(raw); ok {
				sn.PriceAmount = &v
			} else {
				problem = "Цена: напиши число, например 450 000 или 450к."
			}
		}
	case "negotiable":
	default:
		sn.PriceType = ""
	}
	return sn, problem
}

func parseIDs(vals []string) []int64 {
	var out []int64
	for _, v := range vals {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			out = append(out, id)
		}
	}
	return out
}

func (s *Server) appEditorPost(w http.ResponseWriter, r *http.Request, u store.User) {
	ctx := r.Context()
	p, id, ok := s.appProvider(w, r, u)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, (store.MaxPhotos+1)*media.MaxUploadBytes)
	if err := r.ParseMultipartForm(8 << 20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		s.renderEditor(w, r, u, p, id, nil, "Файл слишком большой — до 15 МБ на фото.", "")
		return
	}
	st, err := s.store.LoadEditor(ctx, id, p.ID)
	if errors.Is(err, store.ErrNotEditable) {
		s.renderMy(w, r, u, "", "Это объявление сейчас нельзя править.")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	sn, problem := snapshotFromForm(r, st.Snapshot, deref(u.Username))
	action := r.FormValue("action")
	if r.MultipartForm != nil && len(r.MultipartForm.File["photos"]) > 0 && action == "" {
		action = "upload"
	}
	if action == "refresh" {
		// Сменили категорию: дополнительные подкатегории из прежней больше не подходят.
		sn.ExtraCategoryIDs = nil
		problem = ""
	}
	if problem != "" {
		s.renderEditor(w, r, u, p, id, &sn, problem, "")
		return
	}
	if _, err := s.store.SaveEditor(ctx, id, p.ID, sn); err != nil {
		var ve store.ValidationError
		if errors.As(err, &ve) {
			s.renderEditor(w, r, u, p, id, &sn, ve.Msg, "")
			return
		}
		s.fail(w, r, err)
		return
	}

	switch {
	case action == "upload":
		s.appUpload(w, r, u, p, id)
		return
	case strings.HasPrefix(action, "delphoto:"):
		n, _ := strconv.Atoi(strings.TrimPrefix(action, "delphoto:"))
		if _, err := s.store.RemoveEditorPhoto(ctx, id, p.ID, n); err != nil {
			s.fail(w, r, err)
			return
		}
		s.renderEditor(w, r, u, p, id, nil, "", "Фото удалено.")
		return
	case action == "submit":
		s.appSubmit(w, r, u, p, id, st)
		return
	case action == "refresh":
		s.renderEditor(w, r, u, p, id, nil, "", "")
		return
	}
	notice := "Сохранено."
	if st.Mode == store.EditRevision {
		notice = "Сохранено. Правка ушла на проверку — клиенты пока видят прежнюю версию."
	}
	s.renderEditor(w, r, u, p, id, nil, "", notice)
}

func (s *Server) appUpload(w http.ResponseWriter, r *http.Request, u store.User, p store.Provider, id int64) {
	if s.media == nil {
		s.renderEditor(w, r, u, p, id, nil, "Загрузка фото сейчас недоступна. Фото можно прислать боту.", "")
		return
	}
	files := r.MultipartForm.File["photos"]
	added := 0
	for _, fh := range files {
		if fh.Size > media.MaxUploadBytes {
			s.renderEditor(w, r, u, p, id, nil, "Фото «"+fh.Filename+"» больше 15 МБ.", "")
			return
		}
		f, err := fh.Open()
		if err != nil {
			s.fail(w, r, err)
			return
		}
		key, err := s.media.Save(f)
		f.Close()
		if errors.Is(err, media.ErrUnsupported) {
			s.renderEditor(w, r, u, p, id, nil, "Подходят фото JPEG и PNG.", "")
			return
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if _, err := s.store.AddEditorPhoto(r.Context(), id, p.ID, key); err != nil {
			var ve store.ValidationError
			if errors.As(err, &ve) {
				s.renderEditor(w, r, u, p, id, nil, ve.Msg, "")
				return
			}
			s.fail(w, r, err)
			return
		}
		added++
	}
	s.renderEditor(w, r, u, p, id, nil, "", "Фото добавлено: "+strconv.Itoa(added)+".")
}

func (s *Server) appSubmit(w http.ResponseWriter, r *http.Request, u store.User, p store.Provider, id int64, before store.EditorState) {
	ctx := r.Context()
	if p.TermsAcceptedAt == nil && before.Mode == store.EditDirect {
		if r.FormValue("terms") != "1" {
			s.renderEditor(w, r, u, p, id, nil, "Отметь, что принимаешь правила площадки.", "")
			return
		}
	}
	err := s.store.SubmitEditor(ctx, id, p.ID)
	var inc store.ErrIncomplete
	var ve store.ValidationError
	switch {
	case errors.As(err, &inc):
		s.renderEditor(w, r, u, p, id, nil, "Не хватает: "+strings.Join(inc.Missing, ", ")+".", "")
		return
	case errors.As(err, &ve):
		s.renderEditor(w, r, u, p, id, nil, ve.Msg, "")
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	if p.TermsAcceptedAt == nil && before.Mode == store.EditDirect {
		if err := s.store.AcceptTerms(ctx, p.ID); err != nil {
			s.log.Warn("правила", "err", err)
		}
	}
	title := before.Snapshot.Title
	if st, err := s.store.LoadEditor(ctx, id, p.ID); err == nil && st.Snapshot.Title != "" {
		title = st.Snapshot.Title
	}
	switch {
	case before.Mode == store.EditRevision:
		_ = s.store.LogEvent(ctx, "listing_revision_submitted", u.ID, "listing", id, "miniapp", nil)
		s.render(w, r, http.StatusOK, views.AppSubmitted(title, true))
	case before.Listing.Status == store.ListingModeration:
		s.renderMy(w, r, u, "Сохранено. Объявление по-прежнему на проверке.", "")
	default:
		_ = s.store.LogEvent(ctx, "listing_submitted", u.ID, "listing", id, "miniapp", nil)
		s.render(w, r, http.StatusOK, views.AppSubmitted(title, false))
	}
}

// Фото ------------------------------------------------------------------------

func (s *Server) appPhoto(w http.ResponseWriter, r *http.Request) {
	raw, err := base64.RawURLEncoding.DecodeString(r.PathValue("key"))
	key := string(raw)
	q := r.URL.Query()
	if err != nil || !media.ValidKey(key) || s.media == nil || !s.checkPhotoLink("photo", key, q.Get("e"), q.Get("s"), time.Now()) {
		http.NotFound(w, r)
		return
	}
	s.serveMedia(w, r, key)
}

func (s *Server) serveMedia(w http.ResponseWriter, r *http.Request, key string) {
	f, err := s.media.Open(key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
	http.ServeContent(w, r, "", time.Time{}, f)
}

func (s *Server) appTelegramPhoto(w http.ResponseWriter, r *http.Request) {
	fileID := r.PathValue("fileID")
	q := r.URL.Query()
	if !s.checkPhotoLink("tg", fileID, q.Get("e"), q.Get("s"), time.Now()) {
		http.NotFound(w, r)
		return
	}
	s.proxyTelegramFile(w, r, fileID)
}

// adminMedia — фото, загруженные через приложение, для модератора.
func (s *Server) adminMedia(w http.ResponseWriter, r *http.Request, _ store.TeamMember) {
	key := r.PathValue("key")
	if s.media == nil || !media.ValidKey(key) {
		http.NotFound(w, r)
		return
	}
	s.serveMedia(w, r, key)
}
