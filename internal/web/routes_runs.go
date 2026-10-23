package web

import (
	"net/http"
	"strconv"

	"github.com/morphis/gummi/internal/webapi"
)

// A session's workflow runs (DESIGN §25): its specs, a step opened beside
// the thread — its head and brief, its own diff, a person's message to it
// and the answer to its question — and what a person does to a run:
// pause, resume, stop and revert, and saving a recipe the session wrote
// inline as a workspace recipe — a person's act no agent reaches. The reads and writes are the
// engine's, through ui.WebDocs, off the board's loop; every write tells
// the pages the session moved.

func (s *Server) runRoutes() {
	s.api("GET /api/cards/{id}/specs", s.handleSpecs)
	s.api("GET /api/cards/{id}/steps/{card}", s.handleStep)
	s.api("GET /api/cards/{id}/steps/{card}/diff", s.handleStepDiff)
	s.api("POST /api/cards/{id}/steps/{card}/send", s.handleStepSend)
	s.api("POST /api/cards/{id}/steps/{card}/answer", s.handleStepAnswer)
	s.api("POST /api/cards/{id}/runs/{n}/{act}", s.handleRunAct)
	s.api("GET /api/cards/{id}/runs/{n}/revert", s.handleRevertPreview)
	s.api("POST /api/cards/{id}/runs/{n}/revert", s.handleRevert)
	s.api("POST /api/cards/{id}/runs/{n}/save", s.handleSaveRecipe)
	s.api("GET /api/cards/{id}/runs/{n}/diff", s.handleRunDiff)
	s.api("GET /api/cards/{id}/runs/{n}/artifacts/{name}", s.handleRunArtifact)
}

// runN reads the {n} of a run route, answering the request itself when it
// is no run's number.
func runN(w http.ResponseWriter, r *http.Request) (int, bool) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 1 {
		writeError(w, http.StatusBadRequest, "a run is named by its number")
		return 0, false
	}
	return n, true
}

