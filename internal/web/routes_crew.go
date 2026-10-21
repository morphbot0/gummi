package web

import (
	"net/http"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/ui"
	"github.com/morphis/gummi/internal/webapi"
)

// A session's crew (DESIGN §24) and its spec's writes. The model hands
// over what each needs (ui.WebCrew, ui.WebDocs) and the reads and writes
// run here, off the board's loop: a crew's landing and re-check run git
// and its checks. Every write tells the pages the card changed; the
// engine's crew event moves the board itself.

func (s *Server) crewRoutes() {
	s.api("GET /api/cards/{id}/crew", s.handleCrew)
	s.api("POST /api/cards/{id}/crew/{action}", s.handleCrewAction)
	s.api("PUT /api/cards/{id}/spec", s.handleSpecWrite)
	s.api("POST /api/cards/{id}/spec/start", s.handleSpecStart)
}

// crew captures the session's crew from the model, answering the request
// itself when it cannot.
func (s *Server) crew(w http.ResponseWriter, r *http.Request) (*ui.WebCrew, bool) {
	id := r.PathValue("id")
	var (
		c   *ui.WebCrew
		err error
	)
	if !s.do(w, r, func(m *ui.Shell) tea.Cmd { c, err = m.WebCrew(id); return nil }) {
		return nil, false
	}
	if err != nil {
		writeDocsError(w, err, id)
		return nil, false
	}
	return c, true
}

// handleCrew is GET /api/cards/{id}/crew.
func (s *Server) handleCrew(w http.ResponseWriter, r *http.Request) {
	c, ok := s.crew(w, r)
	if !ok {
		return
	}
	out, err := c.Read(r.Context())
	if err != nil {
		writeDocsError(w, err, r.PathValue("id"))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCrewAction is POST /api/cards/{id}/crew/{action}: start, pause,
// resume, stop, steer, land, recheck or preauth. Answers CrewResult.
func (s *Server) handleCrewAction(w http.ResponseWriter, r *http.Request) {
	var req webapi.CrewRequest
	if !readBody(w, r, &req) {
		return
	}
	c, ok := s.crew(w, r)
	if !ok {
		return
	}
	out, err := c.Act(r.Context(), r.PathValue("action"), req, person(r))
	if err != nil {
		writeDocsError(w, err, r.PathValue("id"))
		return
	}
	s.changed(r.PathValue("id"))
	writeJSON(w, http.StatusOK, out)
}

// handleSpecWrite is PUT /api/cards/{id}/spec: a session spec's edit.
func (s *Server) handleSpecWrite(w http.ResponseWriter, r *http.Request) {
	var req webapi.SpecWriteRequest
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, bodyError(err, "bad request body: "+err.Error()))
		return
	}
	d, ok := s.docs(w, r)
	if !ok {
		return
	}
	sp, err := d.WriteSessionSpec(r.Context(), req, person(r))
	if err != nil {
		writeDocsError(w, err, r.PathValue("id"))
		return
	}
	s.changed(r.PathValue("id"))
	writeJSON(w, http.StatusOK, sp)
}

// handleSpecStart is POST /api/cards/{id}/spec/start.
func (s *Server) handleSpecStart(w http.ResponseWriter, r *http.Request) {
	var req webapi.SpecStartRequest
	if !readBody(w, r, &req) {
		return
	}
	d, ok := s.docs(w, r)
	if !ok {
		return
	}
	sp, err := d.StartSessionSpec(r.Context(), req.Title, person(r))
	if err != nil {
		writeDocsError(w, err, r.PathValue("id"))
		return
	}
	s.changed(r.PathValue("id"))
	writeJSON(w, http.StatusOK, sp)
}
