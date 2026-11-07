package web

import (
	"context"
	"net/url"
	"slices"
	"strings"

	"github.com/signaveris-13/uslugi/internal/store"
	"github.com/signaveris-13/uslugi/internal/web/views"
)

// adminPhotoURL — фото для модератора: из Telegram через прокси или загруженное в приложении.
func adminPhotoURL(p store.SnapshotPhoto) string {
	if p.StorageKey != "" {
		return "/media/file/" + p.StorageKey
	}
	return "/media/tg/" + url.PathEscape(p.TelegramFileID)
}

// revisionView сравнивает опубликованную версию с правкой — модератор видит только изменения.
func (s *Server) revisionView(ctx context.Context, cur store.Snapshot, rev store.Revision) *views.RevisionView {
	nw := rev.Data
	v := &views.RevisionView{CreatedAt: rev.CreatedAt}
	add := func(label, old, new string) {
		if old != new {
			v.Changes = append(v.Changes, views.FieldChange{Label: label, Old: dash(old), New: dash(new)})
		}
	}
	add("Название", cur.Title, nw.Title)
	add("Категория", s.categoryNames(ctx, []int64{cur.PrimaryCategoryID}), s.categoryNames(ctx, []int64{nw.PrimaryCategoryID}))
	add("Ещё в категории", s.categoryNames(ctx, cur.ExtraCategoryIDs), s.categoryNames(ctx, nw.ExtraCategoryIDs))
	add("Города", s.cityNames(ctx, cur.CityIDs), s.cityNames(ctx, nw.CityIDs))
	add("Выезд к клиенту", yesNo(cur.TravelsToClient), yesNo(nw.TravelsToClient))
	add("Описание", cur.Description, nw.Description)
	add("Цена", snapshotPrice(cur), snapshotPrice(nw))
	add("Telegram", at(cur.Telegram), at(nw.Telegram))
	add("WhatsApp", cur.WhatsApp, nw.WhatsApp)
	add("Телефон", cur.Phone, nw.Phone)
	add("Instagram", cur.Instagram, nw.Instagram)
	add("Языки", langNames(cur.Languages), langNames(nw.Languages))
	add("График", cur.Schedule, nw.Schedule)
	if !slices.Equal(cur.Photos, nw.Photos) {
		v.PhotosChanged = true
		for _, p := range nw.Photos {
			v.NewPhotos = append(v.NewPhotos, adminPhotoURL(p))
		}
	}
	return v
}

func snapshotPrice(sn store.Snapshot) string {
	l := store.Listing{}
	if sn.PriceType != "" {
		l.PriceType = &sn.PriceType
	}
	l.PriceAmount = sn.PriceAmount
	if sn.Currency != "" {
		l.Currency = &sn.Currency
	}
	return l.PriceText()
}

func (s *Server) categoryNames(ctx context.Context, ids []int64) string {
	var out []string
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if n, _, err := s.store.CategoryName(ctx, id); err == nil {
			out = append(out, n)
		}
	}
	return strings.Join(out, ", ")
}

func (s *Server) cityNames(ctx context.Context, ids []int64) string {
	var out []string
	for _, id := range ids {
		if n := s.store.CityName(ctx, id); n != "" {
			out = append(out, n)
		}
	}
	return strings.Join(out, ", ")
}

func langNames(codes []string) string {
	var out []string
	for _, c := range codes {
		if l := store.LanguageLabels[c]; l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, ", ")
}

func yesNo(b bool) string {
	if b {
		return "да"
	}
	return "нет"
}

func at(u string) string {
	if u == "" {
		return ""
	}
	return "@" + u
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
