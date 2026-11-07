package views

import (
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/signaveris-13/uslugi/internal/store"
)

// Экраны клиента в Mini App: главная, поиск, категория, карточка, избранное, профиль.

// Вкладки нижнего меню.
const (
	TabHome    = "home"
	TabFav     = "fav"
	TabMine    = "mine"
	TabProfile = "profile"
)

type AppCardView struct {
	store.CatalogCard
	PhotoURL string
}

type AppHomeData struct {
	CityName   string
	Total      int
	Categories []store.CatalogItem
	IsProvider bool
}

type AppCityData struct {
	Cities  []store.CityCount
	Current int64
	Next    string // куда вернуться после выбора: home или my
}

type AppChip struct {
	Label string
	URL   string
	On    bool
	Count int
}

// AppListData — список карточек: категория или поиск.
type AppListData struct {
	Title     string
	Sub       string
	Back      string
	Chips     []AppChip
	Cards     []AppCardView
	Total     int
	Query     store.CatalogQuery
	FilterURL string
	MoreURL   string
	// Поиск.
	Search  bool
	Text    string
	Subcats []store.CatalogItem
}

type AppFiltersData struct {
	Action string // куда отправить форму
	Query  store.CatalogQuery
	Back   string
	Count  int
	SubID  int64
}

type AppListingData struct {
	Listing      store.Listing
	ProviderName string
	PhotoURLs    []string
	Fav          bool
	ShareURL     string
	Back         string
	Notice       string
	Error        string
	Own          bool
}

type AppComplaintData struct {
	Listing store.Listing
	Name    string
	Reason  string
	Comment string
	Error   string
}

type AppFavCard struct {
	store.FavoriteCard
	PhotoURL string
}

type AppFavData struct {
	Cards []AppFavCard
}

type AppProfileData struct {
	Name       string
	Username   string
	CityName   string
	IsProvider bool
	Stats      store.Stats
	Active     int
	Limit      int
	Summary    string
}

type AppManageData struct {
	Listing  store.Listing
	PhotoURL string
	Stats7   store.Stats
	Stats30  store.Stats
	Days     int
	ShareURL string
	Notice   string
	Error    string
	Revision bool
}

type AppRulesData struct {
	Text string
}

func (d AppManageData) Stats() store.Stats {
	if d.Days == 30 {
		return d.Stats30
	}
	return d.Stats7
}

// CatalogURL собирает адрес списка с фильтрами.
func CatalogURL(path string, q store.CatalogQuery) string {
	v := url.Values{}
	if q.CategoryID > 0 && q.ParentID > 0 {
		v.Set("sub", strconv.FormatInt(q.CategoryID, 10))
	}
	if q.Text != "" {
		v.Set("q", q.Text)
	}
	if q.Travels {
		v.Set("travels", "1")
	}
	if q.MaxPrice > 0 {
		v.Set("max", strconv.FormatInt(q.MaxPrice, 10))
	}
	if q.Language != "" {
		v.Set("lang", q.Language)
	}
	if q.Offset > 0 {
		v.Set("offset", strconv.Itoa(q.Offset))
	}
	if len(v) == 0 {
		return path
	}
	return path + "?" + v.Encode()
}

func appListingURL(i int64) string  { return "/app/l/" + id(i) }
func appCategoryURL(i int64) string { return "/app/c/" + id(i) }
func appManageURL(i int64) string   { return "/app/listings/" + id(i) + "/manage" }

func cardPlace(c store.CatalogCard) string {
	parts := []string{}
	if c.ProviderName != "" {
		parts = append(parts, c.ProviderName)
	}
	place := c.CityName
	if c.TravelsToClient {
		if place != "" {
			place += ", выезд"
		} else {
			place = "выезд к клиенту"
		}
	}
	if place != "" {
		parts = append(parts, place)
	}
	return strings.Join(parts, " · ")
}

func priceOr(l store.Listing) string {
	if p := l.PriceText(); p != "" {
		return p
	}
	return "цена по запросу"
}

func photoBadge(n int) string {
	if n <= 1 {
		return ""
	}
	return strconv.Itoa(n) + " фото"
}

func countMasters(n int) string {
	return plural(n, "объявление", "объявления", "объявлений")
}

