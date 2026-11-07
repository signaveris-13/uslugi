package store

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// PriceText — цена для карточки: «от 450 000 ₫», «$25», «по договорённости».
func (l Listing) PriceText() string {
	if l.PriceType == nil {
		return ""
	}
	if *l.PriceType == "negotiable" {
		return "по договорённости"
	}
	if l.PriceAmount == nil {
		return ""
	}
	s := FormatAmount(*l.PriceAmount, deref(l.Currency))
	if *l.PriceType == "from" {
		return "от " + s
	}
	return s
}

// FormatAmount: 450000 VND → «450 000 ₫», 25 USD → «$25».
func FormatAmount(v float64, currency string) string {
	if currency == "USD" {
		if v == math.Trunc(v) {
			return "$" + groupThousands(int64(v))
		}
		return "$" + strconv.FormatFloat(v, 'f', 2, 64)
	}
	s := groupThousands(int64(math.Round(v)))
	if currency == "VND" || currency == "" {
		return s + " ₫"
	}
	return s + " " + currency
}

func groupThousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ' ')
		}
		out = append(out, c)
	}
	return string(out)
}

// TitleOr — название или «Без названия».
func (l Listing) TitleOr() string {
	if l.Title == nil || *l.Title == "" {
		return "Без названия"
	}
	return *l.Title
}

var thousandsRe = regexp.MustCompile(`^\d{1,3}([.,]\d{3})+$`)

// ParseAmount понимает «450000», «450 000», «450.000», «450к», «1,5 млн», «25.5».
func ParseAmount(s string) (float64, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer(" ", "", " ", "", " ", "", "₫", "", "$", "", "vnd", "", "usd", "", "донгов", "", "донга", "").Replace(s)
	mult := 1.0
	for _, suf := range []struct {
		s string
		m float64
	}{{"млн", 1e6}, {"kk", 1e6}, {"m", 1e6}, {"тыс", 1e3}, {"к", 1e3}, {"k", 1e3}} {
		if strings.HasSuffix(s, suf.s) {
			s = strings.TrimSuffix(s, suf.s)
			mult = suf.m
			break
		}
	}
	if thousandsRe.MatchString(s) {
		s = strings.NewReplacer(".", "", ",", "").Replace(s)
	} else {
		s = strings.Replace(s, ",", ".", 1)
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	v *= mult
	if v >= 1e12 {
		return 0, false
	}
	return v, true
}

// NormalizeContact приводит контакт к одному виду: телефоны без мусора, Instagram — ником.
func NormalizeContact(field, v string) string {
	v = strings.TrimSpace(v)
	switch field {
	case "phone", "whatsapp":
		var b strings.Builder
		for i, r := range v {
			if (r >= '0' && r <= '9') || (r == '+' && i == 0) || r == ' ' {
				b.WriteRune(r)
			}
		}
		return strings.Join(strings.Fields(b.String()), " ")
	case "instagram":
		for _, p := range []string{"https://", "http://", "www.", "instagram.com/", "@"} {
			v = strings.TrimPrefix(v, p)
		}
		v = strings.TrimSuffix(v, "/")
		if i := strings.IndexAny(v, "/?"); i >= 0 {
			v = v[:i]
		}
		return v
	}
	return v
}

// AmountInput — сумма для поля ввода: «450 000» или «25.50», без валюты.
func AmountInput(v float64) string {
	if v != math.Trunc(v) {
		return strconv.FormatFloat(v, 'f', 2, 64)
	}
	return groupThousands(int64(v))
}