// handleSpecs is GET /api/cards/{id}/specs.
func (s *Server) handleSpecs(w http.ResponseWriter, r *http.Request) {
	d, ok := s.docs(w, r)
	if !ok {
		return
	}
	out, err := d.Specs(r.Context())
	if err != nil {
		writeDocsError(w, err, r.PathValue("id"))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleStep is GET /api/cards/{id}/steps/{card}.
func (s *Server) handleStep(w http.ResponseWriter, r *http.Request) {
	d, ok := s.docs(w, r)
	if !ok {
		return
	}
	out, err := d.Step(r.Context(), r.PathValue("card"))
	if err != nil {
		writeDocsError(w, err, r.PathValue("id"))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleStepDiff is GET /api/cards/{id}/steps/{card}/diff.
func (s *Server) handleStepDiff(w http.ResponseWriter, r *http.Request) {
	d, ok := s.docs(w, r)
	if !ok {
		return
	}
	out, err := d.StepDiff(r.Context(), r.PathValue("card"))
	if err != nil {
		writeDocsError(w, err, r.PathValue("id"))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleStepSend is POST /api/cards/{id}/steps/{card}/send.
func (s *Server) handleStepSend(w http.ResponseWriter, r *http.Request) {
	var req webapi.StepSendRequest
	if !readBody(w, r, &req) {
		return
	}
	d, ok := s.docs(w, r)
	if !ok {
		return
	}
	out, err := d.StepSend(r.Context(), r.PathValue("card"), req.Text, person(r))
	if err != nil {
		writeDocsError(w, err, r.PathValue("id"))
		return
	}
	s.stepMoved(r)
	writeJSON(w, http.StatusOK, out)
}

// handleStepAnswer is POST /api/cards/{id}/steps/{card}/answer.
func (s *Server) handleStepAnswer(w http.ResponseWriter, r *http.Request) {
	var req webapi.StepAnswerRequest
	if !readBody(w, r, &req) {
		return
	}
	d, ok := s.docs(w, r)
	if !ok {
		return
	}
	if err := d.StepAnswer(r.Context(), r.PathValue("card"), req.Call, req.Answer, person(r)); err != nil {
		writeDocsError(w, err, r.PathValue("id"))
		return
	}
	s.stepMoved(r)
	w.WriteHeader(http.StatusNoContent)
}

// stepMoved tells the pages a step and its session moved.
func (s *Server) stepMoved(r *http.Request) {
	s.Publish(webapi.Change{Kind: webapi.ChangeCard, ID: r.PathValue("card")})
	s.Publish(webapi.Change{Kind: webapi.ChangeLive, ID: r.PathValue("card")})
	s.changed(r.PathValue("id"))
}

// handleRunAct is POST /api/cards/{id}/runs/{n}/{pause,resume,stop}.
func (s *Server) handleRunAct(w http.ResponseWriter, r *http.Request) {
	n, ok := runN(w, r)
	if !ok {
		return
	}
	d, ok := s.docs(w, r)
	if !ok {
		return
	}
	if err := d.RunAct(r.Context(), n, r.PathValue("act"), person(r)); err != nil {
		writeDocsError(w, err, r.PathValue("id"))
		return
	}
	s.changed(r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

// handleRevertPreview is GET /api/cards/{id}/runs/{n}/revert.
func (s *Server) handleRevertPreview(w http.ResponseWriter, r *http.Request) {
	n, ok := runN(w, r)
	if !ok {
		return
	}
	d, ok := s.docs(w, r)
	if !ok {
		return
	}
	out, err := d.RevertPreviewTo(r.Context(), n, r.URL.Query().Get("step"))
	if err != nil {
		writeDocsError(w, err, r.PathValue("id"))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleRevert is POST /api/cards/{id}/runs/{n}/revert: the confirmed
// revert, naming the tree its preview was read from.
func (s *Server) handleRevert(w http.ResponseWriter, r *http.Request) {
	n, ok := runN(w, r)
	if !ok {
		return
	}
	var body webapi.RevertRequest
	if err := readJSON(w, r, &body); err != nil || body.Tree == "" {
		writeError(w, http.StatusBadRequest, "expected {\"tree\": …}, the tree the revert's preview was read from")
		return
	}
	d, ok := s.docs(w, r)
	if !ok {
		return
	}
	out, err := d.RevertTo(r.Context(), n, body.Step, body.Tree)
	if err != nil {
		writeDocsError(w, err, r.PathValue("id"))
		return
	}
	s.changed(r.PathValue("id"))
	writeJSON(w, http.StatusOK, out)
}

// handleSaveRecipe is POST /api/cards/{id}/runs/{n}/save: the person
// saving the run's inline recipe as the workspace recipe the body names.
func (s *Server) handleSaveRecipe(w http.ResponseWriter, r *http.Request) {
	n, ok := runN(w, r)
	if !ok {
		return
	}
	var body webapi.SaveRecipeRequest
	if err := readJSON(w, r, &body); err != nil || body.Name == "" {
		writeError(w, http.StatusBadRequest, "expected {\"name\": …}, the name to save the recipe as")
		return
	}
	d, ok := s.docs(w, r)
	if !ok {
		return
	}
	out, err := d.SaveRecipe(r.Context(), n, body.Name)
	if err != nil {
		writeDocsError(w, err, r.PathValue("id"))
		return
	}
	s.changed(r.PathValue("id"))
	writeJSON(w, http.StatusOK, out)
}

// handleRunDiff is GET /api/cards/{id}/runs/{n}/diff: the run's whole
// diff, from where it started to the tree now.
func (s *Server) handleRunDiff(w http.ResponseWriter, r *http.Request) {
	n, ok := runN(w, r)
	if !ok {
		return
	}
	d, ok := s.docs(w, r)
	if !ok {
		return
	}
	out, err := d.RunDiff(r.Context(), n)
	if err != nil {
		writeDocsError(w, err, r.PathValue("id"))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleRunArtifact is GET /api/cards/{id}/runs/{n}/artifacts/{name}: one
// artifact the run's recipe declares, as text.
func (s *Server) handleRunArtifact(w http.ResponseWriter, r *http.Request) {
	n, ok := runN(w, r)
	if !ok {
		return
	}
	d, ok := s.docs(w, r)
	if !ok {
		return
	}
	out, err := d.RunArtifact(r.Context(), n, r.PathValue("name"))
	if err != nil {
		writeDocsError(w, err, r.PathValue("id"))
		return
	}
	writeJSON(w, http.StatusOK, out)
}
