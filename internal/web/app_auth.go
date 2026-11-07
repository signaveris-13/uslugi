package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/signaveris-13/uslugi/internal/store"
)

// Вход в Mini App. Telegram передаёт приложению initData, подписанные токеном бота;
// сервер проверяет подпись и выдаёт свой короткий токен. Токен ходит в заголовке
// X-App-Token: cookie внутри Telegram Web живут не везде.

const (
	appTokenTTL  = 24 * time.Hour
	initDataTTL  = 24 * time.Hour
	photoLinkTTL = 6 * time.Hour
)

var errBadInitData = errors.New("подпись Telegram не сошлась")

// ValidateInitData проверяет initData по алгоритму Telegram и возвращает пользователя.
// https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app
func ValidateInitData(raw, botToken string, now time.Time) (store.TelegramUser, error) {
	vals, err := url.ParseQuery(raw)
	if err != nil {
		return store.TelegramUser{}, errBadInitData
	}
	hash := vals.Get("hash")
	if hash == "" {
		return store.TelegramUser{}, errBadInitData
	}
	var pairs []string
	for k, v := range vals {
		if k == "hash" {
			continue
		}
		pairs = append(pairs, k+"="+v[0])
	}
	sort.Strings(pairs)
	check := strings.Join(pairs, "\n")

	secret := hmacSum([]byte("WebAppData"), []byte(botToken))
	want := hex.EncodeToString(hmacSum(secret, []byte(check)))
	if !hmac.Equal([]byte(want), []byte(hash)) {
		return store.TelegramUser{}, errBadInitData
	}

	authDate, _ := strconv.ParseInt(vals.Get("auth_date"), 10, 64)
	if authDate == 0 || now.Sub(time.Unix(authDate, 0)) > initDataTTL {
		return store.TelegramUser{}, errors.New("данные входа устарели — открой приложение заново")
	}
	var u struct {
		ID           int64  `json:"id"`
		FirstName    string `json:"first_name"`
		LastName     string `json:"last_name"`
		Username     string `json:"username"`
		LanguageCode string `json:"language_code"`
	}
	if err := json.Unmarshal([]byte(vals.Get("user")), &u); err != nil || u.ID == 0 {
		return store.TelegramUser{}, errBadInitData
	}
	return store.TelegramUser{ID: u.ID, Username: u.Username, FirstName: u.FirstName, LastName: u.LastName, LanguageCode: u.LanguageCode}, nil
}

func hmacSum(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}

// appToken: "<user_id>.<unix_exp>.<подпись>".
func (s *Server) issueAppToken(userID int64, now time.Time) string {
	exp := now.Add(appTokenTTL).Unix()
	payload := fmt.Sprintf("%d.%d", userID, exp)
	return payload + "." + s.sign("app:"+payload)
}

func (s *Server) parseAppToken(tok string, now time.Time) (int64, bool) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return 0, false
	}
	payload := parts[0] + "." + parts[1]
	if !hmac.Equal([]byte(s.sign("app:"+payload)), []byte(parts[2])) {
		return 0, false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || now.Unix() > exp {
		return 0, false
	}
	uid, err := strconv.ParseInt(parts[0], 10, 64)
	return uid, err == nil && uid > 0
}

func (s *Server) sign(msg string) string {
	return base64.RawURLEncoding.EncodeToString(hmacSum(s.appSecret, []byte(msg))[:18])
}

// photoLink — подписанная ссылка на фото для <img>: картинки не умеют слать заголовки.
func (s *Server) photoLink(p store.SnapshotPhoto, now time.Time) string {
	exp := strconv.FormatInt(now.Add(photoLinkTTL).Unix(), 10)
	if p.StorageKey != "" {
		k := base64.RawURLEncoding.EncodeToString([]byte(p.StorageKey))
		return "/app/photo/" + k + "?e=" + exp + "&s=" + s.sign("photo:"+p.StorageKey+":"+exp)
	}
	return "/app/tgphoto/" + url.PathEscape(p.TelegramFileID) + "?e=" + exp + "&s=" + s.sign("tg:"+p.TelegramFileID+":"+exp)
}

func (s *Server) checkPhotoLink(kind, id, exp, sig string, now time.Time) bool {
	e, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || now.Unix() > e {
		return false
	}
	return hmac.Equal([]byte(s.sign(kind+":"+id+":"+exp)), []byte(sig))
}
