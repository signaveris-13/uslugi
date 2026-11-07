package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/signaveris-13/uslugi/internal/store"
	"github.com/signaveris-13/uslugi/internal/web/views"
)

// Сообщения после действий передаются через ?msg=, чтобы пережить редирект.
var notices = map[string]string{
	"created": "Лид создан.",
	"saved":   "Сохранено.",
	"invite":  "Ссылка-приглашение готова.",
	"touch":   "Касание отмечено.",
}

var errorNotices = map[string]string{
	"dnc": store.ErrDoNotContact.Error() + ".",
}

func (s *Server) refs(r *http.Request) (views.Refs, error) {
	ctx := r.Context()
	cities, err := s.store.ListCities(ctx)
	if err != nil {
		return views.Refs{}, err
	}
	cats, err := s.store.ListSubcategories(ctx)
	if err != nil {
		return views.Refs{}, err
	}
	team, err := s.store.ListTeam(ctx)
	if err != nil {
		return views.Refs{}, err
	}
	return views.Refs{Cities: cities, Groups: views.GroupCategories(cats), Team: team}, nil
}

func parseFilter(q url.Values) store.LeadFilter {
	return store.LeadFilter{
		Status:     q.Get("status"),
		CityID:     parseID(q.Get("city")),
		CategoryID: parseID(q.Get("category")),
		Source:     q.Get("source"),
		AssigneeID: parseID(q.Get("assignee")),
		Query:      q.Get("q"),
	}
}

func (s *Server) leadsList(w http.ResponseWriter, r *http.Request, me store.TeamMember) {
	f := parseFilter(r.URL.Query())
	leads, err := s.store.ListLeads(r.Context(), f)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	refs, err := s.refs(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, views.LeadsPage(views.LeadsData{
		Page: s.page(r, me, "Лиды", "list"), Refs: refs, Leads: leads, Filter: f,
	}))
}

