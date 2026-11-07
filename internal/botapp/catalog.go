package botapp

import (
	"errors"
	"fmt"
	"html"
	"net/url"
	"strings"
	"time"

	"github.com/signaveris-13/uslugi/internal/store"
)

// Каталог клиента (FR-B-02…08). Кнопки — "c:<действие>:<аргументы>".

const pageSize = 5

func (a *App) onCatalog(t *turn, data string) {
	parts := strings.Split(data, ":")
	arg := func(i int) int64 {
		if i < len(parts) {
			return parseID(parts[i])
		}
		return 0
	}
	if t.user.CityID == nil {
		a.askCity(t, "Сначала выбери город:")
		return
	}
	switch parts[0] {
	case "cats":
		a.showCatalog(t)
	case "p":
		a.showSubcategories(t, arg(1))
	case "s":
		a.showCategoryPage(t, arg(1), int(arg(2)))
	case "l":
		a.showCard(t, arg(1), arg(2), int(arg(3)))
	case "w":
		channel := ""
		if len(parts) > 2 {
			channel = parts[2]
		}
		a.onContact(t, arg(1), channel)
	case "fav":
		a.onToggleFavorite(t, arg(1), arg(2), int(arg(3)))
	case "favs":
		a.showFavorites(t)
	case "sh":
		a.onShare(t, arg(1))
	case "rep":
		a.askComplaintReason(t, arg(1))
	case "rr":
		if len(parts) > 2 {
			a.askComplaintComment(t, arg(1), parts[2])
		}
	case "rs":
		a.sendComplaint(t, "")
	default:
		a.log.Warn("неизвестная кнопка каталога", "data", data)
	}
}

// showCatalog — категории города со счётчиками.
func (a *App) showCatalog(t *turn) {
	cats, err := a.store.CatalogCategories(t.ctx, *t.user.CityID)
	if err != nil {
		a.fail(t, err)
		return
	}
	city := html.EscapeString(a.store.CityName(t.ctx, *t.user.CityID))
	if len(cats) == 0 {
		a.show(t, "В "+city+" пока нет опубликованных объявлений — мы собираем первых мастеров. Загляни чуть позже!", keyboard{
			row(btn("➕ Разместить своё", "m:new")),
			row(btn("← В меню", "m:menu")),
		})
		return
	}
	var kb keyboard
	for i := 0; i < len(cats); i += 2 {
		r := row(catButton(cats[i]))
		if i+1 < len(cats) {
			r = append(r, catButton(cats[i+1]))
		}
		kb = append(kb, r)
	}
	kb = append(kb, row(btn("⭐ Избранное", "c:favs"), btn("← В меню", "m:menu")))
	a.show(t, "<b>Каталог · "+city+"</b>\nВыбери категорию:", kb)
}

func catButton(c store.CatalogItem) btnT {
	return btn(fmt.Sprintf("%s %s · %d", c.Emoji, c.Name, c.Count), fmt.Sprintf("c:p:%d", c.ID))
}

func (a *App) showSubcategories(t *turn, parentID int64) {
	subs, err := a.store.CatalogSubcategories(t.ctx, parentID, *t.user.CityID)
	if err != nil {
		a.fail(t, err)
		return
	}
	// Одна непустая подкатегория — сразу список, без лишнего шага.
	if len(subs) == 1 {
		a.showCategoryPage(t, subs[0].ID, 0)
		return
	}
	if len(subs) == 0 {
		a.showCatalog(t)
		return
	}
	a.event(t, "category_opened", "category", parentID, nil)
	name, emoji, _ := a.store.CategoryName(t.ctx, parentID)
	var kb keyboard
	for _, s := range subs {
		kb = append(kb, row(btn(fmt.Sprintf("%s · %d", s.Name, s.Count), fmt.Sprintf("c:s:%d:0", s.ID))))
	}
	kb = append(kb, row(btn("← Категории", "c:cats")))
	a.show(t, fmt.Sprintf("<b>%s %s</b>\nЧто именно ищешь?", emoji, html.EscapeString(name)), kb)
}

