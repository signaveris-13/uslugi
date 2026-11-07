package botapp

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/signaveris-13/uslugi/internal/store"
)

var (
	timeNow    = time.Now
	timeMinute = time.Minute
)

// render показывает текущий шаг формы.
func (a *App) render(f *form) {
	l := f.listing
	switch f.st.Step {
	case stepCategory:
		cats, err := a.store.ListParentCategories(f.ctx, *f.user.CityID)
		if err != nil {
			a.fail(f.turn, err)
			return
		}
		var kb keyboard
		for i := 0; i < len(cats); i += 2 {
			r := row(btn(cats[i].Emoji+" "+cats[i].Name, fmt.Sprintf("f:cat:%d", cats[i].ID)))
			if i+1 < len(cats) {
				r = append(r, btn(cats[i+1].Emoji+" "+cats[i+1].Name, fmt.Sprintf("f:cat:%d", cats[i+1].ID)))
			}
			kb = append(kb, r)
		}
		kb = append(kb, backRow(stepCategory))
		a.show(f.turn, stepTitleText(1, "Категория")+"Что ты предлагаешь? Выбери категорию:", kb)

	case stepSubcategory:
		parentID := parseID(f.st.Field)
		if parentID == 0 && l.PrimaryCategory != nil {
			parentID, _ = a.store.CategoryParentID(f.ctx, l.PrimaryCategory.ID)
		}
		if parentID == 0 {
			a.goTo(f.turn, l.ID, stepCategory, f.st.ReturnToPreview)
			return
		}
		subs, err := a.store.ListChildCategories(f.ctx, parentID)
		if err != nil {
			a.fail(f.turn, err)
			return
		}
		var kb keyboard
		for _, s := range subs {
			label := s.Name
			if l.PrimaryCategory != nil && l.PrimaryCategory.ID == s.ID {
				label = "✅ " + label
			}
			kb = append(kb, row(btn(label, fmt.Sprintf("f:sub:%d", s.ID))))
		}
		kb = append(kb, row(btn("← Другая категория", "f:back")))
		a.show(f.turn, stepTitleText(1, "Категория")+"Уточни — какая услуга основная?", kb)

	case stepExtras:
		a.renderExtras(f, false)

	case stepCities:
		a.renderCities(f, "")

	case stepTitle:
		a.askText(f, 3, "Название",
			fmt.Sprintf("Как назовём объявление? До %d символов.\nНапример: <i>Маникюр с покрытием, выезд на дом</i>.", store.MaxTitleLen), l.Title)

	case stepDescription:
		a.askText(f, 4, "Описание",
			fmt.Sprintf("Расскажи об услугах: что делаешь, опыт, материалы, где принимаешь. До %d символов.", store.MaxDescLen), l.Description)

	case stepPriceType:
		mark := func(v, label string) string {
			if l.PriceType != nil && *l.PriceType == v {
				return "✅ " + label
			}
			return label
		}
		a.show(f.turn, stepTitleText(5, "Цена")+"Как указать цену?", keyboard{
			row(btn(mark("fixed", "Точная цена"), "f:ptype:fixed"), btn(mark("from", "«От …»"), "f:ptype:from")),
			row(btn(mark("negotiable", "По договорённости"), "f:ptype:negotiable")),
			backRow(stepPriceType),
		})

	case stepPriceAmount:
		var current *string
		if l.PriceAmount != nil {
			s := formatAmount(*l.PriceAmount, deref(l.Currency))
			current = &s
		}
		a.askText(f, 5, "Цена", "Сколько стоит? Напиши числом, например <code>450000</code> или <code>450к</code>.", current)

	case stepCurrency:
		mark := func(v, label string) string {
			if l.Currency != nil && *l.Currency == v {
				return "✅ " + label
			}
			return label
		}
		a.show(f.turn, stepTitleText(5, "Цена")+"В какой валюте?", keyboard{
			row(btn(mark("VND", "₫ Донги"), "f:cur:VND"), btn(mark("USD", "$ Доллары"), "f:cur:USD")),
			backRow(stepCurrency),
		})

	case stepPhotos:
		text := stepTitleText(6, "Фото") + fmt.Sprintf("Пришли до %d фото работ — с ними объявление смотрят чаще. Можно пропустить.", store.MaxPhotos)
		if n := len(l.PhotoFileIDs); n > 0 {
			text += fmt.Sprintf("\n\nСейчас фото: %d.", n)
		}
		a.show(f.turn, text, photosKeyboard(f))

	case stepContacts:
		a.renderContacts(f, "")

	case stepContactInput:
		prompts := map[string]string{
			"whatsapp":  "Пришли номер WhatsApp в международном формате, например <code>+84 905 123 456</code>.",
			"phone":     "Пришли номер телефона в международном формате, например <code>+84 905 123 456</code>.",
			"instagram": "Пришли Instagram: ник или ссылку на профиль.",
		}
		kb := keyboard{}
		if v := contactValue(l, f.st.Field); v != "" {
			kb = append(kb, row(btn("Убрать "+v, "f:contact_clear")))
		}
		kb = append(kb, row(btn("← Назад", "f:back")))
		a.show(f.turn, stepTitleText(7, "Контакты")+prompts[f.st.Field], kb)

	case stepLanguages:
		a.renderLanguages(f, "")

	case stepSchedule:
		a.askText(f, 9, "График", "Когда принимаешь? Например: <i>пн–сб, 10:00–19:00</i>. Можно пропустить.", l.Schedule)

	case stepPreview:
		a.renderPreview(f)

	default:
		a.goTo(f.turn, l.ID, stepCategory, false)
	}
}

