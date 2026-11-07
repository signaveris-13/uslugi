package botapp

import (
	"errors"
	"fmt"
	"html"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-telegram/bot/models"

	"github.com/signaveris-13/uslugi/internal/store"
)

// Шаги формы объявления (FR-P-01) в порядке прохождения.
const (
	stepCategory     = "category"
	stepSubcategory  = "subcategory"
	stepExtras       = "extras"
	stepCities       = "cities"
	stepTitle        = "title"
	stepDescription  = "description"
	stepPriceType    = "price_type"
	stepPriceAmount  = "price_amount"
	stepCurrency     = "currency"
	stepPhotos       = "photos"
	stepContacts     = "contacts"
	stepContactInput = "contact_input"
	stepLanguages    = "languages"
	stepSchedule     = "schedule"
	stepPreview      = "preview"
)

// Шаги, на которых бот ждёт текст.
func isTextStep(step string) bool {
	switch step {
	case stepTitle, stepDescription, stepPriceAmount, stepContactInput, stepSchedule:
		return true
	}
	return false
}

// form — состояние одного шага: объявление, исполнитель, сохранённый шаг.
type form struct {
	*turn
	st       store.BotState
	listing  store.Listing
	provider store.Provider
}

// startNewListing — «Разместить объявление»: черновик, лимит, первый шаг.
func (a *App) startNewListing(t *turn) {
	if t.user.CityID == nil {
		a.askCity(t, "Сначала выбери город:")
		return
	}
	p, err := a.store.EnsureProvider(t.ctx, t.user, t.tg.Name())
	if err != nil {
		a.fail(t, err)
		return
	}
	if p.IsBlocked {
		a.show(t, "Размещение объявлений для этого аккаунта недоступно.", nil)
		return
	}

	// Незаконченный черновик — продолжаем его, а не плодим новые.
	if id, _, err := a.store.ProviderDraft(t.ctx, p.ID); err == nil {
		a.show(t, "У тебя есть незаконченный черновик. Продолжим его?", keyboard{
			row(btn("✏️ Продолжить черновик", fmt.Sprintf("f:open:%d", id))),
			row(btn("🗑 Удалить и начать заново", fmt.Sprintf("f:restart:%d", id))),
			row(btn("← В меню", "m:menu")),
		})
		return
	} else if !isNotFound(err) {
		a.fail(t, err)
		return
	}

	active, err := a.store.CountActiveListings(t.ctx, p.ID)
	if err != nil {
		a.fail(t, err)
		return
	}
	if limit := a.store.ProviderListingLimit(t.ctx, p); active >= limit {
		a.show(t, fmt.Sprintf("Сейчас можно держать до %d объявлений, и у тебя их уже %d. Сними одно с публикации, чтобы добавить новое.", limit, active), keyboard{
			row(btn("📋 Мои объявления", "m:mine")),
		})
		return
	}

	id, err := a.store.CreateListingDraft(t.ctx, p.ID, store.ListingDraftInput{
		CityID:   *t.user.CityID,
		Telegram: t.tg.Username,
		Currency: a.store.CountryCurrencyByCity(t.ctx, *t.user.CityID),
	})
	if err != nil {
		a.fail(t, err)
		return
	}
	a.goTo(t, id, stepCategory, false)
}

// goTo сохраняет шаг и показывает его.
func (a *App) goTo(t *turn, listingID int64, step string, returnToPreview bool) {
	a.goToField(t, listingID, step, "", returnToPreview)
}

func (a *App) goToField(t *turn, listingID int64, step, field string, returnToPreview bool) {
	st := store.BotState{Step: step, ListingID: &listingID, Field: field, ReturnToPreview: returnToPreview}
	if err := a.store.SetBotState(t.ctx, t.user.ID, st); err != nil {
		a.fail(t, err)
		return
	}
	if step != stepPreview && step != stepContactInput {
		if err := a.store.SetDraftStep(t.ctx, listingID, step); err != nil {
			a.log.Warn("draft_step", "err", err)
		}
	}
	f, ok := a.loadForm(t, st)
	if !ok {
		return
	}
	a.render(f)
}

