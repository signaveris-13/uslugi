// Package tgclient — то, что веб-серверу нужно от Telegram: уведомить пользователя
// бота и получить файл (фото объявления) по file_id.
package tgclient

import (
	"strings"
	"context"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type Client struct {
	b *bot.Bot
}

func New(token string) (*Client, error) {
	b, err := bot.New(token, bot.WithSkipGetMe())
	if err != nil {
		return nil, err
	}
	return &Client{b: b}, nil
}

// Username — username бота без @.
func (c *Client) Username(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	me, err := c.b.GetMe(ctx)
	if err != nil {
		return "", err
	}
	return me.Username, nil
}

// Notify пишет пользователю бота HTML-сообщение с одной необязательной кнопкой.
func (c *Client) Notify(ctx context.Context, chatID int64, html, buttonText, buttonData string) error {
	p := &bot.SendMessageParams{ChatID: chatID, Text: html, ParseMode: models.ParseModeHTML}
	if buttonText != "" {
		b := models.InlineKeyboardButton{Text: buttonText, CallbackData: buttonData}
		// https-ссылка — кнопка открывает Mini App на нужном экране.
		if strings.HasPrefix(buttonData, "https://") {
			b = models.InlineKeyboardButton{Text: buttonText, WebApp: &models.WebAppInfo{URL: buttonData}}
		}
		p.ReplyMarkup = &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{b}}}
	}
	_, err := c.b.SendMessage(ctx, p)
	return err
}

// FileURL — временная ссылка на скачивание файла. В ней токен бота: наружу не отдаём.
func (c *Client) FileURL(ctx context.Context, fileID string) (string, error) {
	f, err := c.b.GetFile(ctx, &bot.GetFileParams{FileID: fileID})
	if err != nil {
		return "", err
	}
	return c.b.FileDownloadLink(f), nil
}