const totalSteps = 9

func stepTitleText(n int, name string) string {
	return fmt.Sprintf("<b>Шаг %d из %d · %s</b>\n\n", n, totalSteps, name)
}

// askText — шаг, где ждём текст. Если значение уже есть, его можно оставить.
func (a *App) askText(f *form, n int, name, prompt string, current *string) {
	text := stepTitleText(n, name) + prompt
	var kb keyboard
	if current != nil && *current != "" {
		text += "\n\nСейчас: <i>" + html.EscapeString(truncate(*current, 300)) + "</i>"
		kb = append(kb, row(btn("Оставить как есть", "f:skip")))
	} else if f.st.Step == stepSchedule {
		kb = append(kb, row(btn("Пропустить", "f:skip")))
	}
	kb = append(kb, backRow(f.st.Step))
	a.show(f.turn, text, kb)
}

func (a *App) renderExtras(f *form, limitHit bool) {
	l := f.listing
	if l.PrimaryCategory == nil {
		a.goTo(f.turn, l.ID, stepCategory, f.st.ReturnToPreview)
		return
	}
	parentID, err := a.store.CategoryParentID(f.ctx, l.PrimaryCategory.ID)
	if err != nil {
		a.fail(f.turn, err)
		return
	}
	subs, err := a.store.ListChildCategories(f.ctx, parentID)
	if err != nil {
		a.fail(f.turn, err)
		return
	}
	chosen := map[int64]bool{}
	for _, c := range l.ExtraCategories {
		chosen[c.ID] = true
	}
	var kb keyboard
	for _, s := range subs {
		if s.ID == l.PrimaryCategory.ID {
			continue
		}
		label := s.Name
		if chosen[s.ID] {
			label = "✅ " + label
		}
		kb = append(kb, row(btn(label, fmt.Sprintf("f:extra:%d", s.ID))))
	}
	done := "Пропустить"
	if len(l.ExtraCategories) > 0 {
		done = "Готово"
	}
	kb = append(kb, row(btn(done+" →", "f:extras_done")), backRow(stepExtras))
	text := stepTitleText(1, "Категория") + fmt.Sprintf("Основная: <b>%s</b>.\nЕсть ещё услуги в этой категории? Можно отметить до %d.",
		html.EscapeString(l.PrimaryCategory.Name), store.MaxExtraCats)
	if limitHit {
		text += fmt.Sprintf("\n\n⚠️ Дополнительных — не больше %d. Сними одну, чтобы выбрать другую.", store.MaxExtraCats)
	}
	if len(subs) <= 1 {
		a.advance(f, stepExtras)
		return
	}
	a.show(f.turn, text, kb)
}

