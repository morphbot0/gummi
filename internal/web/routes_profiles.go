package web

import (
	"net/http"

	"github.com/morphis/gummi/internal/webapi"
)

// profilesRoutes are the board's profiles (.gummi/profiles.yaml): one
// read, and a person's edits, each answered with the profiles as they
// then read.
func (s *Server) profilesRoutes() {
	s.api("GET /api/profiles", s.handleProfiles)
	s.api("PUT /api/profiles/{name}", s.handleSaveProfile)
	s.api("DELETE /api/profiles/{name}", s.handleDeleteProfile)
	s.api("POST /api/profiles/{name}/default", s.handleDefaultProfile)
}

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	s.profilesAnswer(w)(s.opt.Board.Profiles(r.Context()))
}

func (s *Server) handleSaveProfile(w http.ResponseWriter, r *http.Request) {
	var req webapi.ProfileRequest
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request body: "+err.Error())
		return
	}
	s.profilesAnswer(w)(s.opt.Board.SaveProfile(r.Context(), r.PathValue("name"), req))
}

func (s *Server) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	s.profilesAnswer(w)(s.opt.Board.DeleteProfile(r.Context(), r.PathValue("name")))
}

func (s *Server) handleDefaultProfile(w http.ResponseWriter, r *http.Request) {
	s.profilesAnswer(w)(s.opt.Board.SetDefaultProfile(r.Context(), r.PathValue("name")))
}

func (s *Server) profilesAnswer(w http.ResponseWriter) func(webapi.Profiles, error) {
	return func(out webapi.Profiles, err error) {
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}
