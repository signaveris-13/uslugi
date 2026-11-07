package botapp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/signaveris-13/uslugi/internal/store"
)

// fakeTelegram — подмена Bot API: записывает вызовы и отвечает «ок».
type fakeTelegram struct {
	mu    sync.Mutex
	calls []tgCall
	msgID int
}

type tgCall struct {
	Method string
	Fields map[string]string
}

func (f *fakeTelegram) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	method := parts[len(parts)-1]
	fields := map[string]string{}
	if err := r.ParseMultipartForm(10 << 20); err == nil {
		for k, v := range r.MultipartForm.Value {
			fields[k] = v[0]
		}
	} else {
		body, _ := io.ReadAll(r.Body)
		fields["_body"] = string(body)
	}
	f.mu.Lock()
	f.calls = append(f.calls, tgCall{Method: method, Fields: fields})
	f.msgID++
	id := f.msgID
	f.mu.Unlock()

	msg := fmt.Sprintf(`{"message_id":%d,"date":0,"chat":{"id":%s,"type":"private"}}`, id, orDefault(fields["chat_id"], "1"))
	var result string
	switch method {
	case "answerCallbackQuery":
		result = "true"
	case "sendMediaGroup":
		result = "[" + msg + "]"
	case "getMe":
		result = `{"id":1,"is_bot":true,"first_name":"Test","username":"test_bot"}`
	default:
		result = msg
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"result":%s}`, result)
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

type harness struct {
	t     *testing.T
	ctx   context.Context
	tg    *fakeTelegram
	b     *bot.Bot
	st    *store.Store
	pool  *pgxpool.Pool
	user  models.User
	clean []func()
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL не задан")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	tg := &fakeTelegram{}
	srv := httptest.NewServer(tg)
	st := store.New(pool)
	app := New(st, slog.New(slog.NewTextHandler(testLogOut(), nil)))
	b, err := bot.New("123:test", bot.WithServerURL(srv.URL), bot.WithSkipGetMe(),
		bot.WithNotAsyncHandlers(), bot.WithDefaultHandler(app.DefaultHandler))
	if err != nil {
		t.Fatal(err)
	}
	app.Register(b)
	app.SetBotUsername("test_bot")

	h := &harness{t: t, ctx: ctx, tg: tg, b: b, st: st, pool: pool,
		user: models.User{ID: 8_000_000_000 + rand.Int64N(1_000_000_000), FirstName: "Анна", Username: fmt.Sprintf("anna_test_%d", rand.IntN(1e6))}}
	t.Cleanup(func() {
		for i := len(h.clean) - 1; i >= 0; i-- {
			h.clean[i]()
		}
		h.cleanupUser()
		srv.Close()
		pool.Close()
	})
	return h
}

func (h *harness) cleanupUser() {
	ctx := context.Background()
	q := func(sql string) {
		if _, err := h.pool.Exec(ctx, sql, h.user.ID); err != nil {
			h.t.Logf("cleanup: %v", err)
		}
	}
	q(`UPDATE lead SET provider_id = NULL, listing_id = NULL WHERE provider_id IN (SELECT p.id FROM provider p JOIN users u ON u.id = p.user_id WHERE u.telegram_id = $1)`)
	q(`DELETE FROM listing WHERE provider_id IN (SELECT p.id FROM provider p JOIN users u ON u.id = p.user_id WHERE u.telegram_id = $1)`)
	q(`DELETE FROM provider WHERE user_id IN (SELECT id FROM users WHERE telegram_id = $1)`)
	q(`DELETE FROM event WHERE user_id IN (SELECT id FROM users WHERE telegram_id = $1)`)
	q(`DELETE FROM users WHERE telegram_id = $1`)
}

func (h *harness) chat() models.Chat { return models.Chat{ID: h.user.ID, Type: models.ChatTypePrivate} }

func (h *harness) sendText(text string) {
	h.b.ProcessUpdate(h.ctx, &models.Update{Message: &models.Message{
		ID: 1, From: &h.user, Chat: h.chat(), Text: text,
	}})
}

func (h *harness) sendPhoto(fileID, group string) {
	h.b.ProcessUpdate(h.ctx, &models.Update{Message: &models.Message{
		ID: 1, From: &h.user, Chat: h.chat(), MediaGroupID: group,
		Photo: []models.PhotoSize{{FileID: fileID + "_small"}, {FileID: fileID}},
	}})
}

// lastMarkup — кнопки последнего сообщения бота с клавиатурой.
func (h *harness) lastMarkup() (string, [][]models.InlineKeyboardButton) {
	h.tg.mu.Lock()
	defer h.tg.mu.Unlock()
	for i := len(h.tg.calls) - 1; i >= 0; i-- {
		c := h.tg.calls[i]
		if c.Fields["reply_markup"] == "" {
			continue
		}
		var m models.InlineKeyboardMarkup
		if err := json.Unmarshal([]byte(c.Fields["reply_markup"]), &m); err != nil {
			h.t.Fatalf("reply_markup: %v", err)
		}
		return c.Fields["text"], m.InlineKeyboard
	}
	h.t.Fatal("бот не прислал ни одной клавиатуры")
	return "", nil
}

func (h *harness) lastText() string {
	text, _ := h.lastMarkup()
	return text
}

// click нажимает кнопку, подпись которой содержит label.
func (h *harness) click(label string) {
	h.t.Helper()
	text, kb := h.lastMarkup()
	for _, r := range kb {
		for _, b := range r {
			if strings.Contains(b.Text, label) {
				h.b.ProcessUpdate(h.ctx, &models.Update{CallbackQuery: &models.CallbackQuery{
					ID: "cb", From: h.user, Data: b.CallbackData,
					Message: models.MaybeInaccessibleMessage{Type: models.MaybeInaccessibleMessageTypeMessage,
						Message: &models.Message{ID: 1, Chat: h.chat()}},
				}})
				return
			}
		}
	}
	var labels []string
	for _, r := range kb {
		for _, b := range r {
			labels = append(labels, b.Text)
		}
	}
	h.t.Fatalf("нет кнопки %q. Сообщение: %q\nКнопки: %v", label, text, labels)
}

func (h *harness) expectText(substr string) {
	h.t.Helper()
	if text := h.lastText(); !strings.Contains(text, substr) {
		h.t.Fatalf("ждали %q в сообщении бота, получили:\n%s", substr, text)
	}
}

func (h *harness) expectButton(substr string) {
	h.t.Helper()
	_, kb := h.lastMarkup()
	for _, r := range kb {
		for _, b := range r {
			if strings.Contains(b.Text, substr) {
				return
			}
		}
	}
	h.t.Fatalf("нет кнопки с %q", substr)
}

func (h *harness) listing() store.Listing {
	h.t.Helper()
	var id int64
	err := h.pool.QueryRow(h.ctx, `
		SELECT l.id FROM listing l JOIN provider p ON p.id = l.provider_id JOIN users u ON u.id = p.user_id
		WHERE u.telegram_id = $1 ORDER BY l.id DESC LIMIT 1`, h.user.ID).Scan(&id)
	if err != nil {
		h.t.Fatalf("объявление: %v", err)
	}
	l, err := h.st.GetListing(h.ctx, id, 0)
	if err != nil {
		h.t.Fatal(err)
	}
	return l
}

// Полный путь нового исполнителя: город → роль → форма → превью → правила → модерация.
func TestProviderFlow(t *testing.T) {
	h := newHarness(t)

	h.sendText("/start src_chat_nhatrang")
	h.expectText("В каком ты городе?")
	h.click("Нячанг")
	h.expectText("Что тебя сюда привело?")
	h.click("Я исполнитель")

	h.expectText("Шаг 1 из 9 · Категория")
	h.click("Красота")
	h.click("Маникюр и педикюр")
	h.expectText("Основная: <b>Маникюр и педикюр</b>")
	h.click("Ресницы и брови")
	h.click("Визаж")
	h.click("Депиляция") // третья дополнительная — сверх лимита
	h.expectText("не больше 2")
	h.click("Готово")

	h.expectText("Где работаешь")
	h.click("Готово") // Нячанг уже выбран по городу пользователя
	h.expectText("Название")
	h.sendText(strings.Repeat("я", 61))
	h.expectText("длиннее 60")
	h.sendText("Маникюр с покрытием")
	h.expectText("Описание")
	h.click("Назад")
	h.expectText("Сейчас: <i>Маникюр с покрытием</i>")
	h.click("Оставить как есть")
	h.sendText("Делаю маникюр с покрытием, приезжаю со своим набором.")

	h.expectText("Как указать цену?")
	h.click("«От …»")
	h.sendText("сколько-то")
	h.expectText("Не понял сумму")
	h.sendText("450к")
	h.click("Донги")

	h.expectText("Фото")
	h.sendPhoto("photo1", "album1")
	h.sendPhoto("photo2", "album1")
	h.click("Готово, фото: 2")

	h.expectText("Контакты")
	h.click("Телефон")
	h.sendText("+84 (905) 123-456")
	h.expectText("Контакты") // вернулись к контактам
	h.expectButton("Телефон: +84 905 123456")
	h.click("Готово")

	h.expectText("На каких языках")
	h.click("English")
	h.click("Готово")
	h.expectText("График")
	h.click("Пропустить")

	h.expectText("Так клиенты увидят объявление")
	h.expectText("от 450 000 ₫")
	h.expectText("Нячанг")

	// Исправляем название из превью — после шага возвращаемся к превью.
	h.click("Исправить")
	h.click("Название")
	h.sendText("Маникюр и педикюр с покрытием")
	h.expectText("Так клиенты увидят объявление")

	h.click("Отправить на модерацию")
	h.expectText("Правила Uslugi")
	h.click("Принимаю и отправляю")
	h.expectText("отправлено на модерацию")

	l := h.listing()
	if l.Status != store.ListingModeration {
		t.Fatalf("статус %q", l.Status)
	}
	if got := deref(l.Title); got != "Маникюр и педикюр с покрытием" {
		t.Fatalf("название %q", got)
	}
	if l.PrimaryCategory == nil || l.PrimaryCategory.Name != "Маникюр и педикюр" || len(l.ExtraCategories) != 2 {
		t.Fatalf("категории: %+v %+v", l.PrimaryCategory, l.ExtraCategories)
	}
	if l.PriceAmount == nil || *l.PriceAmount != 450000 || deref(l.PriceType) != "from" || deref(l.Currency) != "VND" {
		t.Fatalf("цена: %v %v %v", l.PriceAmount, l.PriceType, l.Currency)
	}
	if len(l.PhotoFileIDs) != 2 || l.PhotoFileIDs[0] != "photo1" {
		t.Fatalf("фото: %v", l.PhotoFileIDs)
	}
	if deref(l.ContactPhone) != "+84 905 123456" || deref(l.ContactTelegram) != h.user.Username {
		t.Fatalf("контакты: тел %q, tg %q", deref(l.ContactPhone), deref(l.ContactTelegram))
	}
	if strings.Join(l.Languages, ",") != "ru,en" {
		t.Fatalf("языки: %v", l.Languages)
	}

	// Повторная отправка не нужна, а в «Моих объявлениях» видно статус.
	h.sendText("/menu")
	h.click("Мои объявления")
	h.expectText("Мои объявления")
	_, kb := h.lastMarkup()
	if !strings.Contains(kb[0][0].Text, "На модерации") {
		t.Fatalf("в списке: %q", kb[0][0].Text)
	}

	var source string
	if err := h.pool.QueryRow(h.ctx, `SELECT coalesce(start_source, '') FROM users WHERE telegram_id = $1`, h.user.ID).Scan(&source); err != nil || source != "chat_nhatrang" {
		t.Fatalf("метка источника: %q %v", source, err)
	}
}

// Исполнитель приходит по ссылке-приглашению из CRM: лид связывается, черновик предзаполнен.
func TestInviteFlow(t *testing.T) {
	h := newHarness(t)

	if err := h.st.BootstrapTeam(h.ctx, []int64{h.user.ID + 1}); err != nil {
		t.Fatal(err)
	}
	m, _ := h.st.ActiveTeamMemberByTelegramID(h.ctx, h.user.ID+1)
	h.clean = append(h.clean, func() {
		ctx := context.Background()
		h.pool.Exec(ctx, `DELETE FROM audit_log WHERE actor_id = $1`, m.ID)
		h.pool.Exec(ctx, `DELETE FROM lead WHERE assignee_id = $1`, m.ID)
		h.pool.Exec(ctx, `DELETE FROM team_member WHERE id = $1`, m.ID)
	})

	var catID, cityID int64
	h.pool.QueryRow(h.ctx, `SELECT id FROM category WHERE slug = 'cakes'`).Scan(&catID)
	h.pool.QueryRow(h.ctx, `SELECT id FROM city WHERE slug = 'da-nang'`).Scan(&cityID)
	leadID, err := h.st.CreateLead(h.ctx, m.ID, store.LeadInput{
		Name: "Мария, торты", CategoryID: catID, CityID: cityID, Phone: "+84 111",
		Summary: "Торты на заказ, бенто, капкейки.", Status: store.LeadInvited,
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := h.st.EnsureInviteToken(h.ctx, m.ID, leadID)
	if err != nil {
		t.Fatal(err)
	}

	h.sendText("/start inv_" + token)
	h.expectText("Так клиенты увидят объявление")
	h.expectText("Торты на заказ")
	h.expectText("Дананг")
	h.expectText("Осталось заполнить: цена")

	lead, _ := h.st.GetLead(h.ctx, leadID)
	if lead.ProviderID == nil || lead.Status != store.LeadInterested {
		t.Fatalf("лид после перехода: provider %v, статус %q", lead.ProviderID, lead.Status)
	}

	h.click("Исправить")
	h.click("Цена")
	h.click("По договорённости")
	h.expectText("по договорённости")
	h.click("Отправить на модерацию")
	h.click("Принимаю и отправляю")
	h.expectText("отправлено на модерацию")

	lead, _ = h.st.GetLead(h.ctx, leadID)
	l := h.listing()
	if lead.Status != store.LeadListingCreated || lead.ListingID == nil || *lead.ListingID != l.ID {
		t.Fatalf("лид после отправки: статус %q, listing %v (объявление %d)", lead.Status, lead.ListingID, l.ID)
	}
}

func TestParseAmount(t *testing.T) {
	cases := map[string]float64{
		"450000": 450000, "450 000": 450000, "450.000": 450000, "450к": 450000, "450k": 450000,
		"1,5 млн": 1500000, "25": 25, "25.5": 25.5, "1.200.000 ₫": 1200000,
	}
	for in, want := range cases {
		if got, ok := parseAmount(in); !ok || got != want {
			t.Errorf("parseAmount(%q) = %v, %v; ждали %v", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "abc", "-5", "0"} {
		if _, ok := parseAmount(bad); ok {
			t.Errorf("parseAmount(%q) должен не пройти", bad)
		}
	}
	if got := formatAmount(1500000, "VND"); got != "1 500 000 ₫" {
		t.Errorf("formatAmount: %q", got)
	}
	if got := normalizeContact("instagram", "https://www.instagram.com/anna.nails/?hl=ru"); got != "anna.nails" {
		t.Errorf("instagram: %q", got)
	}
}

func testLogOut() io.Writer {
	if os.Getenv("BOT_TEST_LOG") != "" {
		return os.Stderr
	}
	return io.Discard
}

// publishedListing — объявление другого исполнителя, уже прошедшее модерацию.
func (h *harness) publishedListing(title, subSlug string) int64 {
	h.t.Helper()
	ctx := h.ctx
	tg := store.TelegramUser{ID: 7_500_000_000 + rand.Int64N(100_000_000), Username: fmt.Sprintf("master_%d", rand.IntN(1e6)), FirstName: "Мастер"}
	u, _, err := h.st.UpsertBotUser(ctx, tg, "")
	if err != nil {
		h.t.Fatal(err)
	}
	if err := h.st.BootstrapTeam(ctx, []int64{tg.ID + 1}); err != nil {
		h.t.Fatal(err)
	}
	m, _ := h.st.ActiveTeamMemberByTelegramID(ctx, tg.ID+1)
	h.clean = append(h.clean, func() {
		ctx := context.Background()
		h.pool.Exec(ctx, `DELETE FROM complaint WHERE listing_id IN (SELECT l.id FROM listing l JOIN provider p ON p.id = l.provider_id WHERE p.user_id = $1)`, u.ID)
		h.pool.Exec(ctx, `DELETE FROM event WHERE object_type = 'listing' AND object_id IN (SELECT l.id FROM listing l JOIN provider p ON p.id = l.provider_id WHERE p.user_id = $1)`, u.ID)
		h.pool.Exec(ctx, `DELETE FROM listing WHERE provider_id IN (SELECT id FROM provider WHERE user_id = $1)`, u.ID)
		h.pool.Exec(ctx, `DELETE FROM provider WHERE user_id = $1`, u.ID)
		h.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, u.ID)
		h.pool.Exec(ctx, `DELETE FROM audit_log WHERE actor_id = $1`, m.ID)
		h.pool.Exec(ctx, `DELETE FROM team_member WHERE id = $1`, m.ID)
	})
	p, _ := h.st.EnsureProvider(ctx, u, "Мастер")
	var catID, cityID int64
	h.pool.QueryRow(ctx, `SELECT id FROM category WHERE slug = $1`, subSlug).Scan(&catID)
	h.pool.QueryRow(ctx, `SELECT id FROM city WHERE slug = 'nha-trang'`).Scan(&cityID)
	id, err := h.st.CreateListingDraft(ctx, p.ID, store.ListingDraftInput{
		CategoryID: catID, CityID: cityID, Title: title, Description: "Описание услуги", Telegram: tg.Username,
		WhatsApp: "+84 905 111 222", Currency: "VND",
	})
	if err != nil {
		h.t.Fatal(err)
	}
	h.st.SetPriceType(ctx, id, "fixed")
	h.st.SetPriceAmount(ctx, id, 300000)
	if err := h.st.SubmitListing(ctx, id, p.ID); err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.st.ApproveListing(ctx, m.ID, id); err != nil {
		h.t.Fatal(err)
	}
	return id
}

func (h *harness) events(typ string, listingID int64) int {
	var n int
	h.pool.QueryRow(h.ctx, `SELECT count(*) FROM event e JOIN users u ON u.id = e.user_id
		WHERE u.telegram_id = $1 AND e.type = $2 AND e.object_id = $3`, h.user.ID, typ, listingID).Scan(&n)
	return n
}

func (h *harness) lastCall() tgCall {
	h.tg.mu.Lock()
	defer h.tg.mu.Unlock()
	return h.tg.calls[len(h.tg.calls)-1]
}

// Клиент: каталог → список → карточка → контакт, избранное, поделиться, жалоба; deep link.
func TestClientFlow(t *testing.T) {
	h := newHarness(t)
	title := fmt.Sprintf("Брови и ресницы %d", rand.IntN(1e6))
	id := h.publishedListing(title, "brows-lashes")

	h.sendText("/start")
	h.click("Нячанг")
	h.click("Ищу услугу")
	h.expectText("Каталог · Нячанг")
	h.click("Красота")
	// В «Красоте» могут быть объявления из других тестов: тогда сначала подкатегории.
	if text := h.lastText(); strings.Contains(text, "Что именно ищешь?") {
		h.click("Ресницы и брови")
	}
	h.expectText(title)
	h.click(title)

	h.expectText("<b>" + title + "</b>")
	h.expectText("300 000 ₫")
	if h.events("listing_viewed", id) != 1 {
		t.Fatal("просмотр не записан")
	}

	h.click("Написать в Telegram")
	h.expectText("Скажи, что нашёл объявление")
	_, kb := h.lastMarkup()
	if !strings.HasPrefix(kb[0][0].URL, "https://t.me/master_") {
		t.Fatalf("ссылка на чат: %q", kb[0][0].URL)
	}
	if h.events("contact_clicked", id) != 1 {
		t.Fatal("клик по контакту не записан")
	}

	// Возвращаемся к карточке по deep link и добавляем в избранное.
	h.sendText(fmt.Sprintf("/start l_%d", id))
	h.expectText("<b>" + title + "</b>")
	h.click("В избранное")
	h.expectButton("❤️ В избранном")
	h.sendText("/menu")
	h.click("Избранное")
	h.expectButton(title)

	h.sendText(fmt.Sprintf("/start l_%d", id))
	h.click("Поделиться")
	h.expectText(fmt.Sprintf("https://t.me/test_bot?start=l_%d", id))

	h.sendText(fmt.Sprintf("/start l_%d", id))
	h.click("WhatsApp")
	_, kb = h.lastMarkup()
	if kb[0][0].URL != "https://wa.me/84905111222" {
		t.Fatalf("WhatsApp: %q", kb[0][0].URL)
	}

	h.sendText(fmt.Sprintf("/start l_%d", id))
	h.click("Пожаловаться")
	h.click("Неактуально")
	h.sendText("Мастер уехал из города")
	h.expectText("Жалоба ушла модератору")
	var reason, comment string
	if err := h.pool.QueryRow(h.ctx, `SELECT reason, comment FROM complaint WHERE listing_id = $1`, id).Scan(&reason, &comment); err != nil || reason != "outdated" || comment != "Мастер уехал из города" {
		t.Fatalf("жалоба: %q %q %v", reason, comment, err)
	}
	// Повторная жалоба на то же объявление — не дублируем.
	h.sendText(fmt.Sprintf("/start l_%d", id))
	h.click("Пожаловаться")
	h.click("Другое")
	h.click("Отправить без комментария")
	h.expectText("уже пожаловался")
}