func (a *App) renderCities(f *form, warn string) {
	l := f.listing
	cities, err := a.store.ListActiveCitiesInCountryOf(f.ctx, *f.user.CityID)
	if err != nil {
		a.fail(f.turn, err)
		return
	}
	chosen := map[int64]bool{}
	for _, c := range l.Cities {
		chosen[c.ID] = true
	}
	var kb keyboard
	for _, c := range cities {
		label := c.Name
		if chosen[c.ID] {
			label = "✅ " + label
		}
		kb = append(kb, row(btn(label, fmt.Sprintf("f:city:%d", c.ID))))
	}
	travel := "🚗 Выезжаю к клиенту"
	if l.TravelsToClient {
		travel = "✅ " + travel
	}
	kb = append(kb, row(btn(travel, "f:travel")), row(btn("Готово →", "f:cities_done")), backRow(stepCities))
	text := stepTitleText(2, "Где работаешь") + "Отметь города. Если выезжаешь к клиентам — отметь и это."
	if warn != "" {
		text += "\n\n⚠️ " + warn
	}
	a.show(f.turn, text, kb)
}

func photosKeyboard(f *form) keyboard {
	n := len(f.listing.PhotoFileIDs)
	var kb keyboard
	if n > 0 {
		kb = append(kb, row(btn(fmt.Sprintf("Готово, фото: %d →", n), "f:photos_done")))
		kb = append(kb, row(btn("Удалить все фото", "f:photos_clear")))
	} else {
		kb = append(kb, row(btn("Пропустить →", "f:photos_done")))
	}
	return append(kb, backRow(stepPhotos))
}

func contactValue(l store.Listing, field string) string {
	switch field {
	case "telegram":
		if l.ContactTelegram != nil {
			return "@" + *l.ContactTelegram
		}
	case "whatsapp":
		return deref(l.ContactWhatsApp)
	case "phone":
		return deref(l.ContactPhone)
	case "instagram":
		return deref(l.ContactInstagram)
	}
	return ""
}

func (a *App) renderContacts(f *form, warn string) {
	l := f.listing
	label := func(field, name string) string {
		if v := contactValue(l, field); v != "" {
			return "✅ " + name + ": " + v
		}
		return "+ " + name
	}
	var kb keyboard
	if f.tg.Username != "" {
		kb = append(kb, row(btn(label("telegram", "Telegram"), "f:contact:telegram")))
	}
	kb = append(kb,
		row(btn(label("whatsapp", "WhatsApp"), "f:contact:whatsapp")),
		row(btn(label("phone", "Телефон"), "f:contact:phone")),
		row(btn(label("instagram", "Instagram"), "f:contact:instagram")),
		row(btn("Готово →", "f:contacts_done")),
		backRow(stepContacts),
	)
	text := stepTitleText(7, "Контакты") + "Как с тобой связаться? Кнопка «Написать» в карточке ведёт в Telegram."
	if f.tg.Username == "" {
		text += "\n\nУ твоего аккаунта нет username в Telegram, поэтому клиентам нужен WhatsApp или телефон."
	}
	if warn != "" {
		text += "\n\n⚠️ " + warn
	}
	a.show(f.turn, text, kb)
}

