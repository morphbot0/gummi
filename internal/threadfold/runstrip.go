package threadfold

// An open run's live strip (DESIGN §25.9): one line directly above the
// composer while a run is running or paused, so a person talking to the
// session keeps the run in sight after its block has scrolled up the
// thread. It is folded with the block, from the same run, steps and live
// sessions, and rides on it (Run.Live), so its words move the block's Rev
// and neither face decides what it says.

import (
	"strings"

	"github.com/morphis/gummi/internal/domain"
)

// The strip's moods: what it says, and what it offers beside Jump.
const (
	// StripRunning: steps at work (or the next one starting); Open goes to
	// the active step, or picks among several.
	StripRunning = "running"
	// StripAsking: a step asks its person; Answer opens it on its
	// question.
	StripAsking = "asking"
	// StripDeciding: the run's own decision (a revised plan, a loop at its
	// max or one that cannot judge) waits on the person, pinned above the
	// composer.
	StripDeciding = "deciding"
	// StripBudget: paused because the session's pool is spent; Top up…
	// raises the session's envelope, and the run carries on.
	StripBudget = "budget"
	// StripPaused: paused by a person, or stopping; Resume carries a
	// paused run on.
	StripPaused = "paused"
)

// RunStrip is an open run's live strip.
type RunStrip struct {
	// Name is the run in a line: "deliver · run 2".
	Name string `json:"name"`
	// Mood is one of the Strip* moods.
	Mood string `json:"mood"`
	// Steps are the run's live steps, in the order they started: Open
	// opens the one, or offers a pick of several.
	Steps []LiveStep `json:"steps,omitempty"`
	// Who names the active steps, their ids joined by " ∥ " (the asking
	// step's alone while it asks).
	Who string `json:"who,omitempty"`
	// Action is the active step's last action, the newest-started one's
	// when several are at work.
	Action string `json:"action,omitempty"`
	// Card is the step Open (or Answer) goes to, when there is one step
	// to go to.
	Card string `json:"card,omitempty"`
	// Question is the asking step's question, its first line.
	Question string `json:"question,omitempty"`
	// Line is what a strip that is not running says: the decision it
	// waits on, the spent pool, or the pause.
	Line string `json:"line,omitempty"`
	// CanTopUp offers Top up…; CanResume offers Resume.
	CanTopUp  bool `json:"canTopUp,omitempty"`
	CanResume bool `json:"canResume,omitempty"`
}

// LiveStep is one live step as the strip names it.
type LiveStep struct {
	ID    string `json:"id"`
	Path  string `json:"path"`
	Card  string `json:"card,omitempty"`
	State string `json:"state"`
}

// liveStrip folds an open run's strip; nil for a run that is not open.
// steps are the run's steps in the order they started, their live ones in
// the state their own sessions say (FoldRun's now).
func liveStrip(in RunInput, r Run, steps []domain.RunStep) *RunStrip {
	run := in.Run
	if !run.State.Open() {
		return nil
	}
	s := &RunStrip{Name: RunLine(r), Mood: StripRunning, CanResume: r.CanResume}
	var asking *domain.RunStep
	var action string
	for i := range steps {
		st := steps[i]
		if !st.State.Live() || st.Card == "" {
			continue
		}
		id := st.StepID
		if id == "" {
			id = st.Path[strings.LastIndex(st.Path, "/")+1:]
		}
		s.Steps = append(s.Steps, LiveStep{ID: id, Path: st.Path, Card: string(st.Card), State: string(st.State)})
		if st.State == domain.StepAsking && asking == nil && st.EndTree == "" {
			asking = &steps[i]
		}
		if a := in.Live[st.Path].Action; a != "" || action == "" {
			action = actionWords(a)
		}
	}
	ids := make([]string, 0, len(s.Steps))
	for _, l := range s.Steps {
		ids = append(ids, l.ID)
	}
	s.Who, s.Action = strings.Join(ids, " ∥ "), action
	if len(s.Steps) == 1 {
		s.Card = s.Steps[0].Card
	}
	if len(s.Steps) == 0 {
		s.Action = PhaseWords(r.Phase)
		if s.Action == "" {
			s.Action = "the next step is starting"
		}
	}
	d := run.Pending
	switch {
	case run.Stopping:
		s.Mood, s.Line = StripPaused, "stopping · its steps are interrupted, its changes stay in the tree"
	case (d != nil && d.Kind == domain.RunDecideBudget) || (run.OutOfBudget && run.State == domain.RunPaused):
		s.Mood, s.CanTopUp, s.CanResume = StripBudget, true, false
		pool := "budget"
		if in.Envelope > 0 {
			pool = domain.FormatDollars(in.Envelope)
		}
		s.Line = "paused · the session's " + pool + " is spent · top up to carry on"
	case d != nil:
		s.Mood, s.Line = StripDeciding, "waits on you · "+cut(Sanitize(firstLineOf(d.Question)), 140)
	case run.State == domain.RunPaused:
		s.Mood, s.Line = StripPaused, "paused"
		if why := Sanitize(firstLineOf(run.Reason)); why != "" {
			s.Line += " · " + why
		}
		s.Line += " · resume to carry on"
	case asking != nil:
		s.Mood, s.Card, s.Action = StripAsking, string(asking.Card), ""
		s.Who = s.idOf(asking.Path)
		s.Question = "a question for you"
		if l := in.Live[asking.Path]; l.Ask != nil && strings.TrimSpace(l.Ask.Question) != "" {
			s.Question = cut(Sanitize(firstLineOf(l.Ask.Question)), 140)
		}
	}
	return s
}

// idOf is the id of the live step at path.
func (s *RunStrip) idOf(path string) string {
	for _, l := range s.Steps {
		if l.Path == path {
			return l.ID
		}
	}
	return path
}
