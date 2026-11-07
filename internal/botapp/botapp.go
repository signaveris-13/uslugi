// Package botapp — сценарии Telegram-бота: онбординг, меню, форма объявления
// исполнителя, приглашения из CRM и подтверждение входа в админку.
package botapp

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/signaveris-13/uslugi/internal/media"
	"github.com/signaveris-13/uslugi/internal/store"
)

type App struct {
	store       *store.Store
	log         *slog.Logger
	botUsername string // для ссылок «Поделиться»
	media       *media.Store
	publicURL   string // https-адрес сервера: кнопки Mini App; пусто — без них

	// Альбом приходит пачкой отдельных сообщений: на первое отвечаем,
	// а на следующие обновляем тот же ответ со счётчиком фото.
	mu     sync.Mutex
	albums map[string]albumReply
}

type albumReply struct {
	at    time.Time
	msgID int
}

func New(s *store.Store, log *slog.Logger) *App {
	return &App{store: s, log: log, albums: map[string]albumReply{}}
}

// SetMedia — хранилище фото, загруженных через приложение.
func (a *App) SetMedia(m *media.Store) { a.media = m }

// SetPublicURL включает кнопки Mini App. Telegram открывает только https.
func (a *App) SetPublicURL(u string) {
	if strings.HasPrefix(u, "https://") {
		a.publicURL = strings.TrimSuffix(u, "/")
	}
}

// appURL — адрес Mini App; listingID > 0 — сразу открыть форму объявления.
func (a *App) appURL(listingID int64) string {
	if listingID > 0 {
		return a.publicURL + "/app?listing=" + strconv.FormatInt(listingID, 10)
	}
	return a.publicURL + "/app"
}

func appBtn(text, u string) btnT { return btnT{Text: text, WebApp: &models.WebAppInfo{URL: u}} }

// SetupMenuButton ставит кнопку «Объявления» слева от поля ввода — она открывает Mini App.
func (a *App) SetupMenuButton(ctx context.Context, b *bot.Bot) {
	if a.publicURL == "" {
		return
	}
	_, err := b.SetChatMenuButton(ctx, &bot.SetChatMenuButtonParams{
		MenuButton: models.MenuButtonWebApp{Type: models.MenuButtonTypeWebApp, Text: "Объявления", WebApp: models.WebAppInfo{URL: a.appURL(0)}},
	})
	if err != nil {
		a.log.Warn("кнопка меню", "err", err)
	}
}

// SetBotUsername — username бота без @, для deep link в «Поделиться».
func (a *App) SetBotUsername(u string) { a.botUsername = strings.TrimPrefix(u, "@") }

// Register подключает обработчики. Всё, что не /start и не вход, идёт в onUpdate.
func (a *App) Register(b *bot.Bot) {
	b.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypePrefix, a.onStart)
	b.RegisterHandler(bot.HandlerTypeCallbackQueryData, loginCallbackPrefix, bot.MatchTypePrefix, a.onLoginConfirm)
}

// DefaultHandler — для bot.WithDefaultHandler.
func (a *App) DefaultHandler(ctx context.Context, b *bot.Bot, u *models.Update) {
	a.onUpdate(ctx, b, u)
}

// turn — один апдейт: кто пишет, в какой чат и какое сообщение можно отредактировать.
type turn struct {
	ctx    context.Context
	b      *bot.Bot
	chatID int64
	editID int // сообщение с кнопками, на которое нажали; 0 — отвечаем новым
	tg     store.TelegramUser
	user   store.User
}

func tgUser(u models.User) store.TelegramUser {
	return store.TelegramUser{ID: u.ID, Username: u.Username, FirstName: u.FirstName, LastName: u.LastName, LanguageCode: u.LanguageCode}
}

func (a *App) newTurn(ctx context.Context, b *bot.Bot, from models.User, chatID int64, editID int, startSource string) (*turn, bool, error) {
	tg := tgUser(from)
	user, created, err := a.store.UpsertBotUser(ctx, tg, startSource)
	if err != nil {
		return nil, false, err
	}
	return &turn{ctx: ctx, b: b, chatID: chatID, editID: editID, tg: tg, user: user}, created, nil
}

func (a *App) onStart(ctx context.Context, b *bot.Bot, u *models.Update) {
	msg := u.Message
	if msg == nil || msg.From == nil {
		return
	}
	payload := strings.TrimSpace(strings.TrimPrefix(msg.Text, "/start"))

	// Вход в админку не заводит пользователя бота.
	if token, ok := strings.CutPrefix(payload, "login_"); ok {
		a.askLoginConfirm(ctx, b, msg, token)
		return
	}

	source, _ := strings.CutPrefix(payload, "src_")
	if source == payload {
		source = ""
	}
	t, created, err := a.newTurn(ctx, b, *msg.From, msg.Chat.ID, 0, source)
	if err != nil {
		a.log.Error("пользователь бота", "err", err)
		return
	}
	if t.user.IsBlocked {
		return
	}
	if created {
		a.event(t, "bot_start", "", 0, map[string]any{"payload": payload})
	}
	a.log.Info("start", "user_id", t.user.ID, "payload", payload)

	if token, ok := strings.CutPrefix(payload, "inv_"); ok {
		a.startFromInvite(t, token)
		return
	}
	if a.startFromDeepLink(t, payload) {
		return
	}
	if t.user.CityID == nil {
		a.askCity(t, "Привет! Это Uslugi — каталог услуг русскоязычных мастеров.\n\nВ каком ты городе?")
		return
	}
	a.showMenu(t, "")
}

