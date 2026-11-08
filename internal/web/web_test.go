package web

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/signaveris-13/uslugi/internal/media"
	"github.com/signaveris-13/uslugi/internal/store"
)

const testBotToken = "123456:TEST-token"

type sentNotice struct {
	ChatID int64
	Text   string
}

type fakeTelegram struct {
	mu       sync.Mutex
	sent     []sentNotice
	chatSent []chatNotice
	fileURL  string
}

type chatNotice struct {
	Chat, Text, Button string
}

func (f *fakeTelegram) NotifyChat(_ context.Context, chat, html, _, buttonURL string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chatSent = append(f.chatSent, chatNotice{chat, html, buttonURL})
	return nil
}

// waitChat ждёт n сообщений в чат поддержки: они уходят в фоне.
func (f *fakeTelegram) waitChat(t *testing.T, n int) []chatNotice {
	t.Helper()
	for i := 0; i < 100; i++ {
		f.mu.Lock()
		if len(f.chatSent) >= n {
			out := append([]chatNotice(nil), f.chatSent...)
			f.mu.Unlock()
			return out
		}
		f.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("в чат поддержки не пришло %d сообщений", n)
	return nil
}

func (f *fakeTelegram) Notify(_ context.Context, chatID int64, html, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentNotice{chatID, html})
	return nil
}

func (f *fakeTelegram) FileURL(context.Context, string) (string, error) { return f.fileURL, nil }

func (f *fakeTelegram) last(t *testing.T) sentNotice {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		t.Fatal("уведомлений не было")
	}
	return f.sent[len(f.sent)-1]
}

