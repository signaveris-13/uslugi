package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/signaveris-13/uslugi/internal/store"
	"github.com/signaveris-13/uslugi/internal/web/views"
)

// Mini App для клиента: каталог, карточка, связь с мастером, избранное, жалобы,
// профиль. Бот после этого только открывает приложение и присылает уведомления.

func (s *Server) appClientRoutes(mux *http.ServeMux) {
	mux.Handle("GET /app/home", s.appUser(s.appHome))
	mux.Handle("GET /app/search", s.appUser(s.appSearch))
	mux.Handle("GET /app/c/{id}", s.appUser(s.appCategory))
	mux.Handle("GET /app/c/{id}/filters", s.appUser(s.appFilters))
	mux.Handle("GET /app/l/{id}", s.appUser(s.appListing))
	mux.Handle("POST /app/l/{id}/fav", s.appUser(s.appFav))
	mux.Handle("POST /app/l/{id}/contact/{ch}", s.appUser(s.appContact))
	mux.Handle("GET /app/l/{id}/complaint", s.appUser(s.appComplaintPage))
	mux.Handle("POST /app/l/{id}/complaint", s.appUser(s.appComplaint))
	mux.Handle("GET /app/favorites", s.appUser(s.appFavorites))
	mux.Handle("GET /app/profile", s.appUser(s.appProfile))
	mux.Handle("GET /app/rules", s.appUser(s.appRules))
	mux.Handle("POST /app/new", s.appUser(s.appNew))
	mux.Handle("GET /app/listings/{id}/manage", s.appUser(s.appManage))
	mux.Handle("POST /app/listings/{id}/manage/{action}", s.appUser(s.appManageAction))
	s.appReviewRoutes(mux)
}

const appPageSize = 20

// Главная и город ------------------------------------------------------------

func (s *Server) appHome(w http.ResponseWriter, r *http.Request, u store.User) {
	s.renderHome(w, r, u)
}