func plural(n int, one, few, many string) string {
	m10, m100 := n%10, n%100
	w := many
	switch {
	case m10 == 1 && m100 != 11:
		w = one
	case m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14):
		w = few
	}
	return strconv.Itoa(n) + " " + w
}

func listingPlace(l store.Listing) string {
	var cities []string
	for _, c := range l.Cities {
		cities = append(cities, c.Name)
	}
	p := strings.Join(cities, ", ")
	if l.TravelsToClient {
		if p != "" {
			p += " · "
		}
		p += "выезд к клиенту"
	}
	return p
}

func listingCats(l store.Listing) string {
	var out []string
	if l.PrimaryCategory != nil {
		out = append(out, l.PrimaryCategory.Name)
	}
	for _, c := range l.ExtraCategories {
		out = append(out, c.Name)
	}
	return strings.Join(out, " · ")
}

func listingLangs(l store.Listing) string {
	var out []string
	for _, c := range l.Languages {
		if n := store.LanguageLabels[c]; n != "" {
			out = append(out, n)
		}
	}
	return strings.Join(out, ", ")
}

func strOr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func contactPath(id int64, ch string) string {
	return "/app/l/" + strconv.FormatInt(id, 10) + "/contact/" + ch
}

func favPath(i int64) string { return "/app/l/" + id(i) + "/fav" }

func complaintPath(i int64) string { return "/app/l/" + id(i) + "/complaint" }

func complaintReasons() []string { return store.ComplaintReasons }

func complaintLabel(r string) string { return store.ComplaintReasonLabels[r] }

func vals(kv ...string) string {
	b := strings.Builder{}
	b.WriteString("{")
	for i := 0; i+1 < len(kv); i += 2 {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(strconv.Quote(kv[i]) + ":" + strconv.Quote(kv[i+1]))
	}
	b.WriteString("}")
	return b.String()
}

var priceSteps = []struct {
	Label string
	Value int64
}{{"Любая", 0}, {"до 300 000 ₫", 300000}, {"до 700 000 ₫", 700000}, {"до 1,5 млн ₫", 1500000}}

func langOptions() []string { return store.LanguageCodes }

