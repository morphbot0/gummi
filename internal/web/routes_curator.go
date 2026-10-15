package web

import (
	"net/http"

	"github.com/morphis/gummi/internal/webapi"
)

// curatorRoutes are the skill pack and its curator (DESIGN §23): one
// read, and the person's acts on the pack afterwards (§23.6).
func (s *Server) curatorRoutes() {
	s.api("GET /api/curator", s.handleCurator)
	s.api("PUT /api/curator/config", s.handleSetCurator)
	s.api("DELETE /api/curator/config", s.handleClearCurator)
	s.api("POST /api/curator/run", s.handleRunCurator)
	s.api("POST /api/curator/runs/{id}/revert", s.handleRevertCuratorRun)
	s.api("POST /api/curator/skills/{name}/pin", s.handlePinSkill(true))
	s.api("POST /api/curator/skills/{name}/unpin", s.handlePinSkill(false))
}

// handleCurator is GET /api/curator: the curator's configuration, the
// pack's skills and the curator's runs, newest first.
func (s *Server) handleCurator(w http.ResponseWriter, r *http.Request) {
	out, err := s.opt.Board.Curator(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSetCurator is PUT /api/curator/config: the curator block written
// to the workspace config and live at the board's next tick. A block the
// curator could not run on is a bad request.
func (s *Server) handleSetCurator(w http.ResponseWriter, r *http.Request) {
	var req webapi.CuratorConfig
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request body: "+err.Error())
		return
	}
	s.curatorConfigAnswer(w, r, &req)
}

// handleClearCurator is DELETE /api/curator/config: the curator block
// removed, so nothing curates and nothing spends. The pack stays.
func (s *Server) handleClearCurator(w http.ResponseWriter, r *http.Request) {
	s.curatorConfigAnswer(w, r, nil)
}

func (s *Server) curatorConfigAnswer(w http.ResponseWriter, r *http.Request, req *webapi.CuratorConfig) {
	out, err := s.opt.Board.SetCurator(r.Context(), req)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleRunCurator is POST /api/curator/run: the board runs the curator
// at its next tick. Refused when no curator is configured or one is
// already running.
func (s *Server) handleRunCurator(w http.ResponseWriter, r *http.Request) {
	out, err := s.opt.Board.RunCuratorNow(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	s.answer(w, out)
}

// handleRevertCuratorRun is POST /api/curator/runs/{id}/revert: the run
// undone in one commit and published.
func (s *Server) handleRevertCuratorRun(w http.ResponseWriter, r *http.Request) {
	out, err := s.opt.Board.RevertCuratorRun(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.answer(w, out)
}

// handlePinSkill is POST /api/curator/skills/{name}/pin and …/unpin.
func (s *Server) handlePinSkill(pinned bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out, err := s.opt.Board.PinSkill(r.Context(), r.PathValue("name"), pinned)
		if err != nil {
			s.fail(w, err)
			return
		}
		s.answer(w, out)
	}
}