func (s *Server) renderHome(w http.ResponseWriter, r *http.Request, u store.User) {
	ctx := r.Context()
	if u.CityID == nil {
		s.renderCity(w, r, u, "home")
		return
	}
	cats, err := s.store.CatalogCategories(ctx, *u.CityID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	_, perr := s.store.ProviderByUser(ctx, u.ID)
	s.render(w, r, http.StatusOK, views.AppHome(views.AppHomeData{
		CityName: s.store.CityName(ctx, *u.CityID), Total: s.store.CountCityListings(ctx, *u.CityID),
		Categories: cats, IsProvider: perr == nil,
	}))
}

func (s *Server) appCityPage(w http.ResponseWriter, r *http.Request, u store.User) {
	s.renderCity(w, r, u, r.URL.Query().Get("next"))
}

func (s *Server) renderCity(w http.ResponseWriter, r *http.Request, u store.User, next string) {
	switch next {
	case "my", "profile":
	default:
		next = "home"
	}
	cities, err := s.store.CityCounts(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d := views.AppCityData{Cities: cities, Next: next}
	if u.CityID != nil {
		d.Current = *u.CityID
	}
	s.render(w, r, http.StatusOK, views.AppCity(d))
}

// Список: категория и поиск --------------------------------------------------

// catalogQuery — фильтры из адреса: travels, max, lang, n (сколько показать).
func catalogQuery(r *http.Request, cityID int64) store.CatalogQuery {
	v := r.URL.Query()
	q := store.CatalogQuery{CityID: cityID, Travels: v.Get("travels") == "1", Limit: appPageSize}
	q.MaxPrice, _ = strconv.ParseInt(v.Get("max"), 10, 64)
	if l := v.Get("lang"); store.LanguageLabels[l] != "" {
		q.Language = l
	}
	if n, err := strconv.Atoi(v.Get("n")); err == nil && n > appPageSize && n <= 200 {
		q.Limit = n
	}
	return q
}

func withN(u string, n int) string {
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return u + sep + "n=" + strconv.Itoa(n)
}

func (s *Server) cardViews(cards []store.CatalogCard) []views.AppCardView {
	now := time.Now()
	out := make([]views.AppCardView, 0, len(cards))
	for _, c := range cards {
		v := views.AppCardView{CatalogCard: c}
		if c.PhotoKey != nil || c.PhotoFileID != nil {
			v.PhotoURL = s.photoLink(c.Photo(), now)
		}
		out = append(out, v)
	}
	return out
}

func (s *Server) appCategory(w http.ResponseWriter, r *http.Request, u store.User) {
	ctx := r.Context()
	if u.CityID == nil {
		s.renderCity(w, r, u, "home")
		return
	}
	cid, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	// Ссылка может вести на подкатегорию: открываем её родителя с выбранным чипом.
	parentID, sub := cid, int64(0)
	if p, err := s.store.CategoryParentID(ctx, cid); err == nil {
		parentID, sub = p, cid
	}
	if v, err := strconv.ParseInt(r.URL.Query().Get("sub"), 10, 64); err == nil && v > 0 {
		sub = v
	}
	name, _, err := s.store.CategoryName(ctx, parentID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	q := catalogQuery(r, *u.CityID)
	q.ParentID, q.CategoryID = parentID, sub
	cards, total, err := s.store.SearchCatalog(ctx, q)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	subs, err := s.store.CatalogSubcategories(ctx, parentID, *u.CityID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	base := "/app/c/" + strconv.FormatInt(parentID, 10)
	filters := q
	filters.CategoryID, filters.Limit = 0, 0
	d := views.AppListData{
		Title: name, Sub: s.store.CityName(ctx, *u.CityID), Back: "/app/home",
		Cards: s.cardViews(cards), Total: total, Query: q,
		FilterURL: views.CatalogURL(base+"/filters", store.CatalogQuery{ParentID: parentID, CategoryID: sub, Travels: q.Travels, MaxPrice: q.MaxPrice, Language: q.Language}),
	}
	all := q
	all.CategoryID, all.Limit = 0, 0
	d.Chips = append(d.Chips, views.AppChip{Label: "Все", URL: views.CatalogURL(base, all), On: sub == 0})
	for _, c := range subs {
		cq := all
		cq.CategoryID = c.ID
		d.Chips = append(d.Chips, views.AppChip{Label: c.Name, URL: views.CatalogURL(base, cq), On: sub == c.ID, Count: c.Count})
	}
	if total > len(cards) {
		cur := q
		cur.Limit = 0
		d.MoreURL = withN(views.CatalogURL(base, cur), q.Limit+appPageSize)
	}
	s.render(w, r, http.StatusOK, views.AppList(d))
}

func (s *Server) appFilters(w http.ResponseWriter, r *http.Request, u store.User) {
	ctx := r.Context()
	if u.CityID == nil {
		s.renderCity(w, r, u, "home")
		return
	}
	parentID, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	q := catalogQuery(r, *u.CityID)
	q.ParentID = parentID
	sub, _ := strconv.ParseInt(r.URL.Query().Get("sub"), 10, 64)
	base := "/app/c/" + strconv.FormatInt(parentID, 10)
	back := q
	back.CategoryID, back.Limit = sub, 0
	s.render(w, r, http.StatusOK, views.AppFilters(views.AppFiltersData{
		Action: base, Query: q, SubID: sub, Back: views.CatalogURL(base, back),
	}))
	_ = ctx
}

func (s *Server) appSearch(w http.ResponseWriter, r *http.Request, u store.User) {
	ctx := r.Context()
	if u.CityID == nil {
		s.renderCity(w, r, u, "home")
		return
	}
	text := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(text)) > 80 {
		text = string([]rune(text)[:80])
	}
	d := views.AppListData{Search: true, Text: text}
	if text != "" {
		q := catalogQuery(r, *u.CityID)
		q.Text = text
		cards, total, err := s.store.SearchCatalog(ctx, q)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		subs, err := s.store.SearchSubcategories(ctx, *u.CityID, text)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		d.Cards, d.Total, d.Subcats, d.Query = s.cardViews(cards), total, subs, q
		if total > len(cards) {
			d.MoreURL = withN("/app/search?q="+url.QueryEscape(text), q.Limit+appPageSize)
		}
		_ = s.store.LogEvent(ctx, "search", u.ID, "", 0, "miniapp", map[string]any{"q": text, "results": total})
	}
	s.render(w, r, http.StatusOK, views.AppList(d))
}

// Карточка ---------------------------------------------------------------------

func (s *Server) listingID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}

func (s *Server) shareURL(listingID int64) string {
	if s.botUsername == "" {
		return ""
	}
	link := fmt.Sprintf("https://t.me/%s?start=l_%d", s.botUsername, listingID)
	return "https://t.me/share/url?url=" + url.QueryEscape(link) + "&text=" + url.QueryEscape("Нашёл мастера в Uslugi")
}

func (s *Server) appListing(w http.ResponseWriter, r *http.Request, u store.User) {
	s.renderListingCard(w, r, u, s.listingID(r), "", "")
}

func (s *Server) renderListingCard(w http.ResponseWriter, r *http.Request, u store.User, id int64, notice, errText string) {
	ctx := r.Context()
	l, err := s.store.PublishedListing(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		s.renderHomeNotice(w, r, u, "Это объявление больше не опубликовано.")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Ссылка от друга без выбранного города: берём город объявления.
	if u.CityID == nil && len(l.Cities) > 0 {
		if err := s.store.SetUserCity(ctx, u.ID, l.Cities[0].ID); err == nil {
			u.CityID = &l.Cities[0].ID
		}
	}
	d := views.AppListingData{
		Listing: l, ProviderName: s.store.ListingProviderName(ctx, l.ID), Fav: s.store.IsFavorite(ctx, u.ID, l.ID),
		ShareURL: s.shareURL(l.ID), Notice: notice, Error: errText, Back: "/app/home",
	}
	if l.PrimaryCategory != nil {
		d.Back = "/app/c/" + strconv.FormatInt(l.PrimaryCategory.ID, 10)
	}
	if p, err := s.store.ProviderByUser(ctx, u.ID); err == nil && p.ID == l.ProviderID {
		d.Own, d.Back = true, appManagePath(l.ID)
	}
	now := time.Now()
	for _, ph := range l.Photos {
		d.PhotoURLs = append(d.PhotoURLs, s.photoLink(ph, now))
	}
	d.ViewerID = u.ID
	d.Summary, _ = s.store.ListingReviewSummary(ctx, l.ID)
	d.Reviews, _ = s.store.ListingReviews(ctx, l.ID, u.ID, 3)
	d.CanReview = !d.Own && s.canLeaveReview(r, u, l.ID)
	if !d.Own {
		_ = s.store.LogEvent(ctx, "listing_viewed", u.ID, "listing", l.ID, "miniapp", nil)
	}
	s.render(w, r, http.StatusOK, views.AppListing(d))
}

func appManagePath(id int64) string { return "/app/listings/" + strconv.FormatInt(id, 10) + "/manage" }

func (s *Server) renderHomeNotice(w http.ResponseWriter, r *http.Request, u store.User, text string) {
	w.Header().Set("HX-Trigger", toastTrigger(text))
	s.renderHome(w, r, u)
}

func toastTrigger(text string) string {
	b, _ := json.Marshal(map[string]any{"app-toast": map[string]string{"text": text}})
	return string(b)
}

func (s *Server) appFav(w http.ResponseWriter, r *http.Request, u store.User) {
	id := s.listingID(r)
	on, err := s.store.ToggleFavorite(r.Context(), u.ID, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if on {
		_ = s.store.LogEvent(r.Context(), "favorite_added", u.ID, "listing", id, "miniapp", nil)
	}
	if r.FormValue("from") == "favorites" {
		s.appFavorites(w, r, u)
		return
	}
	s.render(w, r, http.StatusOK, views.FavButton(id, on))
}

// appContact пишет contact_clicked (главная метрика) и открывает чат с мастером.
func (s *Server) appContact(w http.ResponseWriter, r *http.Request, u store.User) {
	l, err := s.store.PublishedListing(r.Context(), s.listingID(r))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ch := r.PathValue("ch")
	var target, text string
	switch ch {
	case "tg":
		if l.ContactTelegram != nil {
			target = "https://t.me/" + url.PathEscape(*l.ContactTelegram)
		}
	case "wa":
		if l.ContactWhatsApp != nil {
			target = "https://wa.me/" + onlyDigits(*l.ContactWhatsApp)
		}
	case "ph":
		if l.ContactPhone != nil {
			target = "tel:" + *l.ContactPhone
			text = "Телефон: " + *l.ContactPhone
		}
	case "ig":
		if l.ContactInstagram != nil {
			target = "https://instagram.com/" + url.PathEscape(*l.ContactInstagram)
		}
	}
	if target == "" {
		http.NotFound(w, r)
		return
	}
	_ = s.store.LogEvent(r.Context(), "contact_clicked", u.ID, "listing", l.ID, "miniapp", map[string]any{"channel": ch})
	b, _ := json.Marshal(map[string]any{"app-open": map[string]string{"url": target, "text": text}})
	w.Header().Set("HX-Trigger", string(b))
	w.WriteHeader(http.StatusNoContent)
}

func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Жалоба -----------------------------------------------------------------------

func (s *Server) appComplaintPage(w http.ResponseWriter, r *http.Request, u store.User) {
	s.renderComplaint(w, r, u, "", "", "")
}

func (s *Server) renderComplaint(w http.ResponseWriter, r *http.Request, u store.User, reason, comment, errText string) {
	l, err := s.store.PublishedListing(r.Context(), s.listingID(r))
	if err != nil {
		s.renderHomeNotice(w, r, u, "Это объявление больше не опубликовано.")
		return
	}
	s.render(w, r, http.StatusOK, views.AppComplaint(views.AppComplaintData{
		Listing: l, Name: s.store.ListingProviderName(r.Context(), l.ID), Reason: reason, Comment: comment, Error: errText,
	}))
}

func (s *Server) appComplaint(w http.ResponseWriter, r *http.Request, u store.User) {
	id := s.listingID(r)
	reason, comment := r.FormValue("reason"), strings.TrimSpace(r.FormValue("comment"))
	err := s.store.CreateComplaint(r.Context(), u.ID, id, reason, comment)
	var ve store.ValidationError
	switch {
	case errors.Is(err, store.ErrDuplicateComplaint):
		s.renderListingCard(w, r, u, id, "Вы уже пожаловались на это объявление — модератор разбирается.", "")
	case errors.As(err, &ve):
		s.renderComplaint(w, r, u, reason, comment, "Выберите, что не так.")
	case err != nil:
		s.fail(w, r, err)
	default:
		s.renderListingCard(w, r, u, id, "Спасибо! Жалоба у модератора.", "")
	}
}

// Избранное и профиль ----------------------------------------------------------

func (s *Server) appFavorites(w http.ResponseWriter, r *http.Request, u store.User) {
	cards, err := s.store.FavoriteCards(r.Context(), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d := views.AppFavData{}
	now := time.Now()
	for _, c := range cards {
		v := views.AppFavCard{FavoriteCard: c}
		if c.PhotoKey != nil || c.PhotoFileID != nil {
			v.PhotoURL = s.photoLink(c.Photo(), now)
		}
		d.Cards = append(d.Cards, v)
	}
	s.render(w, r, http.StatusOK, views.AppFavorites(d))
}

func (s *Server) appProfile(w http.ResponseWriter, r *http.Request, u store.User) {
	ctx := r.Context()
	d := views.AppProfileData{Name: deref(u.FirstName), Username: deref(u.Username)}
	if d.Name == "" {
		d.Name = "Пользователь"
	}
	if u.CityID != nil {
		d.CityName = s.store.CityName(ctx, *u.CityID)
	}
	if p, err := s.store.ProviderByUser(ctx, u.ID); err == nil {
		d.IsProvider = true
		d.Name = p.DisplayName
		d.Stats, _ = s.store.ListingStats(ctx, p.ID, 0, 7)
		d.Active, _ = s.store.CountActiveListings(ctx, p.ID)
		d.Limit = s.store.ProviderListingLimit(ctx, p)
		cards, _ := s.store.ProviderCards(ctx, p.ID)
		d.Summary = providerSummary(cards)
		d.Reviews, _ = s.store.ProviderReviewSummary(ctx, p.ID)
	}
	s.render(w, r, http.StatusOK, views.AppProfile(d))
}

func providerSummary(cards []store.ProviderCard) string {
	counts := map[string]int{}
	for _, c := range cards {
		counts[c.Status]++
	}
	var parts []string
	add := func(status, label string) {
		if n := counts[status]; n > 0 {
			parts = append(parts, strconv.Itoa(n)+" "+label)
		}
	}
	add(store.ListingPublished, "опубликовано")
	add(store.ListingModeration, "на проверке")
	add(store.ListingRejected, "отклонено")
	add(store.ListingPaused, "на паузе")
	add(store.ListingDraft, "черновик")
	if len(parts) == 0 {
		return "Пока нет объявлений"
	}
	return strings.Join(parts, " · ")
}

func (s *Server) appRules(w http.ResponseWriter, r *http.Request, u store.User) {
	s.render(w, r, http.StatusOK, views.AppRules(views.AppRulesData{
		Text: s.store.BotText(r.Context(), "terms", "Правила площадки: Uslugi — площадка, а не сторона сделки."),
	}))
}

// «+»: кто ещё не исполнитель, видит рассказ о размещении; остальные сразу форму.
func (s *Server) appNew(w http.ResponseWriter, r *http.Request, u store.User) {
	if _, err := s.store.ProviderByUser(r.Context(), u.ID); errors.Is(err, store.ErrNotFound) {
		s.render(w, r, http.StatusOK, views.AppIntro())
		return
	}
	s.appNewListing(w, r, u)
}

// Управление опубликованным ----------------------------------------------------

func (s *Server) appManage(w http.ResponseWriter, r *http.Request, u store.User) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	s.renderManage(w, r, u, s.listingID(r), days, "", "")
}

func (s *Server) renderManage(w http.ResponseWriter, r *http.Request, u store.User, id int64, days int, notice, errText string) {
	ctx := r.Context()
	p, err := s.store.ProviderByUser(ctx, u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	l, err := s.store.GetListing(ctx, id, p.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if l.Status != store.ListingPublished && l.Status != store.ListingPaused {
		s.renderEditor(w, r, u, p, id, nil, errText, notice)
		return
	}
	d := views.AppManageData{Listing: l, Days: days, ShareURL: s.shareURL(l.ID), Notice: notice, Error: errText}
	if len(l.Photos) > 0 {
		d.PhotoURL = s.photoLink(l.Photos[0], time.Now())
	}
	d.Stats7, _ = s.store.ListingStats(ctx, p.ID, l.ID, 7)
	d.Stats30, _ = s.store.ListingStats(ctx, p.ID, l.ID, 30)
	if _, err := s.store.PendingRevision(ctx, l.ID); err == nil {
		d.Revision = true
	}
	s.render(w, r, http.StatusOK, views.AppManage(d))
}

func (s *Server) appManageAction(w http.ResponseWriter, r *http.Request, u store.User) {
	ctx := r.Context()
	p, err := s.store.ProviderByUser(ctx, u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	id := s.listingID(r)
	var notice string
	switch r.PathValue("action") {
	case "pause":
		err, notice = s.store.PauseListing(ctx, id, p.ID), "Объявление на паузе — в каталоге его не видно."
	case "resume":
		err, notice = s.store.ResumeListing(ctx, id, p.ID), "Объявление снова в каталоге."
	case "actualize":
		err, notice = s.store.ActualizeListing(ctx, id, p.ID), "Спасибо! Отметили, что объявление актуально."
	case "delete":
		if err := s.store.ArchiveListing(ctx, id, p.ID); err != nil {
			s.fail(w, r, err)
			return
		}
		_ = s.store.LogEvent(ctx, "listing_deleted", u.ID, "listing", id, "miniapp", nil)
		s.renderMy(w, r, u, "Объявление удалено.", "")
		return
	default:
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		s.renderManage(w, r, u, id, 7, "", "Сейчас это действие недоступно — обновите экран.")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.renderManage(w, r, u, id, 7, notice, "")
}