// showCategoryPage — список карточек подкатегории, по 5 на экран (FR-B-03).
func (a *App) showCategoryPage(t *turn, categoryID int64, page int) {
	if page < 0 {
		page = 0
	}
	list, total, err := a.store.CatalogListings(t.ctx, categoryID, *t.user.CityID, page*pageSize, pageSize)
	if err != nil {
		a.fail(t, err)
		return
	}
	if page == 0 {
		a.event(t, "category_opened", "category", categoryID, nil)
	}
	name, emoji, _ := a.store.CategoryName(t.ctx, categoryID)
	parentID, _ := a.store.CategoryParentID(t.ctx, categoryID)
	city := a.store.CityName(t.ctx, *t.user.CityID)
	if total == 0 {
		a.show(t, fmt.Sprintf("<b>%s %s · %s</b>\nЗдесь пока пусто.", emoji, html.EscapeString(name), html.EscapeString(city)), keyboard{
			row(btn("← Категории", "c:cats")),
		})
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "<b>%s %s · %s</b>\nНайдено: %d\n", emoji, html.EscapeString(name), html.EscapeString(city), total)
	var kb keyboard
	for i, r := range list {
		n := page*pageSize + i + 1
		l := r.Listing()
		line := fmt.Sprintf("%d. %s", n, l.TitleOr())
		if p := l.PriceText(); p != "" {
			line += " — " + p
		}
		if isNew(r.PublishedAt) {
			line += " · 🆕"
		}
		fmt.Fprintf(&b, "\n%s", html.EscapeString(line))
		kb = append(kb, row(btn(truncate(fmt.Sprintf("%d. %s", n, l.TitleOr()), 60), fmt.Sprintf("c:l:%d:%d:%d", r.ID, categoryID, page))))
	}
	var nav []btnT
	if page > 0 {
		nav = append(nav, btn("← Назад", fmt.Sprintf("c:s:%d:%d", categoryID, page-1)))
	}
	if (page+1)*pageSize < total {
		nav = append(nav, btn("Ещё →", fmt.Sprintf("c:s:%d:%d", categoryID, page+1)))
	}
	if len(nav) > 0 {
		kb = append(kb, nav)
	}
	back := "c:cats"
	if parentID != 0 {
		back = fmt.Sprintf("c:p:%d", parentID)
	}
	kb = append(kb, row(btn("← К категориям", back)))
	a.show(t, b.String(), kb)
}

// isNew — бейдж «Новый»: опубликовано в последние 14 дней.
func isNew(published *time.Time) bool {
	return published != nil && timeNow().Sub(*published) < 14*24*time.Hour
}

// showCard — карточка мастера (FR-B-04). categoryID и page — куда вернуться.
func (a *App) showCard(t *turn, listingID, categoryID int64, page int) {
	l, err := a.store.PublishedListing(t.ctx, listingID)
	if isNotFound(err) {
		a.show(t, "Это объявление больше не опубликовано.", keyboard{row(btn("← Каталог", "c:cats"))})
		return
	}
	if err != nil {
		a.fail(t, err)
		return
	}
	a.event(t, "listing_viewed", "listing", l.ID, nil)
	// Карточка с фото — всегда новым сообщением: альбом нельзя «отредактировать» в старое.
	if len(l.PhotoFileIDs) > 0 {
		t.editID = 0
	}
	a.sendCard(t, l, a.cardFooter(l), a.cardKeyboard(t, l, categoryID, page))
}

func (a *App) cardFooter(l store.Listing) string {
	footer := "<i>Uslugi — площадка, а не сторона сделки. Договаривайтесь с мастером напрямую.</i>"
	if l.PublishedAt != nil && isNew(l.PublishedAt) {
		footer = "🆕 <b>Новый мастер в каталоге</b>\n" + footer
	}
	return footer
}

func (a *App) cardKeyboard(t *turn, l store.Listing, categoryID int64, page int) keyboard {
	var kb keyboard
	if l.ContactTelegram != nil {
		kb = append(kb, row(btn("✉️ Написать в Telegram", fmt.Sprintf("c:w:%d:tg", l.ID))))
	}
	var r []btnT
	if l.ContactWhatsApp != nil {
		r = append(r, btn("WhatsApp", fmt.Sprintf("c:w:%d:wa", l.ID)))
	}
	if l.ContactPhone != nil {
		r = append(r, btn("📞 Телефон", fmt.Sprintf("c:w:%d:ph", l.ID)))
	}
	if l.ContactInstagram != nil {
		r = append(r, btn("Instagram", fmt.Sprintf("c:w:%d:ig", l.ID)))
	}
	if len(r) > 0 {
		kb = append(kb, r)
	}
	fav := "🤍 В избранное"
	if a.store.IsFavorite(t.ctx, t.user.ID, l.ID) {
		fav = "❤️ В избранном"
	}
	kb = append(kb,
		row(btn(fav, fmt.Sprintf("c:fav:%d:%d:%d", l.ID, categoryID, page)), btn("↗️ Поделиться", fmt.Sprintf("c:sh:%d", l.ID))),
		row(btn("⚠️ Пожаловаться", fmt.Sprintf("c:rep:%d", l.ID))),
	)
	if categoryID != 0 {
		kb = append(kb, row(btn("← К списку", fmt.Sprintf("c:s:%d:%d", categoryID, page))))
	} else {
		kb = append(kb, row(btn("← Каталог", "c:cats")))
	}
	return kb
}

