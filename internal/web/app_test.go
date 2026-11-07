package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/rand/v2"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/signaveris-13/uslugi/internal/store"
)

// appClient — исполнитель в Mini App.
type appClient struct {
	e     *env
	token string
}

func (a *appClient) do(method, path string, body io.Reader, ctype string) (int, string, http.Header) {
	a.e.t.Helper()
	req, _ := http.NewRequest(method, a.e.srv.URL+path, body)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if a.token != "" {
		req.Header.Set("X-App-Token", a.token)
	}
	resp, err := a.e.c.Do(req)
	if err != nil {
		a.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func (a *appClient) form(path string, f url.Values) (int, string) {
	code, body, _ := a.do(http.MethodPost, path, strings.NewReader(f.Encode()), "application/x-www-form-urlencoded")
	return code, body
}

// multipartForm — как htmx отправляет форму с hx-encoding="multipart/form-data".
func (a *appClient) multipartForm(path string, f url.Values, photo []byte) (int, string) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, vs := range f {
		for _, v := range vs {
			mw.WriteField(k, v)
		}
	}
	if photo != nil {
		fw, _ := mw.CreateFormFile("photos", "work.png")
		fw.Write(photo)
	}
	mw.Close()
	code, body, _ := a.do(http.MethodPost, path, &buf, mw.FormDataContentType())
	return code, body
}

func testPNG(t *testing.T) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for x := 0; x < 40; x++ {
		for y := 0; y < 30; y++ {
			img.Set(x, y, color.RGBA{218, 37, 29, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func mustContain(t *testing.T, body string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(body, p) {
			t.Fatalf("нет %q в ответе:\n%s", p, body)
		}
	}
}

var photoSrcRe = regexp.MustCompile(`src="(/app/photo/[^"]+)"`)

func TestMiniApp(t *testing.T) {
	e := newEnv(t)
	ctx := e.ctx

	tgID := 5_000_000_000 + rand.Int64N(1_000_000_000)
	username := fmt.Sprintf("app_test_%d", rand.IntN(1e6))
	t.Cleanup(func() {
		ctx := context.Background()
		e.pool.Exec(ctx, `DELETE FROM event WHERE user_id = (SELECT id FROM users WHERE telegram_id = $1)`, tgID)
		e.pool.Exec(ctx, `DELETE FROM listing WHERE provider_id IN (SELECT p.id FROM provider p JOIN users u ON u.id = p.user_id WHERE u.telegram_id = $1)`, tgID)
		e.pool.Exec(ctx, `DELETE FROM provider WHERE user_id = (SELECT id FROM users WHERE telegram_id = $1)`, tgID)
		e.pool.Exec(ctx, `DELETE FROM users WHERE telegram_id = $1`, tgID)
	})

	// Страница приложения открывается во фрейме Telegram Web.
	_, shell, h := (&appClient{e: e}).do(http.MethodGet, "/app", nil, "")
	mustContain(t, shell, "telegram-web-app.js", "/static/miniapp.js")
	if h.Get("X-Frame-Options") != "" || !strings.Contains(h.Get("Content-Security-Policy"), "web.telegram.org") {
		t.Fatalf("заголовки фрейма: %v", h)
	}

	// Вход: подделанные initData не проходят, настоящие дают токен.
	user, _ := json.Marshal(map[string]any{"id": tgID, "username": username, "first_name": "Ира"})
	initData := signInitData(testBotToken, map[string]string{
		"auth_date": strconv.FormatInt(time.Now().Unix(), 10), "query_id": "AAHtest", "user": string(user),
	})
	a := &appClient{e: e}
	if code, _ := a.form("/app/auth", url.Values{"init_data": {strings.Replace(initData, "query_id=AAHtest", "query_id=AAHfake", 1)}}); code != http.StatusUnauthorized {
		t.Fatalf("подделка прошла: %d", code)
	}
	code, body := a.form("/app/auth", url.Values{"init_data": {initData}})
	var res struct{ Token string }
	if code != http.StatusOK || json.Unmarshal([]byte(body), &res) != nil || res.Token == "" {
		t.Fatalf("вход: %d %s", code, body)
	}
	if code, _, h := a.do(http.MethodGet, "/app/my", nil, ""); code != http.StatusUnauthorized || h.Get("HX-Trigger") != "app-reauth" {
		t.Fatalf("без токена: %d", code)
	}
	a.token = res.Token

	// Город, затем пустой список и новое объявление.
	_, body, _ = a.do(http.MethodGet, "/app/my", nil, "")
	mustContain(t, body, "В каком ты городе?")
	var cityID, catID int64
	e.pool.QueryRow(ctx, `SELECT id FROM city WHERE slug = 'nha-trang'`).Scan(&cityID)
	e.pool.QueryRow(ctx, `SELECT id FROM category WHERE slug = 'manicure'`).Scan(&catID)
	_, body = a.form("/app/city", url.Values{"city_id": {strconv.FormatInt(cityID, 10)}})
	mustContain(t, body, "Мои объявления", "Пока нет объявлений", "Нячанг")

	_, body = a.form("/app/listings", nil)
	mustContain(t, body, "Новое объявление", "Отправить на модерацию", "@"+username)
	var id int64
	e.pool.QueryRow(ctx, `SELECT l.id FROM listing l JOIN provider p ON p.id = l.provider_id JOIN users u ON u.id = p.user_id
		WHERE u.telegram_id = $1`, tgID).Scan(&id)
	path := "/app/listings/" + strconv.FormatInt(id, 10)

	// Повторное «Новое» открывает тот же черновик.
	_, body = a.form("/app/listings", nil)
	mustContain(t, body, "Продолжаем незаконченный черновик")

	fields := url.Values{
		"primary_category_id": {strconv.FormatInt(catID, 10)},
		"title":               {"Маникюр с покрытием"},
		"description":         {"Аппаратный маникюр, гель-лак, выезд на дом"},
		"city_id":             {strconv.FormatInt(cityID, 10)},
		"travels":             {"1"},
		"price_type":          {"from"},
		"price_amount":        {"450к"},
		"currency":            {"VND"},
		"show_telegram":       {"1"},
		"whatsapp":            {"+84 905 123 456"},
		"language":            {"ru", "en"},
		"action":              {"save"},
	}
	bad := url.Values{}
	for k, v := range fields {
		bad[k] = v
	}
	bad.Set("price_amount", "дорого")
	_, body = a.multipartForm(path, bad, nil)
	mustContain(t, body, "Цена: напиши число", "Маникюр с покрытием") // ввод не теряется

	_, body = a.multipartForm(path, fields, nil)
	mustContain(t, body, "Сохранено.", `value="450 000"`, "Ещё в этой категории")

	// Фото загружается сразу после выбора (без action), ссылка на него подписана.
	upload := url.Values{}
	for k, v := range fields {
		if k != "action" {
			upload[k] = v
		}
	}
	_, body = a.multipartForm(path, upload, testPNG(t))
	mustContain(t, body, "Фото добавлено: 1.")
	m := photoSrcRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("нет ссылки на фото:\n%s", body)
	}
	src := strings.ReplaceAll(m[1], "&amp;", "&")
	if code, img, h := (&appClient{e: e}).do(http.MethodGet, src, nil, ""); code != http.StatusOK || h.Get("Content-Type") != "image/jpeg" || !strings.HasPrefix(img, "\xff\xd8") {
		t.Fatalf("фото: %d %s", code, h.Get("Content-Type"))
	}
	if code, _, _ := (&appClient{e: e}).do(http.MethodGet, src[:len(src)-2]+"xx", nil, ""); code != http.StatusNotFound {
		t.Fatalf("подпись фото не проверяется: %d", code)
	}

	// Отправка: сначала правила, потом очередь модерации.
	submit := url.Values{}
	for k, v := range fields {
		submit[k] = v
	}
	submit.Set("action", "submit")
	_, body = a.multipartForm(path, submit, nil)
	mustContain(t, body, "Отметь, что принимаешь правила")
	submit.Set("terms", "1")
	_, body = a.multipartForm(path, submit, nil)
	mustContain(t, body, "Отправили на модерацию", "На модерации")
	l, _ := e.st.GetListing(ctx, id, 0)
	if l.Status != store.ListingModeration || l.PriceAmount == nil || *l.PriceAmount != 450000 || l.ContactTelegram == nil || *l.ContactTelegram != username {
		t.Fatalf("объявление: %+v", l)
	}

	// Модератор видит загруженное фото и публикует.
	_, page, _ := e.do(http.MethodGet, fmt.Sprintf("/listings/%d", id), nil)
	fileRe := regexp.MustCompile(`src="(/media/file/[^"]+)"`)
	fm := fileRe.FindStringSubmatch(page)
	if fm == nil {
		t.Fatalf("нет фото в админке:\n%s", page)
	}
	if code, _, _ := e.do(http.MethodGet, fm[1], nil); code != http.StatusOK {
		t.Fatalf("фото в админке: %d", code)
	}
	if code, _, _ := e.do(http.MethodPost, fmt.Sprintf("/listings/%d/approve", id), url.Values{}); code != http.StatusSeeOther {
		t.Fatalf("одобрение: %d", code)
	}

	// Правка опубликованного: уходит на проверку, в каталоге — прежняя версия.
	_, body, _ = a.do(http.MethodGet, path, nil, "")
	mustContain(t, body, "Опубликовано", "Отправить правку на проверку")
	edit := url.Values{}
	for k, v := range fields {
		edit[k] = v
	}
	edit.Set("title", "Маникюр и педикюр с покрытием")
	edit.Set("action", "submit")
	_, body = a.multipartForm(path, edit, nil)
	mustContain(t, body, "Правка ушла на проверку", "Правка на проверке")
	pub, _ := e.st.PublishedListing(ctx, id)
	if pub.TitleOr() != "Маникюр с покрытием" {
		t.Fatalf("в каталоге уже новая версия: %q", pub.TitleOr())
	}

	// Админка: вкладка «Правки» и сравнение полей.
	_, list, _ := e.do(http.MethodGet, "/listings?status=revisions", nil)
	mustContain(t, list, fmt.Sprintf("/listings/%d", id), "Правки")
	_, page, _ = e.do(http.MethodGet, fmt.Sprintf("/listings/%d", id), nil)
	mustContain(t, page, "Правка исполнителя", "Маникюр и педикюр с покрытием", "Одобрить правку")

	if code, _, _ := e.do(http.MethodPost, fmt.Sprintf("/listings/%d/revision/reject", id), url.Values{}); code != http.StatusUnprocessableEntity {
		t.Fatalf("отказ без причины: %d", code)
	}
	code, _, h = e.do(http.MethodPost, fmt.Sprintf("/listings/%d/revision/reject", id), url.Values{"comment": {"Педикюр не делаешь — это из другой категории"}})
	if code != http.StatusSeeOther || !strings.Contains(h.Get("Location"), "rev_no") {
		t.Fatalf("отказ: %d %s", code, h.Get("Location"))
	}
	if n := e.tg.last(t); n.ChatID != tgID || !strings.Contains(n.Text, "не прошли модерацию") {
		t.Fatalf("уведомление: %+v", n)
	}

	// Новая правка и одобрение — каталог обновился.
	edit.Set("title", "Маникюр с покрытием и дизайном")
	a.multipartForm(path, edit, nil)
	code, _, h = e.do(http.MethodPost, fmt.Sprintf("/listings/%d/revision/approve", id), url.Values{})
	if code != http.StatusSeeOther || !strings.Contains(h.Get("Location"), "rev_ok") {
		t.Fatalf("одобрение правки: %d %s", code, h.Get("Location"))
	}
	pub, _ = e.st.PublishedListing(ctx, id)
	if pub.TitleOr() != "Маникюр с покрытием и дизайном" || len(pub.Photos) != 1 {
		t.Fatalf("после одобрения: %q, фото %d", pub.TitleOr(), len(pub.Photos))
	}
	if n := e.tg.last(t); !strings.Contains(n.Text, "одобрены") {
		t.Fatalf("уведомление: %+v", n)
	}
	_, body, _ = a.do(http.MethodGet, "/app/my", nil, "")
	mustContain(t, body, "Маникюр с покрытием и дизайном", "Опубликовано", "/app/photo/")
}
