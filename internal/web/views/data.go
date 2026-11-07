// Package views — HTML-шаблоны админки на templ.
// Файлы *_templ.go генерирует `make generate`; руками их не правим.
package views

import (
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/a-h/templ"

	"github.com/signaveris-13/uslugi/internal/store"
)

// Page — общие данные для каждой страницы админки.
type Page struct {
	Title          string
	Me             store.TeamMember
	Nav            string // "list", "board", "new", "listings"
	TZ             *time.Location
	QueueCount     int    // объявлений ждут модерации
	ComplaintCount int    // новых жалоб
	RevisionCount  int    // правок опубликованных объявлений ждут проверки
	ReviewCount    int    // отзывы, ответы и споры на модерации
	SupportCount   int    // обращения ждут ответа
	Env            string // «Stage» на тестовом стенде; у прода пусто
}

// AttentionCount — сколько всего ждёт модератора: бейдж в меню.
func (p Page) AttentionCount() int { return p.QueueCount + p.ComplaintCount + p.RevisionCount }

type CategoryGroup struct {
	Name  string
	Emoji string
	Items []store.Category
}

// Refs — справочники для фильтров и форм.
type Refs struct {
	Cities []store.City
	Groups []CategoryGroup
	Team   []store.TeamMember
}

type LeadsData struct {
	Page
	Refs
	Leads  []store.Lead
	Filter store.LeadFilter
}

type BoardColumn struct {
	Status string
	Leads  []store.Lead
}

type BoardData struct {
	Page
	Refs
	Columns []BoardColumn
	Filter  store.LeadFilter
	Back    string // текущий адрес доски, чтобы вернуться после смены статуса
}

type LeadData struct {
	Page
	Refs
	Lead       store.Lead
	Form       store.LeadInput
	IsNew      bool
	Error      string
	Notice     string
	Touches    []store.Touch
	Duplicates []store.Lead
	InviteURL  string
}

// GroupCategories раскладывает подкатегории по родителям для <optgroup>.
func GroupCategories(cats []store.Category) []CategoryGroup {
	var groups []CategoryGroup
	for _, c := range cats {
		if len(groups) == 0 || groups[len(groups)-1].Name != c.ParentName {
			groups = append(groups, CategoryGroup{Name: c.ParentName, Emoji: c.Emoji})
		}
		groups[len(groups)-1].Items = append(groups[len(groups)-1].Items, c)
	}
	return groups
}

