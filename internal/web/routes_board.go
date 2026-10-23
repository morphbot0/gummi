package web

import (
	"net/http"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/ui"
	"github.com/morphis/gummi/internal/webapi"
)

func (s *Server) boardRoutes() {
	s.api("GET /api/board", s.handleBoard)
}

// handleBoard is GET /api/board: the model's rail, and who is looking at
// it.
func (s *Server) handleBoard(w http.ResponseWriter, r *http.Request) {
	// taken before the read, so whatever moves during it is still told
	since := s.hub.head()
	var b webapi.Board
	if !s.do(w, r, func(m *ui.Shell) tea.Cmd { b = m.WebBoard(); return nil }) {
		return
	}
	if b.Repo == "" || b.Repo == "." {
		b.Repo = s.opt.Repo
	}
	b.Viewers = s.hub.viewers()
	b.EventID = since
	writeJSON(w, http.StatusOK, b)
}
