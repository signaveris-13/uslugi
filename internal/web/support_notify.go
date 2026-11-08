package web

import (
	"context"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/signaveris-13/uslugi/internal/store"
)

// notifySupport пересылает обращение из Mini App в Telegram поддержки (SUPPORT_CHAT):
// новое обращение целиком, дальше — каждое новое сообщение пользователя.
// Отвечают по-прежнему из админки, кнопка ведёт туда.
// Отправка идёт в фоне: пользователь не ждёт Telegram.
func (s *Server) notifySupport(ticketID int64, text string, first bool) {
	if s.tg == nil || s.supportChat == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		t, _, err := s.store.Ticket(ctx, ticketID, 0)
		if err != nil {
			s.log.Warn("поддержка: обращение для уведомления", "err", err, "ticket", ticketID)
			return
		}
		chat, ok := s.supportTarget(ctx)
		if !ok {
			return
		}
		button, link := "", ""
		if s.publicURL != "" {
			button, link = "Ответить в админке", s.publicURL+"/support/"+strconv.FormatInt(t.ID, 10)
		}
		if err := s.tg.NotifyChat(ctx, chat, supportNoticeHTML(t, text, first, s.envLabel), button, link); err != nil {
			s.log.Warn("поддержка: уведомление в Telegram", "err", err, "chat", s.supportChat, "ticket", ticketID)
		}
	}()
}

// supportTarget — куда писать. @username сначала ищем среди пользователей бота
// (аккаунт поддержки должен один раз нажать /start), иначе считаем его каналом или группой.
func (s *Server) supportTarget(ctx context.Context) (string, bool) {
	c := s.supportChat
	if !strings.HasPrefix(c, "@") {
		return c, true
	}
	id, err := s.store.TelegramIDByUsername(ctx, c)
	if err == nil {
		return strconv.FormatInt(id, 10), true
	}
	if err != store.ErrNotFound {
		s.log.Warn("поддержка: поиск аккаунта", "err", err)
	}
	return c, true
}

func supportNoticeHTML(t store.SupportTicket, text string, first bool, env string) string {
	var b strings.Builder
	if env != "" {
		b.WriteString("[" + html.EscapeString(env) + "] ")
	}
	if first {
		b.WriteString("🆘 <b>Новое обращение #" + strconv.FormatInt(t.ID, 10) + "</b>")
	} else {
		b.WriteString("💬 <b>Новое сообщение в обращении #" + strconv.FormatInt(t.ID, 10) + "</b>")
	}
	if l := store.SupportTopicLabels[t.Topic]; l != "" {
		b.WriteString(" · " + html.EscapeString(l))
	}
	b.WriteString("\nОт: ")
	if t.UserName != nil && *t.UserName != "" {
		b.WriteString(html.EscapeString(*t.UserName) + " ")
	}
	if t.Username != nil && *t.Username != "" {
		b.WriteString("@" + html.EscapeString(*t.Username) + " ")
	}
	b.WriteString(`(<a href="tg://user?id=` + strconv.FormatInt(t.TelegramID, 10) + `">профиль</a>)`)
	if t.ListingID != nil {
		b.WriteString("\nОбъявление #" + strconv.FormatInt(*t.ListingID, 10))
		if t.ListingTitle != nil && *t.ListingTitle != "" {
			b.WriteString(" «" + html.EscapeString(*t.ListingTitle) + "»")
		}
	}
	b.WriteString("\n\n" + html.EscapeString(strings.TrimSpace(text)))
	return b.String()
}
