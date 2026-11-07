package store

import (
	"math"
	"strconv"
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