// loadForm подгружает объявление из состояния и проверяет, что его можно править.
func (a *App) loadForm(t *turn, st store.BotState) (*form, bool) {
	if st.ListingID == nil {
		a.showMenu(t, "")
		return nil, false
	}
	p, err := a.store.ProviderByUser(t.ctx, t.user.ID)
	if err != nil {
		a.fail(t, err)
		return nil, false
	}
	l, err := a.store.GetListing(t.ctx, *st.ListingID, p.ID)
	if isNotFound(err) || errors.Is(err, store.ErrNotOwner) {
		a.showMenu(t, "Это объявление больше недоступно.")
		return nil, false
	}
	if err != nil {
		a.fail(t, err)
		return nil, false
	}
	return &form{turn: t, st: st, listing: l, provider: p}, true
}

// next — какой шаг после текущего. Из «Исправить» после группы шагов возвращаемся к превью.
func next(f *form, from string) string {
	l := f.listing
	n := map[string]string{
		stepCategory:    stepSubcategory,
		stepSubcategory: stepExtras,
		stepExtras:      stepCities,
		stepCities:      stepTitle,
		stepTitle:       stepDescription,
		stepDescription: stepPriceType,
		stepPriceType:   stepPriceAmount,
		stepPriceAmount: stepCurrency,
		stepCurrency:    stepPhotos,
		stepPhotos:      stepContacts,
		stepContacts:    stepLanguages,
		stepLanguages:   stepSchedule,
		stepSchedule:    stepPreview,
	}[from]
	if from == stepPriceType && l.PriceType != nil && *l.PriceType == "negotiable" {
		n = stepPhotos
	}
	groupEnd := map[string]bool{
		stepExtras: true, stepCities: true, stepTitle: true, stepDescription: true, stepCurrency: true,
		stepPhotos: true, stepContacts: true, stepLanguages: true, stepSchedule: true,
	}[from] || (from == stepPriceType && n == stepPhotos)
	if f.st.ReturnToPreview && groupEnd {
		return stepPreview
	}
	return n
}

// prev — куда ведёт «Назад».
func prev(f *form, from string) string {
	if f.st.ReturnToPreview {
		return stepPreview
	}
	switch from {
	case stepSubcategory:
		return stepCategory
	case stepExtras:
		return stepSubcategory
	case stepCities:
		return stepExtras
	case stepTitle:
		return stepCities
	case stepDescription:
		return stepTitle
	case stepPriceType:
		return stepDescription
	case stepPriceAmount:
		return stepPriceType
	case stepCurrency:
		return stepPriceAmount
	case stepPhotos:
		if f.listing.PriceType != nil && *f.listing.PriceType == "negotiable" {
			return stepPriceType
		}
		return stepCurrency
	case stepContacts:
		return stepPhotos
	case stepContactInput:
		return stepContacts
	case stepLanguages:
		return stepContacts
	case stepSchedule:
		return stepLanguages
	}
	return stepCategory
}

func (a *App) advance(f *form, from string) {
	a.goTo(f.turn, f.listing.ID, next(f, from), f.st.ReturnToPreview)
}

func backRow(step string) []models.InlineKeyboardButton {
	if step == stepCategory {
		return row(btn("← В меню", "m:menu"))
	}
	return row(btn("← Назад", "f:back"))
}

// Кнопки --------------------------------------------------------------------