func (s *Server) leadsBoard(w http.ResponseWriter, r *http.Request, me store.TeamMember) {
	f := parseFilter(r.URL.Query())
	f.Status = ""
	f.Limit = 500
	leads, err := s.store.ListLeads(r.Context(), f)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	refs, err := s.refs(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	byStatus := map[string][]store.Lead{}
	for _, l := range leads {
		byStatus[l.Status] = append(byStatus[l.Status], l)
	}
	cols := make([]views.BoardColumn, 0, len(store.LeadStatuses))
	for _, st := range store.LeadStatuses {
		cols = append(cols, views.BoardColumn{Status: st, Leads: byStatus[st]})
	}
	s.render(w, r, http.StatusOK, views.BoardPage(views.BoardData{
		Page: s.page(r, me, "Канбан", "board"), Refs: refs, Columns: cols, Filter: f, Back: r.URL.RequestURI(),
	}))
}

func (s *Server) leadNew(w http.ResponseWriter, r *http.Request, me store.TeamMember) {
	s.renderLeadForm(w, r, me, store.LeadInput{Source: "manual", Status: store.LeadQualified}, "", http.StatusOK)
}

func (s *Server) renderLeadForm(w http.ResponseWriter, r *http.Request, me store.TeamMember, in store.LeadInput, errText string, status int) {
	refs, err := s.refs(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, status, views.LeadPage(views.LeadData{
		Page: s.page(r, me, "Новый лид", "new"), Refs: refs, Form: in, IsNew: true, Error: errText,
	}))
}

func (s *Server) leadCreate(w http.ResponseWriter, r *http.Request, me store.TeamMember) {
	in := leadInputFromForm(r)
	id, err := s.store.CreateLead(r.Context(), me.ID, in)
	if err != nil {
		if msg, ok := validationMessage(err); ok {
			s.renderLeadForm(w, r, me, in, msg, http.StatusUnprocessableEntity)
			return
		}
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/leads/"+strconv.FormatInt(id, 10)+"?msg=created", http.StatusSeeOther)
}

func (s *Server) leadShow(w http.ResponseWriter, r *http.Request, me store.TeamMember) {
	id := parseID(r.PathValue("id"))
	l, err := s.store.GetLead(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.renderLead(w, r, me, l, views.FormFromLead(l), "", http.StatusOK)
}

func (s *Server) renderLead(w http.ResponseWriter, r *http.Request, me store.TeamMember, l store.Lead, form store.LeadInput, errText string, status int) {
	ctx := r.Context()
	refs, err := s.refs(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	touches, err := s.store.ListTouches(ctx, l.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	dups, err := s.store.FindLeadDuplicates(ctx, l)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	msg := r.URL.Query().Get("msg")
	if errText == "" {
		errText = errorNotices[msg]
	}
	inviteURL := ""
	if l.InviteToken != nil && s.botUsername != "" {
		inviteURL = "https://t.me/" + url.PathEscape(s.botUsername) + "?start=inv_" + *l.InviteToken
	}
	s.render(w, r, status, views.LeadPage(views.LeadData{
		Page: s.page(r, me, l.DisplayName(), ""), Refs: refs, Lead: l, Form: form,
		Error: errText, Notice: notices[msg], Touches: touches, Duplicates: dups, InviteURL: inviteURL,
	}))
}

func (s *Server) leadUpdate(w http.ResponseWriter, r *http.Request, me store.TeamMember) {
	id := parseID(r.PathValue("id"))
	in := leadInputFromForm(r)
	err := s.store.UpdateLead(r.Context(), me.ID, id, in)
	if err != nil {
		if msg, ok := validationMessage(err); ok {
			l, gerr := s.store.GetLead(r.Context(), id)
			if gerr != nil {
				s.fail(w, r, gerr)
				return
			}
			s.renderLead(w, r, me, l, in, msg, http.StatusUnprocessableEntity)
			return
		}
		s.fail(w, r, err)
		return
	}
	redirectLead(w, r, id, "saved")
}

func (s *Server) leadStatus(w http.ResponseWriter, r *http.Request, me store.TeamMember) {
	id := parseID(r.PathValue("id"))
	if err := s.store.SetLeadStatus(r.Context(), me.ID, id, r.FormValue("status")); err != nil {
		s.fail(w, r, err)
		return
	}
	// С доски возвращаемся на доску с теми же фильтрами.
	if back := r.FormValue("back"); strings.HasPrefix(back, "/leads/board") {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	redirectLead(w, r, id, "")
}

func (s *Server) leadAssignee(w http.ResponseWriter, r *http.Request, me store.TeamMember) {
	id := parseID(r.PathValue("id"))
	if err := s.store.SetLeadAssignee(r.Context(), me.ID, id, parseID(r.FormValue("assignee_id"))); err != nil {
		s.fail(w, r, err)
		return
	}
	redirectLead(w, r, id, "")
}

func (s *Server) leadInvite(w http.ResponseWriter, r *http.Request, me store.TeamMember) {
	id := parseID(r.PathValue("id"))
	if _, err := s.store.EnsureInviteToken(r.Context(), me.ID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	redirectLead(w, r, id, "invite")
}

func (s *Server) leadTouch(w http.ResponseWriter, r *http.Request, me store.TeamMember) {
	id := parseID(r.PathValue("id"))
	err := s.store.AddTouch(r.Context(), me.ID, id, r.FormValue("direction"), r.FormValue("text"))
	switch {
	case errors.Is(err, store.ErrDoNotContact):
		redirectLead(w, r, id, "dnc")
	case err != nil:
		s.fail(w, r, err)
	default:
		redirectLead(w, r, id, "touch")
	}
}

func redirectLead(w http.ResponseWriter, r *http.Request, id int64, msg string) {
	target := "/leads/" + strconv.FormatInt(id, 10)
	if msg != "" {
		target += "?msg=" + msg
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func leadInputFromForm(r *http.Request) store.LeadInput {
	return store.LeadInput{
		Source:           r.FormValue("source"),
		SourceURL:        r.FormValue("source_url"),
		SourceText:       r.FormValue("source_text"),
		Name:             r.FormValue("name"),
		CategoryID:       parseID(r.FormValue("category_id")),
		CityID:           parseID(r.FormValue("city_id")),
		TelegramUsername: r.FormValue("telegram_username"),
		Phone:            r.FormValue("phone"),
		WhatsApp:         r.FormValue("whatsapp"),
		Instagram:        r.FormValue("instagram"),
		Summary:          r.FormValue("summary"),
		Notes:            r.FormValue("notes"),
		Tags:             strings.Split(r.FormValue("tags"), ","),
		Status:           r.FormValue("status"),
	}
}

// validationMessage отличает ошибки ввода от поломок: первые показываем в форме.
func validationMessage(err error) (string, bool) {
	var ve store.ValidationError
	if errors.As(err, &ve) {
		return ve.Msg, true
	}
	return "", false
}

func parseID(s string) int64 {
	v, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return v
}
