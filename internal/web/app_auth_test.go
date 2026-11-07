package web

import (
	"encoding/hex"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"
)

// signInitData собирает initData так же, как Telegram.
func signInitData(botToken string, fields map[string]string) string {
	var pairs []string
	vals := url.Values{}
	for k, v := range fields {
		pairs = append(pairs, k+"="+v)
		vals.Set(k, v)
	}
	sort.Strings(pairs)
	secret := hmacSum([]byte("WebAppData"), []byte(botToken))
	vals.Set("hash", hex.EncodeToString(hmacSum(secret, []byte(strings.Join(pairs, "\n")))))
	return vals.Encode()
}

func TestValidateInitData(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	fields := map[string]string{
		"auth_date": "1799999000",
		"query_id":  "AAH",
		"user":      `{"id":150154056,"first_name":"Сергей","username":"Signaveris","language_code":"ru"}`,
	}
	raw := signInitData("123:secret", fields)

	u, err := ValidateInitData(raw, "123:secret", now)
	if err != nil || u.ID != 150154056 || u.Username != "Signaveris" {
		t.Fatalf("валидные данные: %+v %v", u, err)
	}
	if _, err := ValidateInitData(raw, "123:other", now); err == nil {
		t.Fatal("чужой токен бота должен не пройти")
	}
	tampered := strings.Replace(raw, "Signaveris", "Hacker", 1)
	if _, err := ValidateInitData(tampered, "123:secret", now); err == nil {
		t.Fatal("подменённые данные должны не пройти")
	}
	if _, err := ValidateInitData(raw, "123:secret", now.Add(48*time.Hour)); err == nil {
		t.Fatal("устаревшие данные должны не пройти")
	}
}

func TestAppTokenAndPhotoLinks(t *testing.T) {
	s := &Server{appSecret: []byte("test-secret")}
	now := time.Now()
	tok := s.issueAppToken(42, now)
	if uid, ok := s.parseAppToken(tok, now); !ok || uid != 42 {
		t.Fatalf("токен: %d %v", uid, ok)
	}
	if _, ok := s.parseAppToken(strings.Replace(tok, "42.", "43.", 1), now); ok {
		t.Fatal("подменённый токен должен не пройти")
	}
	if _, ok := s.parseAppToken(tok, now.Add(25*time.Hour)); ok {
		t.Fatal("просроченный токен должен не пройти")
	}
}