func (a *App) onForm(t *turn, data string) {
	action, arg, _ := strings.Cut(data, ":")

	// Действия, которые сами выбирают объявление.
	switch action {
	case "open":
		a.openListing(t, parseID(arg))
		return
	case "restart":
		if p, err := a.store.ProviderByUser(t.ctx, t.user.ID); err == nil {
			if err := a.store.DeleteDraft(t.ctx, parseID(arg), p.ID); err != nil && !isNotFound(err) {
				a.fail(t, err)
				return
			}
		}
		a.startNewListing(t)
		return
	}

	st, err := a.store.GetBotState(t.ctx, t.user.ID)
	if err != nil {
		a.fail(t, err)
		return
	}
	f, ok := a.loadForm(t, st)
	if !ok {
		return
	}
	if !store.EditableStatus(f.listing.Status) {
		a.openListing(t, f.listing.ID)
		return
	}
	id := f.listing.ID
	ctx := t.ctx

	switch action {
	case "back":
		a.goTo(t, id, prev(f, st.Step), false)
	case "skip":
		a.advance(f, st.Step)

	case "cat":
		a.goToField(t, id, stepSubcategory, arg, st.ReturnToPreview)
	case "sub":
		if err := a.store.SetPrimaryCategory(ctx, id, parseID(arg)); err != nil {
			a.fail(t, err)
			return
		}
		a.goTo(t, id, stepExtras, st.ReturnToPreview)
	case "extra":
		limitHit, err := a.store.ToggleExtraCategory(ctx, id, parseID(arg))
		if err != nil {
			a.fail(t, err)
			return
		}
		f.reload(a)
		a.renderExtras(f, limitHit)
	case "extras_done":
		a.advance(f, stepExtras)

	case "city":
		if err := a.store.ToggleListingCity(ctx, id, parseID(arg)); err != nil {
			a.fail(t, err)
			return
		}
		f.reload(a)
		a.renderCities(f, "")
	case "travel":
		if err := a.store.ToggleTravels(ctx, id); err != nil {
			a.fail(t, err)
			return
		}
		f.reload(a)
		a.renderCities(f, "")
	case "cities_done":
		if len(f.listing.Cities) == 0 {
			a.renderCities(f, "Выбери хотя бы один город.")
			return
		}
		a.advance(f, stepCities)

	case "ptype":
		if err := a.store.SetPriceType(ctx, id, arg); err != nil {
			a.fail(t, err)
			return
		}
		f.reload(a)
		a.advance(f, stepPriceType)
	case "cur":
		if arg != "VND" && arg != "USD" {
			return
		}
		if err := a.store.SetCurrency(ctx, id, arg); err != nil {
			a.fail(t, err)
			return
		}
		a.advance(f, stepCurrency)

	case "photos_done":
		a.advance(f, stepPhotos)
	case "photos_clear":
		if err := a.store.ClearListingPhotos(ctx, id); err != nil {
			a.fail(t, err)
			return
		}
		f.reload(a)
		a.render(f)

	case "contact":
		if arg == "telegram" {
			// Telegram берём из профиля: кнопка включает или убирает его.
			value := ""
			if f.listing.ContactTelegram == nil {
				value = t.tg.Username
			}
			if err := a.store.SetListingText(ctx, id, "telegram", value); err != nil {
				a.fail(t, err)
				return
			}
			f.reload(a)
			a.render(f)
			return
		}
		a.goToField(t, id, stepContactInput, arg, st.ReturnToPreview)
	case "contact_clear":
		if err := a.store.SetListingText(ctx, id, st.Field, ""); err != nil {
			a.fail(t, err)
			return
		}
		a.goTo(t, id, stepContacts, st.ReturnToPreview)
	case "contacts_done":
		if f.listing.ContactTelegram == nil && f.listing.ContactPhone == nil && f.listing.ContactWhatsApp == nil {
			a.renderContacts(f, "Нужен хотя бы один способ связи: Telegram, WhatsApp или телефон.")
			return
		}
		a.advance(f, stepContacts)

	case "lang":
		if err := a.store.ToggleLanguage(ctx, id, arg); err != nil {
			a.fail(t, err)
			return
		}
		f.reload(a)
		a.renderLanguages(f, "")
	case "langs_done":
		if len(f.listing.Languages) == 0 {
			a.renderLanguages(f, "Выбери хотя бы один язык.")
			return
		}
		a.advance(f, stepLanguages)

	case "edit":
		a.renderEditMenu(f)
	case "goto":
		a.goTo(t, id, arg, true)
	case "preview":
		a.goTo(t, id, stepPreview, false)
	case "submit":
		a.onSubmit(f, false)
	case "terms_ok":
		a.onSubmit(f, true)
	case "delete":
		a.show(t, "Удалить черновик? Это нельзя отменить.", keyboard{
			row(btn("🗑 Да, удалить", "f:delete_yes"), btn("Отмена", "f:preview")),
		})
	case "delete_yes":
		if err := a.store.DeleteDraft(ctx, id, f.provider.ID); err != nil && !isNotFound(err) {
			a.fail(t, err)
			return
		}
		_ = a.store.SetBotState(ctx, t.user.ID, store.BotState{})
		a.showMenu(t, "Черновик удалён.")
	default:
		a.log.Warn("неизвестное действие формы", "data", data)
	}
}

