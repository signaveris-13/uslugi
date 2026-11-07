package views

import (
	"fmt"
	"strconv"

	"github.com/a-h/templ"

	"github.com/signaveris-13/uslugi/internal/store"
)

// Отзывы и поддержка в Mini App.

type AppReviewsData struct {
	Listing      store.Listing
	ProviderName string
	Summary      store.ReviewSummary
	Reviews      []store.Review
	ViewerID     int64
	CanReview    bool
}

type AppReviewFormData struct {
	Listing      store.Listing
	ProviderName string
	Rating       int
	Tags         []string
	Text         string
	ShowName     bool
	AuthorName   string
	Error        string
}

func (d AppReviewFormData) hasTag(t string) bool {
	for _, x := range d.Tags {
		if x == t {
			return true
		}
	}
	return false
}

type AppProviderReviewsData struct {
	Summary store.ReviewSummary
	Reviews []store.Review
	Notice  string
	Error   string
	ErrorID int64
}

type AppSupportData struct {
	Tickets  []store.SupportTicket
	Listings []store.ProviderCard
	Topic    string
	Text     string
	Error    string
}

type AppThreadData struct {
	Ticket   store.SupportTicket
	Messages []store.SupportMessage
	Error    string
}

func reviewTags() []string { return store.ReviewTags }

func supportTopics() []string { return store.SupportTopics }

func supportTopicLabel(t string) string { return store.SupportTopicLabels[t] }

func supportStatusLabel(s string) string { return store.SupportStatusLabels[s] }

func supportStatusClass(s string) string {
	switch s {
	case "answered":
		return "chip chip-ok"
	case "open":
		return "chip chip-dark"
	}
	return "chip"
}

var topicIcons = map[string]string{"listing": "list", "reviews": "star", "provider": "flag", "bug": "help", "idea": "star", "other": "help"}

func topicIcon(t string) string { return topicIcons[t] }

func avgText(s store.ReviewSummary) string {
	if s.Count == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f", s.Average)
}

func reviewsCount(n int) string { return plural(n, "отзыв", "отзыва", "отзывов") }

// stars — оценка звёздами, заполненные — жёлтые.
func stars(n, size int) templ.Component {
	s := strconv.Itoa(size)
	out := `<span class="stars" aria-label="Оценка ` + strconv.Itoa(n) + ` из 5">`
	for i := 1; i <= 5; i++ {
		cls := "star-off"
		if i <= n {
			cls = "star-on"
		}
		out += `<svg class="` + cls + `" width="` + s + `" height="` + s + `" viewBox="0 0 24 24" stroke-width="1.6" stroke-linejoin="round" aria-hidden="true">` + icons["star"] + `</svg>`
	}
	return templ.Raw(out + `</span>`)
}

func distWidth(s store.ReviewSummary, n int) templ.SafeCSS {
	pct := 0
	if s.Count > 0 {
		pct = s.ByStars[n] * 100 / s.Count
	}
	return templ.SafeCSS("width: " + strconv.Itoa(pct) + "%")
}

func starOptions() []int { return []int{5, 4, 3, 2, 1} }

func reviewsPath(i int64) string       { return "/app/l/" + id(i) + "/reviews" }
func reviewFormPath(i int64) string    { return "/app/l/" + id(i) + "/review" }
func replyPath(i int64) string         { return "/app/reviews/" + id(i) + "/reply" }
func disputePath(i int64) string       { return "/app/reviews/" + id(i) + "/dispute" }
func ticketPath(i int64) string        { return "/app/support/" + id(i) }
func ticketNumber(i int64) string      { return "№" + id(i) }
func reviewDate(r store.Review) string { return dateRu(r.CreatedAt.Day(), int(r.CreatedAt.Month())) }

var monthsGen = []string{"", "января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"}

func dateRu(day, month int) string { return strconv.Itoa(day) + " " + monthsGen[month] }

func isMine(r store.Review, viewer int64) bool { return r.AuthorUserID == viewer }

func replyState(r store.Review) string {
	if r.ReplyStatus == nil {
		return ""
	}
	return *r.ReplyStatus
}
