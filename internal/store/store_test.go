package store

import (
	"context"
	"errors"
	"math/rand/v2"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Тесты идут на настоящем Postgres с применёнными миграциями.
// Без DATABASE_URL пропускаются.
func testStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL не задан")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return New(pool), pool
}

// testMember заводит участника команды со случайным Telegram ID.
func testMember(t *testing.T, s *Store, pool *pgxpool.Pool) TeamMember {
	t.Helper()
	ctx := context.Background()
	tgID := 9_000_000_000 + rand.Int64N(1_000_000_000)
	if err := s.BootstrapTeam(ctx, []int64{tgID}); err != nil {
		t.Fatal(err)
	}
	m, err := s.ActiveTeamMemberByTelegramID(ctx, tgID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM audit_log WHERE actor_id = $1`, m.ID)
		pool.Exec(ctx, `DELETE FROM lead_message WHERE author_id = $1`, m.ID)
		pool.Exec(ctx, `DELETE FROM lead WHERE assignee_id = $1`, m.ID)
		pool.Exec(ctx, `DELETE FROM admin_session WHERE team_member_id = $1`, m.ID)
		pool.Exec(ctx, `DELETE FROM admin_login WHERE team_member_id = $1`, m.ID)
		pool.Exec(ctx, `DELETE FROM team_member WHERE id = $1`, m.ID)
	})
	return m
}

func TestLeadLifecycle(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	m := testMember(t, s, pool)

	if _, err := s.CreateLead(ctx, m.ID, LeadInput{}); !errors.Is(err, ErrInvalidLead) {
		t.Fatalf("пустой лид: ждали ErrInvalidLead, получили %v", err)
	}
	var parentID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM category WHERE parent_id IS NULL LIMIT 1`).Scan(&parentID); err != nil {
		t.Fatal(err)
	}
	var ve ValidationError
	if _, err := s.CreateLead(ctx, m.ID, LeadInput{Name: "x", CategoryID: parentID}); !errors.As(err, &ve) {
		t.Fatalf("категория верхнего уровня: ждали ValidationError, получили %v", err)
	}

	id, err := s.CreateLead(ctx, m.ID, LeadInput{
		Name:             "Анна, маникюр",
		TelegramUsername: "https://t.me/anna_nails_test",
		Tags:             []string{" нячанг ", ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.GetLead(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if l.TelegramUsername == nil || *l.TelegramUsername != "anna_nails_test" {
		t.Fatalf("username не нормализован: %v", l.TelegramUsername)
	}
	if l.Status != LeadQualified || l.Source != "manual" {
		t.Fatalf("ручной лид: статус %q, источник %q", l.Status, l.Source)
	}
	if len(l.Tags) != 1 || l.Tags[0] != "нячанг" {
		t.Fatalf("теги: %v", l.Tags)
	}

	// Дубль по username без учёта регистра.
	dupID, err := s.CreateLead(ctx, m.ID, LeadInput{TelegramUsername: "@Anna_Nails_Test"})
	if err != nil {
		t.Fatal(err)
	}
	dups, err := s.FindLeadDuplicates(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	if len(dups) != 1 || dups[0].ID != dupID {
		t.Fatalf("дубли: %+v", dups)
	}

	// Первое исходящее — «Приглашён», входящее после — «Ответил».
	if err := s.AddTouch(ctx, m.ID, id, "outgoing", "Привет! Приглашаем в Uslugi"); err != nil {
		t.Fatal(err)
	}
	l, _ = s.GetLead(ctx, id)
	if l.Status != LeadInvited || l.InvitedAt == nil {
		t.Fatalf("после исходящего: %q, invited_at %v", l.Status, l.InvitedAt)
	}
	if err := s.AddTouch(ctx, m.ID, id, "incoming", "Интересно"); err != nil {
		t.Fatal(err)
	}
	l, _ = s.GetLead(ctx, id)
	if l.Status != LeadReplied {
		t.Fatalf("после входящего: %q", l.Status)
	}
	touches, _ := s.ListTouches(ctx, id)
	if len(touches) != 2 {
		t.Fatalf("касаний: %d", len(touches))
	}

	// «Не беспокоить» блокирует исходящие, но не входящие.
	if err := s.SetLeadStatus(ctx, m.ID, id, LeadDoNotContact); err != nil {
		t.Fatal(err)
	}
	if err := s.AddTouch(ctx, m.ID, id, "outgoing", "Ещё раз"); !errors.Is(err, ErrDoNotContact) {
		t.Fatalf("ждали ErrDoNotContact, получили %v", err)
	}
	if err := s.AddTouch(ctx, m.ID, id, "incoming", "Уберите меня"); err != nil {
		t.Fatal(err)
	}

	// Ссылка-приглашение выдаётся один раз и потом не меняется.
	t1, err := s.EnsureInviteToken(ctx, m.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	t2, _ := s.EnsureInviteToken(ctx, m.ID, id)
	if t1 == "" || t1 != t2 {
		t.Fatalf("токены приглашения: %q, %q", t1, t2)
	}

	// Фильтры.
	list, err := s.ListLeads(ctx, LeadFilter{Status: LeadDoNotContact, Query: "анна", AssigneeID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != id {
		t.Fatalf("фильтр: %d лидов", len(list))
	}
}

func TestLoginFlow(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	m := testMember(t, s, pool)

	token, err := s.CreateLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st, _, _ := s.ExchangeLogin(ctx, token); st != LoginPending {
		t.Fatalf("до подтверждения: %v", st)
	}
	if _, err := s.ConfirmLogin(ctx, token, 1); !errors.Is(err, ErrNotTeamMember) {
		t.Fatalf("чужой Telegram: %v", err)
	}
	if _, err := s.ConfirmLogin(ctx, token, m.TelegramID); err != nil {
		t.Fatal(err)
	}
	st, sid, err := s.ExchangeLogin(ctx, token)
	if err != nil || st != LoginConfirmed || sid == "" {
		t.Fatalf("обмен: %v %q %v", st, sid, err)
	}
	if st, _, _ := s.ExchangeLogin(ctx, token); st != LoginExpired {
		t.Fatalf("повторный обмен должен не работать: %v", st)
	}
	got, err := s.SessionMember(ctx, sid)
	if err != nil || got.ID != m.ID {
		t.Fatalf("сессия: %+v %v", got, err)
	}
	if err := s.DeleteSession(ctx, sid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionMember(ctx, sid); !errors.Is(err, ErrNotFound) {
		t.Fatalf("после выхода: %v", err)
	}
}