func (f *form) reload(a *App) {
	if l, err := a.store.GetListing(f.ctx, f.listing.ID, f.provider.ID); err == nil {
		f.listing = l
	}
}

// Текст и фото ---------------------------------------------------------------

func (a *App) onFormText(t *turn, st store.BotState, text string) {
	f, ok := a.loadForm(t, st)
	if !ok {
		return
	}
	if !store.EditableStatus(f.listing.Status) {
		a.openListing(t, f.listing.ID)
		return
	}
	id := f.listing.ID
	switch st.Step {
	case stepTitle:
		if n := utf8.RuneCountInString(text); n > store.MaxTitleLen {
			a.send(t, fmt.Sprintf("Название длиннее %d символов (сейчас %d). Сократи, пожалуйста.", store.MaxTitleLen, n), keyboard{backRow(stepTitle)})
			return
		}
		a.saveText(f, "title", text, stepTitle)
	case stepDescription:
		if n := utf8.RuneCountInString(text); n > store.MaxDescLen {
			a.send(t, fmt.Sprintf("Описание длиннее %d символов (сейчас %d). Сократи, пожалуйста.", store.MaxDescLen, n), keyboard{backRow(stepDescription)})
			return
		}
		a.saveText(f, "description", text, stepDescription)
	case stepSchedule:
		if utf8.RuneCountInString(text) > 200 {
			a.send(t, "График лучше уложить в 200 символов.", keyboard{backRow(stepSchedule)})
			return
		}
		a.saveText(f, "schedule", text, stepSchedule)
	case stepPriceAmount:
		amount, ok := parseAmount(text)
		if !ok {
			a.send(t, "Не понял сумму. Напиши числом, например <code>450000</code> или <code>450к</code>.", keyboard{backRow(stepPriceAmount)})
			return
		}
		if err := a.store.SetPriceAmount(t.ctx, id, amount); err != nil {
			a.fail(t, err)
			return
		}
		a.advance(f, stepPriceAmount)
	case stepContactInput:
		if utf8.RuneCountInString(text) > 100 {
			a.send(t, "Слишком длинно для контакта.", keyboard{backRow(stepContactInput)})
			return
		}
		if err := a.store.SetListingText(t.ctx, id, st.Field, normalizeContact(st.Field, text)); err != nil {
			a.fail(t, err)
			return
		}
		a.goTo(t, id, stepContacts, st.ReturnToPreview)
	}
}

func (a *App) saveText(f *form, field, value, step string) {
	if err := a.store.SetListingText(f.ctx, f.listing.ID, field, value); err != nil {
		a.fail(f.turn, err)
		return
	}
	a.advance(f, step)
}