func (a *App) renderLanguages(f *form, warn string) {
	chosen := map[string]bool{}
	for _, c := range f.listing.Languages {
		chosen[c] = true
	}
	var kb keyboard
	var r []models.InlineKeyboardButton
	for _, code := range store.LanguageCodes {
		label := store.LanguageLabels[code]
		if chosen[code] {
			label = "✅ " + label
		}
		r = append(r, btn(label, "f:lang:"+code))
		if len(r) == 2 {
			kb = append(kb, r)
			r = nil
		}
	}
	if len(r) > 0 {
		kb = append(kb, r)
	}
	kb = append(kb, row(btn("Готово →", "f:langs_done")), backRow(stepLanguages))
	text := stepTitleText(8, "Языки") + "На каких языках обслуживаешь?"
	if warn != "" {
		text += "\n\n⚠️ " + warn
	}
	a.show(f.turn, text, kb)
}

// renderPreview — карточка как её увидит клиент (FR-P-03) и что осталось заполнить.
func (a *App) renderPreview(f *form) {
	l := f.listing
	var head []string
	if l.Status == store.ListingRejected {
		head = append(head, "⚠️ <b>Объявление отклонено</b>")
		if l.RejectionReason != nil {
			head = append(head, "Причина: "+html.EscapeString(*l.RejectionReason))
		}
		head = append(head, "Исправь и отправь снова.")
	}
	footer := "<b>Так клиенты увидят объявление.</b>"
	miss := l.MissingFields()
	if len(miss) > 0 {
		footer += "\n\n⚠️ Осталось заполнить: " + strings.Join(miss, ", ") + "."
	}
	if len(head) > 0 {
		footer = strings.Join(head, "\n") + "\n\n" + footer
	}
	var kb keyboard
	if len(miss) == 0 {
		kb = append(kb, row(btn("✅ Отправить на модерацию", "f:submit")))
	}
	kb = append(kb, row(btn("✏️ Исправить", "f:edit")))
	if l.Status == store.ListingDraft {
		kb = append(kb, row(btn("🗑 Удалить черновик", "f:delete")))
	}
	kb = append(kb, row(btn("← В меню", "m:menu")))
	a.sendCard(f.turn, l, footer, kb)
}

func (a *App) renderEditMenu(f *form) {
	a.show(f.turn, "Что исправить?", keyboard{
		row(btn("Категория", "f:goto:"+stepCategory), btn("Города", "f:goto:"+stepCities)),
		row(btn("Название", "f:goto:"+stepTitle), btn("Описание", "f:goto:"+stepDescription)),
		row(btn("Цена", "f:goto:"+stepPriceType), btn("Фото", "f:goto:"+stepPhotos)),
		row(btn("Контакты", "f:goto:"+stepContacts), btn("Языки", "f:goto:"+stepLanguages)),
		row(btn("График", "f:goto:"+stepSchedule)),
		row(btn("← К объявлению", "f:preview")),
	})
}

// sendCard показывает карточку: альбом фото (если есть) и текст с кнопками.
func (a *App) sendCard(t *turn, l store.Listing, footer string, kb keyboard) {
	text := cardText(l) + "\n\n" + footer
	if len(l.PhotoFileIDs) == 0 {
		a.show(t, text, kb)
		return
	}
	media := make([]models.InputMedia, 0, len(l.PhotoFileIDs))
	for _, id := range l.PhotoFileIDs {
		media = append(media, &models.InputMediaPhoto{Media: id})
	}
	if len(media) == 1 {
		if _, err := t.b.SendPhoto(t.ctx, &bot.SendPhotoParams{ChatID: t.chatID, Photo: &models.InputFileString{Data: l.PhotoFileIDs[0]}}); err != nil {
			a.log.Warn("send photo", "err", err)
		}
	} else if _, err := t.b.SendMediaGroup(t.ctx, &bot.SendMediaGroupParams{ChatID: t.chatID, Media: media}); err != nil {
		a.log.Warn("send media group", "err", err)
	}
	a.send(t, text, kb)
}