// FormFromLead — значения формы из сохранённого лида.
func FormFromLead(l store.Lead) store.LeadInput {
	return store.LeadInput{
		Source:           l.Source,
		SourceURL:        deref(l.SourceURL),
		SourceText:       deref(l.SourceText),
		Name:             deref(l.Name),
		CategoryID:       derefID(l.CategoryID),
		CityID:           derefID(l.CityID),
		TelegramUsername: deref(l.TelegramUsername),
		Phone:            deref(l.Phone),
		WhatsApp:         deref(l.WhatsApp),
		Instagram:        deref(l.Instagram),
		Summary:          deref(l.Summary),
		Notes:            deref(l.Notes),
		Tags:             l.Tags,
		Status:           l.Status,
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefID(id *int64) int64 {
	if id == nil {
		return 0
	}
	return *id
}

func id(v int64) string { return strconv.FormatInt(v, 10) }

func leadURL(v int64) templ.SafeURL { return templ.URL("/leads/" + id(v)) }

func leadAction(v int64, action string) string { return "/leads/" + id(v) + "/" + action }

func statusLabel(s string) string {
	if l, ok := store.LeadStatusLabels[s]; ok {
		return l
	}
	return s
}

func sourceLabel(s string) string {
	if l, ok := store.LeadSourceLabels[s]; ok {
		return l
	}
	return s
}

func formatTime(t time.Time, tz *time.Location) string {
	if tz != nil {
		t = t.In(tz)
	}
	return t.Format("02.01.2006 15:04")
}

func telegramURL(username string) templ.SafeURL {
	return templ.SafeURL("https://t.me/" + url.PathEscape(username))
}

func joinTags(tags []string) string {
	out := ""
	for i, t := range tags {
		if i > 0 {
			out += ", "
		}
		out += t
	}
	return out
}

func countLabel(n int) string { return fmt.Sprintf("%d", n) }

func statuses() []string { return store.LeadStatuses }

func sources() []string { return store.LeadSources }

// statusClass — цвет бейджа статуса.
func statusClass(s string) string {
	switch s {
	case store.LeadInvited, store.LeadReplied, store.LeadInterested:
		return "badge badge-progress"
	case store.LeadListingCreated, store.LeadPublished:
		return "badge badge-done"
	case store.LeadDeclined, store.LeadDoNotContact, store.LeadInvalid:
		return "badge badge-off"
	}
	return "badge"
}

type ListingsData struct {
	Page
	Rows   []store.ListingRow
	Filter store.ListingFilter
}

type ListingData struct {
	Page
	Photos     []string // ссылки на фото: из Telegram или загруженные в приложении
	Revision   *RevisionView
	Complaints []store.Complaint
	Listing    store.Listing
	Provider   store.ProviderInfo
	Duplicates []store.ListingRow
	History    []store.ListingEvent
	Error      string
	Notice     string
}

// RevisionView — правка опубликованного объявления: что поменялось.
type RevisionView struct {
	CreatedAt     time.Time
	Changes       []FieldChange
	PhotosChanged bool
	NewPhotos     []string
}

type FieldChange struct {
	Label, Old, New string
}

// StatusRevisions — вкладка «Правки» в списке объявлений.
const StatusRevisions = "revisions"

// Вкладки списка объявлений: статус → подпись.
var listingTabs = []struct{ Status, Label string }{
	{store.ListingModeration, "На модерации"},
	{store.ListingPublished, "Опубликованные"},
	{store.ListingRejected, "Отклонённые"},
	{store.ListingPaused, "На паузе"},
	{store.ListingArchived, "Архив"},
	{"", "Все"},
}

func listingStatusLabel(s string) string {
	if l, ok := store.ListingStatusLabels[s]; ok {
		return l
	}
	return s
}

func listingStatusClass(s string) string {
	switch s {
	case store.ListingModeration:
		return "badge badge-progress"
	case store.ListingPublished:
		return "badge badge-done"
	case store.ListingRejected, store.ListingArchived:
		return "badge badge-off"
	}
	return "badge"
}

func listingURL(v int64) templ.SafeURL { return templ.URL("/listings/" + id(v)) }

func listingAction(v int64, action string) string { return "/listings/" + id(v) + "/" + action }

func tabURL(status string) templ.SafeURL {
	if status == "" {
		return templ.URL("/listings?status=all")
	}
	return templ.URL("/listings?status=" + status)
}

func photoURL(fileID string) templ.SafeURL { return templ.URL("/media/tg/" + url.PathEscape(fileID)) }

func titleOr(t *string) string {
	if t == nil || *t == "" {
		return "Без названия"
	}
	return *t
}

func timeOr(t *time.Time, tz *time.Location) string {
	if t == nil {
		return "—"
	}
	return formatTime(*t, tz)
}

func languagesText(codes []string) string {
	out := ""
	for i, c := range codes {
		if i > 0 {
			out += ", "
		}
		if l, ok := store.LanguageLabels[c]; ok {
			out += l
		} else {
			out += c
		}
	}
	return out
}

func citiesText(l store.Listing) string {
	out := ""
	for i, c := range l.Cities {
		if i > 0 {
			out += ", "
		}
		out += c.Name
	}
	if l.TravelsToClient {
		if out != "" {
			out += " · "
		}
		out += "выезд к клиенту"
	}
	return out
}

func extrasText(l store.Listing) string {
	out := ""
	for i, c := range l.ExtraCategories {
		if i > 0 {
			out += ", "
		}
		out += c.Name
	}
	return out
}

var auditLabels = map[string]string{
	"listing.approve":     "Одобрено",
	"listing.reject":      "Отклонено",
	"complaint.resolved":  "Жалоба: меры приняты",
	"complaint.dismissed": "Жалоба отклонена",
}

func auditLabel(a string) string {
	if l, ok := auditLabels[a]; ok {
		return l
	}
	return a
}

func rejectReasons() []string { return store.RejectReasons }

func countBadge(n int) string {
	if n > 99 {
		return "99+"
	}
	return countLabel(n)
}

type ComplaintsData struct {
	Page
	Complaints []store.Complaint
	Status     string
}

func complaintReason(r string) string {
	if l, ok := store.ComplaintReasonLabels[r]; ok {
		return l
	}
	return r
}

func complaintStatusLabel(s string) string {
	return map[string]string{"new": "Новая", "resolved": "Меры приняты", "dismissed": "Отклонена"}[s]
}

func complaintStatusClass(s string) string {
	if s == "new" {
		return "badge badge-progress"
	}
	return "badge badge-off"
}