func (a *App) onPhoto(t *turn, st store.BotState, msg *models.Message) {
	if st.Step != stepPhotos || st.ListingID == nil {
		a.send(t, "Фото можно добавить на шаге «Фото» в форме объявления.", keyboard{row(btn("← В меню", "m:menu"))})
		return
	}
	f, ok := a.loadForm(t, st)
	if !ok || !store.EditableStatus(f.listing.Status) {
		return
	}
	best := msg.Photo[len(msg.Photo)-1] // Telegram присылает размеры по возрастанию
	n, added, err := a.store.AddListingPhoto(t.ctx, f.listing.ID, best.FileID)
	if err != nil {
		a.fail(t, err)
		return
	}
	f.reload(a)
	text := fmt.Sprintf("Фото добавлено, всего %d. Пришли ещё или нажми «Готово».", n)
	if !added {
		text = fmt.Sprintf("Уже %d фото — это максимум.", store.MaxPhotos)
	}
	group := msg.MediaGroupID
	if group != "" {
		if id, ok := a.albumReplyID(group); ok {
			t.editID = id
			a.show(t, text, photosKeyboard(f))
			return
		}
	}
	id := a.send(t, text, photosKeyboard(f))
	if group != "" {
		a.rememberAlbumReply(group, id)
	}
}

func (a *App) albumReplyID(group string) (int, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.albums[group]
	return r.msgID, ok && r.msgID != 0
}

func (a *App) rememberAlbumReply(group string, msgID int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := timeNow()
	// Не даём карте расти бесконечно: альбомы старше 10 минут больше не придут.
	for k, v := range a.albums {
		if now.Sub(v.at) > 10*timeMinute {
			delete(a.albums, k)
		}
	}
	a.albums[group] = albumReply{at: now, msgID: msgID}
}

// Превью и отправка ----------------------------------------------------------

// openListing — из «Мои объявления» или по кнопке «Продолжить».
func (a *App) openListing(t *turn, listingID int64) {
	p, err := a.store.ProviderByUser(t.ctx, t.user.ID)
	if err != nil {
		a.fail(t, err)
		return
	}
	l, err := a.store.GetListing(t.ctx, listingID, p.ID)
	if err != nil {
		a.showMenu(t, "Это объявление больше недоступно.")
		return
	}
	if l.Status == store.ListingDraft && l.DraftStep != nil && *l.DraftStep != "" {
		a.goTo(t, l.ID, *l.DraftStep, false)
		return
	}
	if store.EditableStatus(l.Status) {
		a.goTo(t, l.ID, stepPreview, false)
		return
	}
	// Остальные статусы: только просмотр, править будем через модерацию правок.
	_ = a.store.SetBotState(t.ctx, t.user.ID, store.BotState{})
	a.sendCard(t, l, fmt.Sprintf("Статус: <b>%s</b>", store.ListingStatusLabels[l.Status]), keyboard{
		row(btn("📋 Мои объявления", "m:mine")),
		row(btn("← В меню", "m:menu")),
	})
}

func (a *App) onSubmit(f *form, termsAccepted bool) {
	if termsAccepted {
		if err := a.store.AcceptTerms(f.ctx, f.provider.ID); err != nil {
			a.fail(f.turn, err)
			return
		}
		f.provider, _ = a.store.ProviderByUser(f.ctx, f.user.ID)
	}
	if miss := f.listing.MissingFields(); len(miss) > 0 {
		a.renderPreview(f)
		return
	}
	// FR-P-04: перед первой отправкой — правила площадки.
	if f.provider.TermsAcceptedAt == nil {
		terms := a.store.BotText(f.ctx, "terms", "Правила площадки: Uslugi — площадка, а не сторона сделки.")
		a.show(f.turn, html.EscapeString(terms), keyboard{
			row(btn("✅ Принимаю и отправляю", "f:terms_ok")),
			row(btn("← Назад", "f:preview")),
		})
		return
	}
	err := a.store.SubmitListing(f.ctx, f.listing.ID, f.provider.ID)
	var inc store.ErrIncomplete
	if errors.As(err, &inc) {
		a.renderPreview(f)
		return
	}
	if err != nil {
		a.fail(f.turn, err)
		return
	}
	_ = a.store.SetBotState(f.ctx, f.user.ID, store.BotState{})
	a.event(f.turn, "listing_submitted", "listing", f.listing.ID, nil)
	a.show(f.turn, "Готово! Объявление отправлено на модерацию. Обычно проверяем в течение 12 часов в рабочие дни — пришлём сообщение, когда опубликуем.", keyboard{
		row(btn("📋 Мои объявления", "m:mine")),
		row(btn("← В меню", "m:menu")),
	})
}