type env struct {
	t    *testing.T
	ctx  context.Context
	st   *store.Store
	pool *pgxpool.Pool
	srv  *httptest.Server
	tg   *fakeTelegram
	sid  string
	c    *http.Client
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL не задан")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(pool)

	img := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("\xff\xd8\xff fake jpeg"))
	}))
	tg := &fakeTelegram{fileURL: img.URL + "/file.jpg"}
	ms, err := media.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(st, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		Telegram: tg, BotUsername: "test_bot", Media: ms, BotToken: testBotToken,
		PublicURL: "https://uslugi.test", SupportChat: "-1001234567890",
	}).Routes())

	// Участник команды и сессия — как после входа через бота.
	tgID := 9_100_000_000 + rand.Int64N(100_000_000)
	if err := st.BootstrapTeam(ctx, []int64{tgID}); err != nil {
		t.Fatal(err)
	}
	m, _ := st.ActiveTeamMemberByTelegramID(ctx, tgID)
	token, _ := st.CreateLogin(ctx)
	if _, err := st.ConfirmLogin(ctx, token, tgID); err != nil {
		t.Fatal(err)
	}
	_, sid, err := st.ExchangeLogin(ctx, token)
	if err != nil {
		t.Fatal(err)
	}

	e := &env{t: t, ctx: ctx, st: st, pool: pool, srv: srv, tg: tg, sid: sid,
		c: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM audit_log WHERE actor_id = $1`, m.ID)
		pool.Exec(ctx, `DELETE FROM admin_session WHERE team_member_id = $1`, m.ID)
		pool.Exec(ctx, `DELETE FROM admin_login WHERE team_member_id = $1`, m.ID)
		pool.Exec(ctx, `DELETE FROM team_member WHERE id = $1`, m.ID)
		srv.Close()
		img.Close()
		pool.Close()
	})
	return e
}

func (e *env) do(method, path string, form url.Values) (int, string, http.Header) {
	e.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: e.sid})
	resp, err := e.c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

// submittedListing — исполнитель с объявлением на модерации.
func (e *env) submittedListing(title string) (int64, int64) {
	e.t.Helper()
	ctx := e.ctx
	tg := store.TelegramUser{ID: 6_000_000_000 + rand.Int64N(1_000_000_000), Username: fmt.Sprintf("web_test_%d", rand.IntN(1e6)), FirstName: "Анна"}
	u, _, err := e.st.UpsertBotUser(ctx, tg, "")
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() {
		ctx := context.Background()
		e.pool.Exec(ctx, `DELETE FROM listing WHERE provider_id IN (SELECT id FROM provider WHERE user_id = $1)`, u.ID)
		e.pool.Exec(ctx, `DELETE FROM provider WHERE user_id = $1`, u.ID)
		e.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, u.ID)
	})
	p, _ := e.st.EnsureProvider(ctx, u, "Анна")
	var catID, cityID int64
	e.pool.QueryRow(ctx, `SELECT id FROM category WHERE slug = 'manicure'`).Scan(&catID)
	e.pool.QueryRow(ctx, `SELECT id FROM city WHERE slug = 'nha-trang'`).Scan(&cityID)
	id, err := e.st.CreateListingDraft(ctx, p.ID, store.ListingDraftInput{
		CategoryID: catID, CityID: cityID, Title: title, Description: "Маникюр с покрытием", Telegram: tg.Username, Currency: "VND",
	})
	if err != nil {
		e.t.Fatal(err)
	}
	e.st.SetPriceType(ctx, id, "from")
	e.st.SetPriceAmount(ctx, id, 450000)
	if _, _, err := e.st.AddListingPhoto(ctx, id, "AgACAgIAAxkBAAIBc2VfakeFileId"); err != nil {
		e.t.Fatal(err)
	}
	if err := e.st.SubmitListing(ctx, id, p.ID); err != nil {
		e.t.Fatal(err)
	}
	return id, tg.ID
}

func TestModerationPages(t *testing.T) {
	e := newEnv(t)
	a, chatA := e.submittedListing("Маникюр Анны")
	b, chatB := e.submittedListing("Маникюр второй")

	code, body, _ := e.do("GET", "/listings", nil)
	if code != 200 || !strings.Contains(body, "Маникюр Анны") || !strings.Contains(body, `class="nav-badge"`) {
		t.Fatalf("очередь: %d\n%s", code, body)
	}

	code, body, _ = e.do("GET", fmt.Sprintf("/listings/%d", a), nil)
	for _, want := range []string{"Одобрить и опубликовать", "от 450 000 ₫", "Нячанг", "/media/tg/AgACAgIAAxkBAAIBc2VfakeFileId"} {
		if code != 200 || !strings.Contains(body, want) {
			t.Fatalf("карточка: нет %q (код %d)", want, code)
		}
	}

	// Одобрение: статус, уведомление исполнителю.
	code, _, h := e.do("POST", fmt.Sprintf("/listings/%d/approve", a), url.Values{})
	if code != http.StatusSeeOther || !strings.Contains(h.Get("Location"), "msg=approved") {
		t.Fatalf("одобрение: %d %s", code, h.Get("Location"))
	}
	if n := e.tg.last(t); n.ChatID != chatA || !strings.Contains(n.Text, "«Маникюр Анны» опубликовано") {
		t.Fatalf("уведомление: %+v", n)
	}
	l, _ := e.st.GetListing(e.ctx, a, 0)
	if l.Status != store.ListingPublished {
		t.Fatalf("после одобрения: %q", l.Status)
	}
	// Повторное одобрение — конфликт, а не 500.
	if code, _, _ := e.do("POST", fmt.Sprintf("/listings/%d/approve", a), url.Values{}); code != http.StatusConflict {
		t.Fatalf("повторное одобрение: %d", code)
	}

	// Отказ без причины не проходит, с причиной — уходит исполнителю.
	if code, body, _ := e.do("POST", fmt.Sprintf("/listings/%d/reject", b), url.Values{"reason": {""}}); code != 422 || !strings.Contains(body, "Укажи причину") {
		t.Fatalf("отказ без причины: %d", code)
	}
	code, _, _ = e.do("POST", fmt.Sprintf("/listings/%d/reject", b), url.Values{
		"reason": {"Мало информации: опишите услуги подробнее"}, "comment": {"Добавьте цены на педикюр"},
	})
	if code != http.StatusSeeOther {
		t.Fatalf("отказ: %d", code)
	}
	if n := e.tg.last(t); n.ChatID != chatB || !strings.Contains(n.Text, "опишите услуги подробнее. Добавьте цены на педикюр") {
		t.Fatalf("уведомление об отказе: %+v", n)
	}

	// Жалоба на опубликованное объявление видна модератору и закрывается.
	var reporter int64
	e.pool.QueryRow(e.ctx, `SELECT u.id FROM users u JOIN provider p ON p.user_id = u.id JOIN listing l ON l.provider_id = p.id WHERE l.id = $1`, b).Scan(&reporter)
	if err := e.st.CreateComplaint(e.ctx, reporter, a, "wrong_contacts", "Телефон не отвечает"); err != nil {
		t.Fatal(err)
	}
	e.t.Cleanup(func() { e.pool.Exec(context.Background(), `DELETE FROM complaint WHERE listing_id = $1`, a) })
	code, body, _ = e.do("GET", "/complaints", nil)
	if code != 200 || !strings.Contains(body, "Телефон не отвечает") {
		t.Fatalf("жалобы: %d", code)
	}
	code, body, _ = e.do("GET", fmt.Sprintf("/listings/%d", a), nil)
	if !strings.Contains(body, "Неверные контакты") || !strings.Contains(body, "Меры приняты") {
		t.Fatalf("жалоба в карточке: %d", code)
	}
	var cid int64
	e.pool.QueryRow(e.ctx, `SELECT id FROM complaint WHERE listing_id = $1`, a).Scan(&cid)
	code, _, h = e.do("POST", fmt.Sprintf("/complaints/%d/resolve", cid), url.Values{
		"status": {"dismissed"}, "resolution": {"Дозвонились"}, "back": {fmt.Sprintf("/listings/%d", a)},
	})
	if code != http.StatusSeeOther || !strings.HasPrefix(h.Get("Location"), fmt.Sprintf("/listings/%d", a)) {
		t.Fatalf("закрыть жалобу: %d %s", code, h.Get("Location"))
	}
	var st string
	e.pool.QueryRow(e.ctx, `SELECT status FROM complaint WHERE id = $1`, cid).Scan(&st)
	if st != "dismissed" {
		t.Fatalf("статус жалобы: %q", st)
	}

	// Фото отдаётся через прокси.
	code, body, h = e.do("GET", "/media/tg/AgACAgIAAxkBAAIBc2VfakeFileId", nil)
	if code != 200 || h.Get("Content-Type") != "image/jpeg" || !strings.Contains(body, "fake jpeg") {
		t.Fatalf("фото: %d %s", code, h.Get("Content-Type"))
	}
	if code, _, _ := e.do("GET", "/media/tg/..%2Fetc", nil); code != 404 {
		t.Fatalf("кривой file_id: %d", code)
	}
}