// Иконки: строки SVG, свои, без внешних наборов.
var icons = map[string]string{
	"home":   `<path d="M3 10.5 12 3l9 7.5V20a1 1 0 0 1-1 1h-5v-6h-6v6H4a1 1 0 0 1-1-1z"></path>`,
	"heart":  `<path d="M12 20s-7.5-4.6-9.2-9.3C1.7 7.4 3.9 4 7.3 4c2 0 3.4 1.1 4.7 2.7C13.3 5.1 14.7 4 16.7 4c3.4 0 5.6 3.4 4.5 6.7C19.5 15.4 12 20 12 20z"></path>`,
	"plus":   `<path d="M12 5v14M5 12h14"></path>`,
	"list":   `<rect x="4" y="4" width="16" height="16" rx="3"></rect><path d="M8 9h8M8 13h8M8 17h5"></path>`,
	"user":   `<circle cx="12" cy="8" r="4"></circle><path d="M4 21a8 8 0 0 1 16 0"></path>`,
	"search": `<circle cx="11" cy="11" r="7"></circle><path d="m20 20-3.5-3.5"></path>`,
	"pin":    `<path d="M12 21s-7-6.2-7-11.5A7 7 0 0 1 19 9.5C19 14.8 12 21 12 21z"></path><circle cx="12" cy="9.5" r="2.5"></circle>`,
	"down":   `<path d="m6 9 6 6 6-6"></path>`,
	"right":  `<path d="m9 6 6 6-6 6"></path>`,
	"filter": `<path d="M4 6h16M7 12h10M10 18h4"></path>`,
	"camera": `<rect x="3" y="6" width="18" height="14" rx="3"></rect><circle cx="12" cy="13" r="3.5"></circle><path d="M9 6l1.5-2h3L15 6"></path>`,
	"send":   `<path d="m22 2-7 20-4-9-9-4z"></path><path d="M22 2 11 13"></path>`,
	"chat":   `<path d="M21 12a8 8 0 0 1-11.6 7.1L4 20l1-4.6A8 8 0 1 1 21 12z"></path>`,
	"phone":  `<path d="M22 16.9v3a2 2 0 0 1-2.2 2 19.8 19.8 0 0 1-8.6-3.1 19.5 19.5 0 0 1-6-6A19.8 19.8 0 0 1 2.1 4.2 2 2 0 0 1 4.1 2h3a2 2 0 0 1 2 1.7c.1 1 .4 1.9.7 2.8a2 2 0 0 1-.5 2.1L8.1 9.9a16 16 0 0 0 6 6l1.3-1.3a2 2 0 0 1 2.1-.4c.9.3 1.8.6 2.8.7a2 2 0 0 1 1.7 2z"></path>`,
	"insta":  `<rect x="3" y="3" width="18" height="18" rx="5"></rect><circle cx="12" cy="12" r="4"></circle><circle cx="17.5" cy="6.5" r="1"></circle>`,
	"share":  `<circle cx="18" cy="5" r="3"></circle><circle cx="6" cy="12" r="3"></circle><circle cx="18" cy="19" r="3"></circle><path d="m8.6 13.5 6.8 4M15.4 6.5l-6.8 4"></path>`,
	"shield": `<path d="M12 3 4 6v6c0 5 3.5 8 8 9 4.5-1 8-4 8-9V6z"></path><path d="m9 12 2 2 4-4"></path>`,
	"check":  `<path d="m5 12 4.5 4.5L19 7"></path>`,
	"close":  `<path d="M18 6 6 18M6 6l12 12"></path>`,
	"eye":    `<path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12z"></path><circle cx="12" cy="12" r="3"></circle>`,
	"edit":   `<path d="M12 20h9"></path><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z"></path>`,
	"pause":  `<rect x="6" y="5" width="4" height="14" rx="1"></rect><rect x="14" y="5" width="4" height="14" rx="1"></rect>`,
	"play":   `<path d="M7 4v16l13-8z"></path>`,
	"trash":  `<path d="M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6"></path>`,
	"doc":    `<path d="M7 3h7l5 5v12a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1z"></path><path d="M14 3v5h5M9 13h6M9 17h6"></path>`,
	"help":   `<circle cx="12" cy="12" r="9"></circle><path d="M9.5 9a2.5 2.5 0 1 1 3.5 2.3c-.6.3-1 .9-1 1.6V14M12 17h.01"></path>`,
	"star":   `<path d="m12 3 2.7 5.6 6.1.9-4.4 4.3 1 6.1L12 17l-5.4 2.9 1-6.1L3.2 9.5l6.1-.9z"></path>`,
	"flag":   `<path d="M5 21V4M5 4h11l-2 4 2 4H5"></path>`,
	"clock":  `<circle cx="12" cy="12" r="9"></circle><path d="M12 7v5l3 2"></path>`,
	"grid":   `<rect x="4" y="4" width="6" height="6" rx="1.5"></rect><rect x="14" y="4" width="6" height="6" rx="1.5"></rect><rect x="4" y="14" width="6" height="6" rx="1.5"></rect><rect x="14" y="14" width="6" height="6" rx="1.5"></rect>`,
	"bag":    `<path d="M6 7h12l1 13H5z"></path><path d="M9 7a3 3 0 0 1 6 0"></path>`,
}

// icon — встроенная SVG-иконка; цвет — currentColor.
func icon(name string, size int) templ.Component {
	s := strconv.Itoa(size)
	return templ.Raw(`<svg width="` + s + `" height="` + s + `" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">` + icons[name] + `</svg>`)
}

func iconFilled(name string, size int, fill bool) templ.Component {
	s := strconv.Itoa(size)
	f := "none"
	if fill {
		f = "currentColor"
	}
	return templ.Raw(`<svg width="` + s + `" height="` + s + `" viewBox="0 0 24 24" fill="` + f + `" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">` + icons[name] + `</svg>`)
}

func int64Str(v int64) string { return strconv.FormatInt(v, 10) }

func isNewListing(l store.Listing) bool {
	return l.PublishedAt != nil && time.Since(*l.PublishedAt) < 14*24*time.Hour
}

func firstLetter(s string) string {
	for _, r := range strings.TrimSpace(s) {
		return strings.ToUpper(string(r))
	}
	return "U"
}

func barWidth(n, limit int) templ.SafeCSS {
	pct := 0
	if limit > 0 {
		pct = n * 100 / limit
	}
	if pct > 100 {
		pct = 100
	}
	return templ.SafeCSS("width: " + strconv.Itoa(pct) + "%")
}