// startFromInvite — исполнитель пришёл по ссылке из CRM (FR-P-10):
// связываем лид, готовим черновик по данным лида и показываем его.
func (a *App) startFromInvite(t *turn, token string) {
	lead, err := a.store.LeadByInviteToken(t.ctx, token)
	if isNotFound(err) {
		a.log.Info("приглашение не найдено", "token", token)
		if t.user.CityID == nil {
			a.askCity(t, "Привет! Это Uslugi — каталог мастеров и услуг рядом с тобой.\n\nВ каком ты городе?")
			return
		}
		a.showMenu(t, "")
		return
	}
	if err != nil {
		a.fail(t, err)
		return
	}
	if t.user.CityID == nil && lead.CityID != nil {
		if err := a.store.SetUserCity(t.ctx, t.user.ID, *lead.CityID); err != nil {
			a.fail(t, err)
			return
		}
		t.user.CityID = lead.CityID
	}
	if t.user.CityID == nil {
		a.askCity(t, "Привет! Сначала выбери город, а потом заполним объявление:")
		return
	}

	name := t.tg.Name()
	if lead.Name != nil {
		name = *lead.Name
	}
	p, err := a.store.EnsureProvider(t.ctx, t.user, name)
	if err != nil {
		a.fail(t, err)
		return
	}
	if err := a.store.AttachLeadToProvider(t.ctx, lead.ID, p.ID, t.tg.ID); err != nil {
		a.fail(t, err)
		return
	}
	a.event(t, "invite_opened", "lead", lead.ID, nil)

	// Уже есть черновик — продолжаем его.
	if id, _, err := a.store.ProviderDraft(t.ctx, p.ID); err == nil {
		if a.launcher() {
			a.sendLauncher(t, "С возвращением! Черновик объявления ждёт тебя в приложении.", "Продолжить объявление", "e_"+strconv.FormatInt(id, 10))
			return
		}
		a.send(t, "С возвращением! Продолжим твоё объявление.", nil)
		a.goTo(t, id, stepPreview, false)
		return
	}

	in := store.ListingDraftInput{
		CityID:    *t.user.CityID,
		Telegram:  t.tg.Username,
		Currency:  a.store.CountryCurrencyByCity(t.ctx, *t.user.CityID),
		Phone:     deref(lead.Phone),
		WhatsApp:  deref(lead.WhatsApp),
		Instagram: deref(lead.Instagram),
	}
	if lead.CityID != nil {
		in.CityID = *lead.CityID
	}
	if lead.CategoryID != nil {
		in.CategoryID = *lead.CategoryID
		in.Title = deref(lead.CategoryName)
	}
	in.Description = deref(lead.Summary)

	id, err := a.store.CreateListingDraft(t.ctx, p.ID, in)
	if err != nil {
		a.fail(t, err)
		return
	}
	if err := a.store.SetLeadListing(t.ctx, lead.ID, id); err != nil {
		a.log.Warn("лид → объявление", "err", err)
	}
	const hello = "Привет! Это Uslugi. Мы подготовили черновик твоего объявления — проверь, дополни то, чего не хватает, и отправь на модерацию. Размещение бесплатное."
	if a.launcher() {
		a.sendLauncher(t, hello, "Открыть черновик", "e_"+strconv.FormatInt(id, 10))
		return
	}
	a.send(t, hello, nil)
	a.goTo(t, id, stepPreview, false)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
