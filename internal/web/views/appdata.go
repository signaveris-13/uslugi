package views

import (
	"strconv"

	"github.com/signaveris-13/uslugi/internal/store"
)

// Данные экранов Mini App.

type AppMyData struct {
	Cards  []AppCard
	Active int
	Limit  int
	Notice string
	Error  string
}

// myCardURL: опубликованное и на паузе — экран управления, остальное — форма.
func myCardURL(c store.ProviderCard) string {
	if c.Status == store.ListingPublished || c.Status == store.ListingPaused {
		return appManageURL(c.ID)
	}
	return appListingPath(c.ID)
}

type AppCard struct {
	store.ProviderCard
	PhotoURL string
}

type AppEditorData struct {
	State       store.EditorState
	Groups      []CategoryGroup
	Siblings    []store.Option // подкатегории той же категории — для дополнительных
	Cities      []store.Option
	PhotoURLs   []string
	Username    string // Telegram username пользователя
	TermsNeeded bool
	TermsText   string
	Error       string
	Notice      string
	PriceInput  string
}

func (d AppEditorData) Snap() store.Snapshot { return d.State.Snapshot }

func (d AppEditorData) hasExtra(id int64) bool {
	for _, x := range d.State.Snapshot.ExtraCategoryIDs {
		if x == id {
			return true
		}
	}
	return false
}

func (d AppEditorData) hasCity(id int64) bool {
	for _, x := range d.State.Snapshot.CityIDs {
		if x == id {
			return true
		}
	}
	return false
}

func (d AppEditorData) hasLang(code string) bool {
	for _, x := range d.State.Snapshot.Languages {
		if x == code {
			return true
		}
	}
	return false
}

func (d AppEditorData) isNew() bool {
	return d.State.Listing.Status == store.ListingDraft && d.State.Listing.SubmittedAt == nil
}

func (d AppEditorData) submitLabel() string {
	switch {
	case d.State.Mode == store.EditRevision:
		return "Отправить правку на проверку"
	case d.State.Listing.Status == store.ListingModeration:
		return "Сохранить"
	}
	return "Отправить на модерацию"
}

func (d AppEditorData) missingText() string {
	miss := d.State.Snapshot.Missing()
	out := ""
	for i, m := range miss {
		if i > 0 {
			out += ", "
		}
		out += m
	}
	return out
}

func languageCodes() []string { return store.LanguageCodes }

func languageLabel(c string) string { return store.LanguageLabels[c] }

func appListingPath(id int64) string { return "/app/listings/" + strconv.FormatInt(id, 10) }

func photoIndex(i int) string { return "delphoto:" + strconv.Itoa(i) }

func appStatusLabel(c store.ProviderCard) string {
	if c.RevisionPending {
		return "Правка на проверке"
	}
	return listingStatusLabel(c.Status)
}

func appStatusClass(c store.ProviderCard) string {
	switch {
	case c.RevisionPending, c.Status == store.ListingModeration:
		return "chip chip-dark"
	case c.Status == store.ListingPublished:
		return "chip chip-ok"
	case c.Status == store.ListingRejected:
		return "chip chip-warn"
	}
	return "chip"
}

func maxPhotos() int { return store.MaxPhotos }
