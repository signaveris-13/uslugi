// Package botapp — сценарии Telegram-бота.
package botapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/signaveris-13/uslugi/internal/store"
)

type App struct {
	store *store.Store
	log   *slog.Logger
}

func New(s *store.Store, log *slog.Logger) *App {
	return &App{store: s, log: log}
}

// Register подключает обработчики к боту.
func (a *App) Register(b *bot.Bot) {
	b.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypePrefix, a.onStart)
	b.RegisterHandler(bot.HandlerTypeCallbackQueryData, loginCallbackPrefix, bot.MatchTypePrefix, a.onLoginConfirm)
}

func (a *App) onStart(ctx context.Context, b *bot.Bot, u *models.Update) {
	msg := u.Message
	if msg == nil || msg.From == nil {
		return
	}
	// Всё после "/start " — payload deep link: login_<token>, inv_<token>, l_<id>, src_<tag>…
	payload := strings.TrimSpace(strings.TrimPrefix(msg.Text, "/start"))
	a.log.Info("start", "user_id", msg.From.ID, "username", msg.From.Username, "payload", payload)

	if token, ok := strings.CutPrefix(payload, "login_"); ok {
		a.askLoginConfirm(ctx, b, msg, token)
		return
	}

	a.send(ctx, b, msg.Chat.ID, fmt.Sprintf(
		"Привет! Это тестовый бот Uslugi — скоро здесь будет каталог услуг.\n\nТвой Telegram ID: %d", msg.From.ID))
}

const loginCallbackPrefix = "login:"

// askLoginConfirm — первый шаг входа в админку: явное подтверждение кнопкой,
// чтобы случайный переход по чужой ссылке не открыл кому-то админку.
func (a *App) askLoginConfirm(ctx context.Context, b *bot.Bot, msg *models.Message, token string) {
	if _, err := a.store.ActiveTeamMemberByTelegramID(ctx, msg.From.ID); err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			a.log.Error("проверка команды", "err", err)
		}
		a.send(ctx, b, msg.Chat.ID, "У этого аккаунта нет доступа к админке Uslugi.")
		return
	}
	_, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: msg.Chat.ID,
		Text:   "Войти в админку Uslugi в браузере, где ты нажал «Войти»?\n\nЕсли ты ничего не открывал — просто проигнорируй это сообщение.",
		ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
			{Text: "✅ Да, войти", CallbackData: loginCallbackPrefix + token},
		}}},
	})
	if err != nil {
		a.log.Error("не удалось ответить", "err", err)
	}
}

func (a *App) onLoginConfirm(ctx context.Context, b *bot.Bot, u *models.Update) {
	cq := u.CallbackQuery
	if cq == nil {
		return
	}
	token := strings.TrimPrefix(cq.Data, loginCallbackPrefix)

	text := "Вход подтверждён. Вернись в браузер — страница откроется сама."
	m, err := a.store.ConfirmLogin(ctx, token, cq.From.ID)
	switch {
	case errors.Is(err, store.ErrNotTeamMember):
		text = "У этого аккаунта нет доступа к админке Uslugi."
	case errors.Is(err, store.ErrLoginExpired):
		text = "Ссылка для входа устарела. Нажми «Войти» в браузере ещё раз."
	case err != nil:
		a.log.Error("подтверждение входа", "err", err)
		text = "Что-то пошло не так. Попробуй ещё раз."
	default:
		a.log.Info("вход в админку подтверждён", "team_member_id", m.ID)
		if strings.HasPrefix(m.Name, "Админ ") && cq.From.FirstName != "" {
			name := strings.TrimSpace(cq.From.FirstName + " " + cq.From.LastName)
			if err := a.store.UpdateTeamMemberName(ctx, m.ID, name); err != nil {
				a.log.Error("имя участника команды", "err", err)
			}
		}
	}

	if _, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: cq.ID}); err != nil {
		a.log.Error("answer callback", "err", err)
	}
	if m := cq.Message.Message; m != nil {
		_, err := b.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: m.Chat.ID, MessageID: m.ID, Text: text})
		if err != nil {
			a.log.Error("edit message", "err", err)
		}
	}
}

func (a *App) send(ctx context.Context, b *bot.Bot, chatID int64, text string) {
	if _, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: text}); err != nil {
		a.log.Error("не удалось ответить", "err", err)
	}
}