// onContact — клик по контакту: записываем событие (главная метрика, FR-B-05) и даём ссылку.
func (a *App) onContact(t *turn, listingID int64, channel string) {
	l, err := a.store.PublishedListing(t.ctx, listingID)
	if err != nil {
		a.send(t, "Это объявление больше не опубликовано.", nil)
		return
	}
	name := html.EscapeString(a.store.ListingProviderName(t.ctx, l.ID))
	title := html.EscapeString(l.TitleOr())
	var text string
	var kb keyboard
	switch channel {
	case "tg":
		if l.ContactTelegram == nil {
			return
		}
		u := *l.ContactTelegram
		text = fmt.Sprintf("Напиши %s в Telegram: @%s\nСкажи, что нашёл объявление «%s» в Uslugi 🙂", name, html.EscapeString(u), title)
		kb = keyboard{row(urlBtn("✉️ Открыть чат", "https://t.me/"+url.PathEscape(u)))}
	case "wa":
		if l.ContactWhatsApp == nil {
			return
		}
		digits := onlyDigits(*l.ContactWhatsApp)
		text = fmt.Sprintf("WhatsApp %s: %s", name, html.EscapeString(*l.ContactWhatsApp))
		kb = keyboard{row(urlBtn("Открыть WhatsApp", "https://wa.me/"+digits))}
	case "ph":
		if l.ContactPhone == nil {
			return
		}
		text = fmt.Sprintf("Телефон %s: <code>%s</code>\nНажми на номер, чтобы скопировать.", name, html.EscapeString(*l.ContactPhone))
	case "ig":
		if l.ContactInstagram == nil {
			return
		}
		text = fmt.Sprintf("Instagram %s: %s", name, html.EscapeString(*l.ContactInstagram))
		kb = keyboard{row(urlBtn("Открыть Instagram", "https://instagram.com/"+url.PathEscape(*l.ContactInstagram)))}
	default:
		return
	}
	a.event(t, "contact_clicked", "listing", l.ID, map[string]any{"channel": channel})
	t.editID = 0
	a.send(t, text, kb)
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

func (a *App) onToggleFavorite(t *turn, listingID, categoryID int64, page int) {
	l, err := a.store.PublishedListing(t.ctx, listingID)
	if err != nil {
		a.send(t, "Это объявление больше не опубликовано.", nil)
		return
	}
	on, err := a.store.ToggleFavorite(t.ctx, t.user.ID, l.ID)
	if err != nil {
		a.fail(t, err)
		return
	}
	if on {
		a.event(t, "favorite_added", "listing", l.ID, nil)
	}
	// Перерисовываем ту же карточку: изменилась только кнопка.
	a.show(t, cardText(l)+"\n\n"+a.cardFooter(l), a.cardKeyboard(t, l, categoryID, page))
}

func (a *App) showFavorites(t *turn) {
	list, err := a.store.Favorites(t.ctx, t.user.ID)
	if err != nil {
		a.fail(t, err)
		return
	}
	if len(list) == 0 {
		a.show(t, "В избранном пока пусто. Нажми «🤍 В избранное» на карточке мастера, чтобы не потерять его.", keyboard{
			row(btn("🔍 Каталог", "c:cats")),
			row(btn("← В меню", "m:menu")),
		})
		return
	}
	var kb keyboard
	for _, r := range list {
		l := r.Listing()
		label := l.TitleOr()
		if p := l.PriceText(); p != "" {
			label += " — " + p
		}
		kb = append(kb, row(btn(truncate(label, 60), fmt.Sprintf("c:l:%d:0:0", r.ID))))
	}
	kb = append(kb, row(btn("← В меню", "m:menu")))
	a.show(t, "<b>⭐ Избранное</b>", kb)
}

// onShare — ссылка на карточку для пересылки (deep link l_<id>).
func (a *App) onShare(t *turn, listingID int64) {
	l, err := a.store.PublishedListing(t.ctx, listingID)
	if err != nil {
		a.send(t, "Это объявление больше не опубликовано.", nil)
		return
	}
	if a.botUsername == "" {
		a.send(t, "Поделиться пока не получится — попробуй позже.", nil)
		return
	}
	link := fmt.Sprintf("https://t.me/%s?start=l_%d", a.botUsername, l.ID)
	a.event(t, "share_clicked", "listing", l.ID, nil)
	share := "https://t.me/share/url?url=" + url.QueryEscape(link) + "&text=" + url.QueryEscape(l.TitleOr()+" — в Uslugi")
	t.editID = 0
	a.send(t, "Ссылка на мастера:\n"+link, keyboard{row(urlBtn("↗️ Переслать", share))})
}

// Жалобы (FR-B-08) -----------------------------------------------------------

func (a *App) askComplaintReason(t *turn, listingID int64) {
	var kb keyboard
	for _, r := range store.ComplaintReasons {
		kb = append(kb, row(btn(store.ComplaintReasonLabels[r], fmt.Sprintf("c:rr:%d:%s", listingID, r))))
	}
	kb = append(kb, row(btn("Отмена", fmt.Sprintf("c:l:%d:0:0", listingID))))
	t.editID = 0
	a.send(t, "Что не так с объявлением?", kb)
}

const stepComplaint = "complaint_comment"

func (a *App) askComplaintComment(t *turn, listingID int64, reason string) {
	if _, ok := store.ComplaintReasonLabels[reason]; !ok {
		return
	}
	if err := a.store.SetBotState(t.ctx, t.user.ID, store.BotState{Step: stepComplaint, ListingID: &listingID, Field: reason}); err != nil {
		a.fail(t, err)
		return
	}
	a.show(t, fmt.Sprintf("Причина: <b>%s</b>.\nНапиши пару слов, что случилось — так модератору будет проще разобраться. Или отправь без комментария.",
		store.ComplaintReasonLabels[reason]), keyboard{
		row(btn("Отправить без комментария", "c:rs")),
		row(btn("Отмена", fmt.Sprintf("c:l:%d:0:0", listingID))),
	})
}

func (a *App) sendComplaint(t *turn, comment string) {
	st, err := a.store.GetBotState(t.ctx, t.user.ID)
	if err != nil {
		a.fail(t, err)
		return
	}
	if st.Step != stepComplaint || st.ListingID == nil {
		a.showMenu(t, "")
		return
	}
	_ = a.store.SetBotState(t.ctx, t.user.ID, store.BotState{})
	err = a.store.CreateComplaint(t.ctx, t.user.ID, *st.ListingID, st.Field, comment)
	switch {
	case errors.Is(err, store.ErrDuplicateComplaint):
		a.show(t, "Ты уже пожаловался на это объявление — модератор разбирается.", keyboard{row(btn("← Каталог", "c:cats"))})
		return
	case err != nil:
		a.fail(t, err)
		return
	}
	a.event(t, "complaint_sent", "listing", *st.ListingID, map[string]any{"reason": st.Field})
	a.show(t, "Спасибо! Жалоба ушла модератору — проверим в ближайшее время.", keyboard{
		row(btn("← Каталог", "c:cats")),
		row(btn("← В меню", "m:menu")),
	})
}

func (a *App) onComplaintText(t *turn, text string) {
	if len([]rune(text)) > 1000 {
		text = string([]rune(text)[:1000])
	}
	a.sendComplaint(t, text)
}

// startFromDeepLink — /start l_<id> (карточка) и c_<id> (категория).
func (a *App) startFromDeepLink(t *turn, payload string) bool {
	if id, ok := strings.CutPrefix(payload, "l_"); ok {
		l, err := a.store.PublishedListing(t.ctx, parseID(id))
		if err != nil {
			return false
		}
		if t.user.CityID == nil && len(l.Cities) > 0 {
			cityID := l.Cities[0].ID
			if err := a.store.SetUserCity(t.ctx, t.user.ID, cityID); err == nil {
				t.user.CityID = &cityID
			}
		}
		a.showCard(t, l.ID, 0, 0)
		return true
	}
	if id, ok := strings.CutPrefix(payload, "c_"); ok {
		if t.user.CityID == nil {
			return false
		}
		cid := parseID(id)
		if _, err := a.store.CategoryParentID(t.ctx, cid); err == nil {
			a.showCategoryPage(t, cid, 0)
		} else {
			a.showSubcategories(t, cid)
		}
		return true
	}
	return false
}