// cardText — текст карточки объявления (FR-B-04).
func cardText(l store.Listing) string {
	var b strings.Builder
	title := "Без названия"
	if l.Title != nil {
		title = *l.Title
	}
	b.WriteString("<b>" + html.EscapeString(title) + "</b>\n")

	if l.PrimaryCategory != nil {
		cats := []string{l.PrimaryCategory.Name}
		for _, c := range l.ExtraCategories {
			cats = append(cats, c.Name)
		}
		b.WriteString(html.EscapeString(strings.Join(cats, " · ")) + "\n")
	}

	var where []string
	for _, c := range l.Cities {
		where = append(where, c.Name)
	}
	place := strings.Join(where, ", ")
	if l.TravelsToClient {
		if place != "" {
			place += " · "
		}
		place += "выезд к клиенту"
	}
	if place != "" {
		b.WriteString("📍 " + html.EscapeString(place) + "\n")
	}
	if p := priceText(l); p != "" {
		b.WriteString("💰 " + html.EscapeString(p) + "\n")
	}
	if l.Description != nil {
		b.WriteString("\n" + html.EscapeString(*l.Description) + "\n")
	}
	var langs []string
	for _, c := range l.Languages {
		langs = append(langs, store.LanguageLabels[c])
	}
	if len(langs) > 0 {
		b.WriteString("\n🗣 " + html.EscapeString(strings.Join(langs, ", ")))
	}
	if l.Schedule != nil {
		b.WriteString("\n🕒 " + html.EscapeString(*l.Schedule))
	}
	var contacts []string
	if v := contactValue(l, "telegram"); v != "" {
		contacts = append(contacts, "Telegram "+v)
	}
	if v := contactValue(l, "whatsapp"); v != "" {
		contacts = append(contacts, "WhatsApp "+v)
	}
	if v := contactValue(l, "phone"); v != "" {
		contacts = append(contacts, "тел. "+v)
	}
	if v := contactValue(l, "instagram"); v != "" {
		contacts = append(contacts, "Instagram "+v)
	}
	if len(contacts) > 0 {
		b.WriteString("\n✉️ " + html.EscapeString(strings.Join(contacts, " · ")))
	}
	return strings.TrimRight(b.String(), "\n")
}

func priceText(l store.Listing) string { return l.PriceText() }

func formatAmount(v float64, currency string) string { return store.FormatAmount(v, currency) }

var thousandsRe = regexp.MustCompile(`^\d{1,3}([.,]\d{3})+$`)

// parseAmount понимает «450000», «450 000», «450.000», «450к», «1,5 млн», «25.5».
func parseAmount(s string) (float64, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer(" ", "", " ", "", " ", "", "₫", "", "$", "", "vnd", "", "usd", "", "донгов", "", "донга", "").Replace(s)
	mult := 1.0
	for _, suf := range []struct {
		s string
		m float64
	}{{"млн", 1e6}, {"kk", 1e6}, {"m", 1e6}, {"тыс", 1e3}, {"к", 1e3}, {"k", 1e3}} {
		if strings.HasSuffix(s, suf.s) {
			s = strings.TrimSuffix(s, suf.s)
			mult = suf.m
			break
		}
	}
	if thousandsRe.MatchString(s) {
		s = strings.NewReplacer(".", "", ",", "").Replace(s)
	} else {
		s = strings.Replace(s, ",", ".", 1)
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	v *= mult
	if v >= 1e12 {
		return 0, false
	}
	return v, true
}

// normalizeContact приводит контакт к одному виду: телефоны без мусора, Instagram — ником.
func normalizeContact(field, v string) string {
	v = strings.TrimSpace(v)
	switch field {
	case "phone", "whatsapp":
		var b strings.Builder
		for i, r := range v {
			if (r >= '0' && r <= '9') || (r == '+' && i == 0) || r == ' ' {
				b.WriteRune(r)
			}
		}
		return strings.Join(strings.Fields(b.String()), " ")
	case "instagram":
		for _, p := range []string{"https://", "http://", "www.", "instagram.com/", "@"} {
			v = strings.TrimPrefix(v, p)
		}
		v = strings.TrimSuffix(v, "/")
		if i := strings.IndexAny(v, "/?"); i >= 0 {
			v = v[:i]
		}
		return v
	}
	return v
}
