package web

import (
	"net/http"

	"github.com/morphis/gummi/internal/webapi"
)

// A session's spec's writes (DESIGN §25.3): started from the template,
// and edited whole. The model hands over what each needs (ui.WebDocs) and
// the writes run here, off the board's loop. Every write tells the pages
// the card changed.

func (s *Server) specRoutes() {
	s.api("PUT /api/cards/{id}/spec", s.handleSpecWrite)
	s.api("POST /api/cards/{id}/spec/start", s.handleSpecStart)
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
