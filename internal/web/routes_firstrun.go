package web

import (
	"net/http"

	"github.com/morphis/gummi/internal/webapi"
)

// firstRunRoutes are the first-run setup: one read of what is pending and
// the person's answer, which is a profile or a skip.
func (s *Server) firstRunRoutes() {
	s.api("GET /api/firstrun", s.handleFirstRun)
	s.api("POST /api/firstrun", s.handleFirstRunAnswer)
}

func (s *Server) handleFirstRun(w http.ResponseWriter, r *http.Request) {
	out, err := s.opt.Board.FirstRun(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleFirstRunAnswer(w http.ResponseWriter, r *http.Request) {
	var req webapi.FirstRunRequest
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request body: "+err.Error())
		return
	}
	out, err := s.opt.Board.AnswerFirstRun(r.Context(), req)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
