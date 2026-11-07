package botapp

import (
	"fmt"
	"html"

	"github.com/signaveris-13/uslugi/internal/store"
)

// askCity — выбор города (FR-B-01). Пока страна одна, сразу показываем её города.
func (a *App) askCity(t *turn, text string) {
	cities, err := a.store.ListCities(t.ctx)
	if err != nil {
		a.fail(t, err)
		return
	}
	var kb keyboard
	for _, c := range cities {
		kb = append(kb, row(btn(c.Name, fmt.Sprintf("city:%d", c.ID))))
	}
	a.show(t, text, kb)
}

func (a *App) onCityChosen(t *turn, cityID int64) {
	if cityID == 0 {
		return
	}
	first := t.user.CityID == nil
	if err := a.store.SetUserCity(t.ctx, t.user.ID, cityID); err != nil {
		a.fail(t, err)
		return
	}
	t.user.CityID = &cityID
	a.event(t, "city_selected", "city", cityID, nil)

	if !first {
		a.showMenu(t, "Город сменили на "+html.EscapeString(a.store.CityName(t.ctx, cityID))+".")
		return
	}
	a.show(t, "Отлично, "+html.EscapeString(a.store.CityName(t.ctx, cityID))+"! Что тебя сюда привело?", keyboard{
		row(btn("🔍 Ищу услугу", "role:client")),
		row(btn("💼 Я исполнитель — хочу разместиться", "role:provider")),
	})
}

func (a *App) onRoleChosen(t *turn, role string) {
	if role == "provider" {
		a.startNewListing(t)
		return
	}
	a.showMenu(t, "")
}

func (a *App) showMenu(t *turn, prefix string) {
	if t.user.CityID == nil {
		a.askCity(t, "Сначала выбери город:")
		return
	}
	text := "<b>Uslugi</b> · " + html.EscapeString(a.store.CityName(t.ctx, *t.user.CityID)) + "\nКаталог услуг русскоязычных мастеров."
	if prefix != "" {
		text = prefix + "\n\n" + text
	}
	a.show(t, text, keyboard{
		row(btn("🔍 Найти услугу", "m:find")),
		row(btn("➕ Разместить объявление", "m:new")),
		row(btn("📋 Мои объявления", "m:mine"), btn("📍 Сменить город", "m:city")),
	})
}

func (a *App) onMenu(t *turn, item string) {
	switch item {
	case "find":
		a.show(t, "Каталог скоро откроется: сейчас мы собираем первых мастеров. Если ты мастер — размести объявление, чтобы клиенты увидели тебя первыми.", keyboard{
			row(btn("➕ Разместить объявление", "m:new")),
			row(btn("← В меню", "m:menu")),
		})
	case "new":
		a.startNewListing(t)
	case "mine":
		a.showMyListings(t)
	case "city":
		a.askCity(t, "Выбери город:")
	default:
		a.showMenu(t, "")
	}
}

var listingStatusIcons = map[string]string{
	store.ListingDraft:      "📝",
	store.ListingModeration: "⏳",
	store.ListingPublished:  "✅",
	store.ListingRejected:   "⚠️",
	store.ListingPaused:     "⏸",
}

// showMyListings — «Мои объявления» (FR-P-05).
func (a *App) showMyListings(t *turn) {
	p, err := a.store.ProviderByUser(t.ctx, t.user.ID)
	if isNotFound(err) {
		a.show(t, "У тебя пока нет объявлений.", keyboard{
			row(btn("➕ Разместить объявление", "m:new")),
			row(btn("← В меню", "m:menu")),
		})
		return
	}
	if err != nil {
		a.fail(t, err)
		return
	}
	list, err := a.store.ListProviderListings(t.ctx, p.ID)
	if err != nil {
		a.fail(t, err)
		return
	}
	if len(list) == 0 {
		a.show(t, "У тебя пока нет объявлений.", keyboard{
			row(btn("➕ Разместить объявление", "m:new")),
			row(btn("← В меню", "m:menu")),
		})
		return
	}
	var kb keyboard
	for _, l := range list {
		title := "Без названия"
		if l.Title != nil {
			title = *l.Title
		}
		label := fmt.Sprintf("%s %s · %s", listingStatusIcons[l.Status], title, store.ListingStatusLabels[l.Status])
		kb = append(kb, row(btn(truncate(label, 60), fmt.Sprintf("f:open:%d", l.ID))))
	}
	kb = append(kb, row(btn("➕ Новое объявление", "m:new")), row(btn("← В меню", "m:menu")))
	a.show(t, "<b>Мои объявления</b>", kb)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