func (a *App) onUpdate(ctx context.Context, b *bot.Bot, u *models.Update) {
	switch {
	case u.CallbackQuery != nil:
		cq := u.CallbackQuery
		// Сразу убираем «часики» на кнопке.
		if _, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: cq.ID}); err != nil {
			a.log.Warn("answer callback", "err", err)
		}
		m := cq.Message.Message
		if m == nil {
			return
		}
		t, _, err := a.newTurn(ctx, b, cq.From, m.Chat.ID, m.ID, "")
		if err != nil {
			a.log.Error("пользователь бота", "err", err)
			return
		}
		if t.user.IsBlocked {
			return
		}
		a.onCallback(t, cq.Data)

	case u.Message != nil && u.Message.From != nil:
		msg := u.Message
		if msg.Chat.Type != models.ChatTypePrivate {
			return
		}
		t, _, err := a.newTurn(ctx, b, *msg.From, msg.Chat.ID, 0, "")
		if err != nil {
			a.log.Error("пользователь бота", "err", err)
			return
		}
		if t.user.IsBlocked {
			return
		}
		a.onMessage(t, msg)
	}
}

func (a *App) onCallback(t *turn, data string) {
	action, arg, _ := strings.Cut(data, ":")
	switch action {
	case "city":
		a.onCityChosen(t, parseID(arg))
	case "role":
		a.onRoleChosen(t, arg)
	case "m":
		a.onMenu(t, arg)
	case "f":
		a.onForm(t, arg)
	case "c":
		a.onCatalog(t, arg)
	default:
		a.log.Warn("неизвестная кнопка", "data", data)
	}
}

func (a *App) onMessage(t *turn, msg *models.Message) {
	st, err := a.store.GetBotState(t.ctx, t.user.ID)
	if err != nil {
		a.fail(t, err)
		return
	}
	if len(msg.Photo) > 0 {
		a.onPhoto(t, st, msg)
		return
	}
	text := strings.TrimSpace(msg.Text)
	if text == "/menu" || text == "/cancel" {
		a.showMenu(t, "")
		return
	}
	if text != "" && st.Step == stepComplaint {
		a.onComplaintText(t, text)
		return
	}
	if text != "" && st.ListingID != nil && isTextStep(st.Step) {
		a.onFormText(t, st, text)
		return
	}
	a.showMenu(t, "Не понял сообщение. Вот меню:")
}

// Отправка ------------------------------------------------------------------

type (
	btnT     = models.InlineKeyboardButton
	keyboard [][]btnT
)

func btn(text, data string) btnT { return btnT{Text: text, CallbackData: data} }

// urlBtn — кнопка-ссылка: открывает чат, WhatsApp или профиль без участия бота.
func urlBtn(text, u string) btnT { return btnT{Text: text, URL: u} }

func row(buttons ...btnT) []btnT { return buttons }

// show редактирует сообщение, на кнопку которого нажали, или отправляет новое.
func (a *App) show(t *turn, text string, kb keyboard) {
	var markup models.ReplyMarkup
	if kb != nil {
		markup = &models.InlineKeyboardMarkup{InlineKeyboard: kb}
	}
	if t.editID != 0 {
		_, err := t.b.EditMessageText(t.ctx, &bot.EditMessageTextParams{
			ChatID: t.chatID, MessageID: t.editID, Text: text, ParseMode: models.ParseModeHTML,
			ReplyMarkup: markup, LinkPreviewOptions: noPreview(),
		})
		if err == nil {
			return
		}
		// Сообщение могло устареть или совпасть по тексту — тогда просто шлём новое.
		if strings.Contains(err.Error(), "message is not modified") {
			return
		}
		a.log.Warn("edit message", "err", err)
	}
	a.send(t, text, kb)
}

// send всегда отправляет новое сообщение и возвращает его ID (0 при ошибке).
func (a *App) send(t *turn, text string, kb keyboard) int {
	params := &bot.SendMessageParams{
		ChatID: t.chatID, Text: text, ParseMode: models.ParseModeHTML, LinkPreviewOptions: noPreview(),
	}
	if kb != nil {
		params.ReplyMarkup = &models.InlineKeyboardMarkup{InlineKeyboard: kb}
	}
	t.editID = 0
	m, err := t.b.SendMessage(t.ctx, params)
	if err != nil {
		a.log.Error("send message", "err", err)
		return 0
	}
	return m.ID
}

func noPreview() *models.LinkPreviewOptions {
	yes := true
	return &models.LinkPreviewOptions{IsDisabled: &yes}
}

func (a *App) fail(t *turn, err error) {
	a.log.Error("бот", "err", err, "user_id", t.user.ID)
	a.send(t, "Что-то пошло не так. Попробуй ещё раз или открой /menu.", nil)
}

func (a *App) event(t *turn, typ, objectType string, objectID int64, props map[string]any) {
	if err := a.store.LogEvent(t.ctx, typ, t.user.ID, objectType, objectID, "", props); err != nil {
		a.log.Warn("событие", "type", typ, "err", err)
	}
}

func parseID(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func isNotFound(err error) bool { return errors.Is(err, store.ErrNotFound) }
