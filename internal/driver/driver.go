package driver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/morphis/gummi/internal/cardmint"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/state"
)

// Options configures one run or resume. Envelope is required to mint (D6).
type Options struct {
	// Envelope is the session's credit budget. On a resume it is a floor:
	// a larger one raises the budget (and the crew pre-authorisation with
	// it), a smaller one changes nothing.
	Envelope int
	Profile  string
	// Timeout is the inactivity budget: a run that hears nothing from the
	// session or its crew for this long stops as a timeout. 0 disables it.
	Timeout time.Duration
	Verbose bool   // add the session's per-tool-call activity lines to the stream
	Ref     string // optional external correlation id, echoed and persisted (D11)
	// Repo and Base place a minted session: a configured `repos:` name ("" for
	// the workspace default) and the branch its work forks from ("" for what
	// the repository has checked out).
	Repo string
	Base string
}

// crewPoll is how often the loop ticks an open crew with nothing else
// happening: the crew's own steps send EventCrew when they settle, and
// this is the backstop for one whose event was lost — the board's crew
// loop polls for the same reason.
const crewPoll = 5 * time.Second

// Driver runs one session headlessly. It is the sole consumer of
// engine.Events() for its process, so it drains the stream continuously
// while it waits — the engine's pumps block on a reader that has stopped.
type Driver struct {
	eng   *engine.Engine
	store *state.Store
	ws    state.Workspace
	out   *emitter
	opts  Options
	poll  time.Duration

	// what the stream has already carried: the session's replies, its
	// activity feed (verbose) and each crew's last state
	replies     int
	activity    int
	activityGen time.Time
	crewStates  map[domain.FeatureID]domain.CrewState
	// crews seen open during this invocation: one that ends owes the
	// session its report as a turn, and the run is not done until that
	// turn has arrived (see reportPending)
	seenOpen map[domain.FeatureID]bool
}

// New builds a driver writing its NDJSON stream to out. The caller owns
// the engine and agent lifetime.
func New(eng *engine.Engine, store *state.Store, ws state.Workspace, out io.Writer, opts Options) *Driver {
	return &Driver{
		eng: eng, store: store, ws: ws, out: newEmitter(out, opts.Verbose), opts: opts,
		poll:       crewPoll,
		crewStates: map[domain.FeatureID]domain.CrewState{},
		seenOpen:   map[domain.FeatureID]bool{},
	}
}

// Create mints the session a prompt starts, and persists it, but does not
// start it: the caller takes the card's lock between the two.
func (d *Driver) Create(ctx context.Context, prompt string) (domain.Feature, error) {
	if strings.TrimSpace(prompt) == "" {
		return domain.Feature{}, errors.New("run needs a prompt")
	}
	f, err := cardmint.Mint(ctx, d.store, d.ws, cardmint.Input{
		Description: prompt, Profile: d.opts.Profile,
		Envelope: d.opts.Envelope, Repo: d.opts.Repo, RequireRepo: d.eng.RequireRepo,
		Base: d.opts.Base, ExternalRef: d.opts.Ref,
	})
	if err != nil {
		return domain.Feature{}, err
	}
	d.out.Emit(createdEvent{
		Event: "created", ID: string(f.ID), Ref: d.opts.Ref,
		Branch: f.BranchName(), Envelope: f.Budget.Envelope, Preauth: f.Budget.Envelope,
	})
	return f, nil
}

// Start pre-authorises the session's crew up to its envelope, sends the
// prompt as its first turn, and drives it to a terminal Outcome.
func (d *Driver) Start(ctx context.Context, f domain.Feature, prompt string) (Outcome, error) {
	if err := d.eng.SetCrewPreauth(ctx, f.ID, f.Budget.Envelope); err != nil {
		return d.fail(ctx, string(f.ID), err)
	}
	ff, err := d.eng.OpenFreeform(ctx, f)
	if err != nil {
		return d.fail(ctx, string(f.ID), err)
	}
	if err := ff.KickoffWith(ctx, prompt); err != nil {
		return d.fail(ctx, string(f.ID), err)
	}
	return d.loop(ctx, f.ID)
}

// ResumeInput is what a resume brings. Answer answers the session's open
// question or its crew's open decision; Message, with nothing open, is
// the session's next turn. Both empty carries on: a paused crew is
// resumed, and a raised envelope tells the session to carry on.
type ResumeInput struct {
	Answer  *string
	Message string
}

// Resume picks a session back up: it rehydrates the engine's persisted
// sessions, applies the caller's answer or turn, and drives on.
func (d *Driver) Resume(ctx context.Context, id domain.FeatureID, in ResumeInput) (Outcome, error) {
	if err := d.eng.Restore(ctx); err != nil {
		return d.fail(ctx, string(id), fmt.Errorf("restoring sessions: %w", err))
	}
	f, err := d.store.GetFeature(ctx, id)
	if err != nil {
		return d.fail(ctx, string(id), err)
	}
	switch {
	case !f.IsFreeform():
		return d.fail(ctx, string(id), fmt.Errorf(
			"%s is a %s card from the retired workflow: it is kept to read, and nothing drives it now", id, f.Kind))
	case f.InCrew():
		return d.fail(ctx, string(id), fmt.Errorf("%s is a crew's own card: resume the session that owns it", id))
	case f.Stage == domain.StageDone:
		return d.fail(ctx, string(id), fmt.Errorf("%s is closed", id))
	}
	d.out.Emit(resumedEvent{Event: "resumed", ID: string(id), Ref: d.opts.Ref})

	raised := false
	if d.opts.Envelope > f.Budget.Envelope {
		from := f.Budget.Envelope
		// through the engine: a crew paused because the session's money ran
		// out carries on once the raise lands (§24.7a)
		if err := d.eng.RaiseEnvelope(ctx, id, d.opts.Envelope); err != nil {
			return d.fail(ctx, string(id), fmt.Errorf("raising envelope: %w", err))
		}
		f.Budget.Envelope = d.opts.Envelope
		if err := d.eng.SetCrewPreauth(ctx, id, f.Budget.Envelope); err != nil {
			return d.fail(ctx, string(id), err)
		}
		d.out.Emit(envelopeRaisedEvent{Event: "envelope", ID: string(id), From: from, To: f.Budget.Envelope})
		raised = true
	}

	ff := d.eng.Freeform(id)
	if ff == nil {
		if ff, err = d.eng.OpenFreeform(ctx, f); err != nil {
			return d.fail(ctx, string(id), err)
		}
	}
	// what this session said before this invocation was streamed by the
	// one that said it
	d.prime(ff.Snapshot())

	// A crew left running by a process that is gone is parked by its first
	// tick (decision 17: only a person runs one again) — and a person
	// running `resume` is that person.
	if _, err := d.eng.CrewTick(ctx, id); err != nil {
		return d.fail(ctx, string(id), err)
	}
	// one paused because the money ran out waits for a raise instead: its
	// budget decision says so
	if c, _ := d.store.OpenCrew(ctx, id); c != nil && c.State == domain.CrewPaused && !c.OutOfBudget {
		if err := d.eng.CrewResume(ctx, id); err != nil {
			return d.fail(ctx, string(id), err)
		}
	}

	ask := ff.Snapshot().PendingAsk
	_, decision := d.eng.CrewDecision(ctx, id)
	switch {
	case in.Answer != nil && ask != nil:
		err = d.eng.AnswerAs(ctx, id, *in.Answer, state.ActorUser)
	case in.Answer != nil && decision != nil:
		err = d.eng.CrewAnswer(ctx, id, decision.ID, *in.Answer, state.ActorUser)
	case in.Answer != nil:
		err = fmt.Errorf("%s has no open question to answer — resume it with a message instead", id)
	case in.Message != "" && (ask != nil || decision != nil):
		err = fmt.Errorf("%s is waiting on an answer: pass it with --answer", id)
	case in.Message != "":
		err = ff.Send(ctx, in.Message)
	case raised && ask == nil && decision == nil:
		err = ff.Send(ctx, fmt.Sprintf("Your envelope was raised to %s: carry on where you stopped.",
			domain.FormatDollars(float64(f.Budget.Envelope))))
	}
	if err != nil {
		return d.fail(ctx, string(id), err)
	}
	return d.loop(ctx, id)
}

// loop drives the session until it stops: it ticks the session's crew,
// streams what happened, and checks for a terminal state after every
// event.
func (d *Driver) loop(ctx context.Context, id domain.FeatureID) (Outcome, error) {
	poll := time.NewTicker(d.poll)
	defer poll.Stop()
	var quiet <-chan time.Time
	var timer *time.Timer
	if d.opts.Timeout > 0 {
		timer = time.NewTimer(d.opts.Timeout)
		defer timer.Stop()
		quiet = timer.C
	}
	tick := true
	for {
		if out, stop, err := d.check(ctx, id); stop {
			return out, err
		}
		if tick {
			if _, err := d.eng.CrewTick(ctx, id); err != nil {
				d.out.Activity(string(id), "crew", "crew tick failed: "+err.Error())
			}
			tick = false
		}
		select {
		case <-ctx.Done():
			return d.fail(ctx, string(id), ctx.Err())
		case <-quiet:
			return d.timeout(ctx, id), nil
		case <-poll.C:
			tick = true
		case ev, ok := <-d.eng.Events():
			if !ok {
				return d.fail(ctx, string(id), errors.New("engine event stream closed"))
			}
			if timer != nil {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(d.opts.Timeout)
			}
			if ev.Feature == id && ev.Kind == engine.EventCrew {
				tick = true
			}
		}
	}
}

// check streams what is new and decides whether the run stops here.
func (d *Driver) check(ctx context.Context, id domain.FeatureID) (Outcome, bool, error) {
	f, err := d.store.GetFeature(ctx, id)
	if err != nil {
		out, err := d.fail(ctx, string(id), err)
		return out, true, err
	}
	ff := d.eng.Freeform(id)
	var snap engine.Snapshot
	if ff != nil {
		snap = ff.Snapshot()
	}
	d.stream(id, snap)
	crew, decision := d.eng.CrewDecision(ctx, id)
	d.streamCrews(ctx, id)

	switch {
	case f.Stage == domain.StageDone:
		return d.done(ctx, id), true, nil
	case snap.PendingAsk != nil:
		return d.askStop(id, snap.PendingAsk), true, nil
	case decision != nil:
		return d.decisionStop(id, crew, decision), true, nil
	case crew != nil && crew.State == domain.CrewPaused:
		return d.pausedStop(id, crew), true, nil
	case snap.Exhausted:
		return d.exhausted(ctx, id), true, nil
	case snap.Err != nil && !snap.Busy:
		out, err := d.fail(ctx, string(id), snap.Err)
		return out, true, err
	}
	if snap.Busy || len(snap.Queued) > 0 || (ff != nil && ff.Watching()) || crew != nil || d.reportPending(ctx, id, snap) {
		return Outcome{}, false, nil
	}
	return d.done(ctx, id), true, nil
}

// reportPending says whether a crew this run saw open has ended with a
// report the session has not answered yet. The crew is written ended a
// moment before its report goes to the session as a turn, and the turn is
// on the transcript a moment before the session reads as busy with it;
// in both gaps the session is idle with no crew open — which is not done.
func (d *Driver) reportPending(ctx context.Context, id domain.FeatureID, snap engine.Snapshot) bool {
	crews, err := d.store.SessionCrews(ctx, id)
	if err != nil {
		return false
	}
	for _, c := range crews {
		if !d.seenOpen[c.ID] || !c.State.Ended() || c.Report == "" {
			continue
		}
		answered := false
		for i, m := range snap.Transcript {
			if m.Author != engine.AuthorUser || m.By != state.ActorCrew || !strings.Contains(m.Content, c.Report) {
				continue
			}
			for _, r := range snap.Transcript[i+1:] {
				if r.Author == engine.AuthorAssistant && !r.Streaming {
					answered = true
					break
				}
			}
		}
		if !answered {
			return true
		}
	}
	return false
}

// prime marks what a session already carries as streamed.
func (d *Driver) prime(snap engine.Snapshot) {
	d.replies = len(snap.Transcript)
	d.activity = len(snap.Activity)
	d.activityGen = snap.StartedAt
}

// stream emits the session's finished replies, and under verbose its new
// activity lines.
func (d *Driver) stream(id domain.FeatureID, snap engine.Snapshot) {
	if d.replies > len(snap.Transcript) {
		// the conversation was restarted under it (/compact, /handoff)
		d.replies = len(snap.Transcript)
	}
	for ; d.replies < len(snap.Transcript); d.replies++ {
		m := snap.Transcript[d.replies]
		if m.Streaming {
			break
		}
		if m.Author == engine.AuthorAssistant && strings.TrimSpace(m.Content) != "" {
			d.out.Emit(replyEvent{Event: "reply", ID: string(id), Text: m.Content})
		}
	}
	if !d.out.Verbose() {
		return
	}
	if snap.StartedAt != d.activityGen || d.activity > len(snap.Activity) {
		// a respawned backend starts a feed of its own
		d.activityGen, d.activity = snap.StartedAt, 0
	}
	for ; d.activity < len(snap.Activity); d.activity++ {
		d.out.Activity(string(id), "session", snap.Activity[d.activity])
	}
}

// streamCrews emits a crew event whenever one of the session's crews
// changes state.
func (d *Driver) streamCrews(ctx context.Context, id domain.FeatureID) {
	crews, err := d.store.SessionCrews(ctx, id)
	if err != nil {
		return
	}
	for _, c := range crews {
		if c.State.Open() {
			d.seenOpen[c.ID] = true
		}
		if d.crewStates[c.ID] == c.State {
			continue
		}
		d.crewStates[c.ID] = c.State
		if !d.seenOpen[c.ID] {
			continue // a crew that ended before this run is history, not news
		}
		d.out.Emit(crewEvent{
			Event: "crew", ID: string(id), Crew: c.Seq, State: string(c.State),
			Budget: c.Budget, Nodes: len(c.Plan.Nodes), Report: c.Report,
		})
	}
}

// --- terminal outcomes -------------------------------------------------

func (d *Driver) done(ctx context.Context, id domain.FeatureID) Outcome {
	f, _ := d.store.GetFeature(ctx, id)
	d.out.Emit(doneEvent{
		Event: "done", ID: string(id), Branch: f.BranchName(), Spent: f.Spend.Credits,
		PullRequest: f.PullRequest.StatusPayload(),
	})
	return Outcome{Status: StatusDone, ID: string(id)}
}

// askStop is the session's own question.
func (d *Driver) askStop(id domain.FeatureID, ask *engine.Ask) Outcome {
	labels := make([]string, 0, len(ask.Options))
	for _, o := range ask.Options {
		labels = append(labels, o.Label)
	}
	d.out.Emit(questionEvent{
		Event: "question", ID: string(id), Kind: "ask", Q: ask.Question,
		Options: labels, Recommended: engine.RecommendedOption(ask), FreeForm: true,
		Decision: ask.DecisionID, Resume: string(id),
		Next: resumeCmd(string(id), "--answer", `"<answer>"`),
	})
	return Outcome{Status: StatusNeedsYou, ID: string(id)}
}

// decisionStop is the crew's decision the pre-authorisation did not cover.
func (d *Driver) decisionStop(id domain.FeatureID, c *domain.Crew, dec *domain.CrewDecision) Outcome {
	rec := ""
	if len(dec.Options) > 0 {
		rec = dec.Options[0]
	}
	ev := questionEvent{
		Event: "question", ID: string(id), Kind: "crew_" + string(dec.Kind), Q: dec.Question,
		Options: dec.Options, Recommended: rec, FreeForm: true,
		Decision: dec.ID, Resume: string(id),
		Next: resumeCmd(string(id), "--answer", `"<answer>"`),
	}
	if c != nil {
		ev.Crew = c.Seq
	}
	switch dec.Kind {
	case domain.CrewDecideQuestion, domain.CrewDecideNode:
		ev.Node = dec.Node
	case domain.CrewDecideBudget:
		// a raise of the session's envelope is the top-up: it answers the
		// decision and carries a paused crew on (§24.7a)
		if f, err := d.store.GetFeature(context.Background(), id); err == nil && f.Budget.Envelope > 0 {
			ev.Next = resumeCmd(string(id), "--envelope", domain.DollarsInput(f.Budget.Envelope+dec.Amount))
		}
	}
	d.out.Emit(ev)
	return Outcome{Status: StatusNeedsYou, ID: string(id)}
}

// pausedStop is a crew a person paused (or a quit parked): only a person
// runs it again, and a bare resume is that.
func (d *Driver) pausedStop(id domain.FeatureID, c *domain.Crew) Outcome {
	d.out.Emit(questionEvent{
		Event: "question", ID: string(id), Kind: "crew_paused", Crew: c.Seq,
		Q:      fmt.Sprintf("Crew %d is paused. Resume it?", c.Seq),
		Resume: string(id), Next: resumeCmd(string(id)),
	})
	return Outcome{Status: StatusNeedsYou, ID: string(id)}
}

func (d *Driver) exhausted(ctx context.Context, id domain.FeatureID) Outcome {
	f, _ := d.store.GetFeature(ctx, id)
	// a raise that clears what is already spent, with headroom: doubling
	// alone can land below recorded spend, and buy nothing
	suggested := f.Budget.Envelope * 2
	if bySpend := int(math.Ceil(f.Spend.Credits * 1.2)); bySpend > suggested {
		suggested = bySpend
	}
	d.out.Emit(exhaustedEvent{
		Event: "exhausted", ID: string(id), Spent: f.Spend.Credits, Envelope: f.Budget.Envelope,
		Resume:        string(id),
		Next:          resumeCmd(string(id), "--envelope", domain.DollarsInput(suggested)),
		Preconditions: d.resumePreconditions(id),
	})
	return Outcome{Status: StatusExhausted, ID: string(id)}
}

func (d *Driver) timeout(_ context.Context, id domain.FeatureID) Outcome {
	d.out.Emit(timeoutEvent{
		Event: "timeout", ID: string(id),
		Hint: "nothing happened on the session or its crew for the whole --timeout window. Check gummi is not " +
			"still running (preconditions.check_running); otherwise the backend stalled or lost auth, or the " +
			"work needs longer — resume with a larger --timeout.",
		TimeoutUsed: d.opts.Timeout.String(), Resume: string(id),
		Next:          resumeCmd(string(id)),
		Preconditions: d.resumePreconditions(id),
	})
	return Outcome{Status: StatusTimeout, ID: string(id)}
}

// resumePreconditions is the pid probe attached to the stops whose `next`
// starts a new gummi: it warns when this card's recorded pid is still
// alive, so a caller waits instead of hitting the card lock.
func (d *Driver) resumePreconditions(id domain.FeatureID) *resumePreconditions {
	pid := d.ws.PIDFile(id)
	if pid == "" {
		return nil
	}
	return &resumePreconditions{
		CheckRunning: "pid=$(cat " + pid + " 2>/dev/null); " +
			"[ -n \"$pid\" ] && kill -0 \"$pid\" 2>/dev/null && " +
			"echo \"gummi still running as pid $pid — wait before resuming\"",
	}
}

// fail emits the error line and returns the StatusError outcome plus the
// error. A failure that left an open session behind is resumable.
func (d *Driver) fail(ctx context.Context, id string, err error) (Outcome, error) {
	ev := errorEvent{Event: "error", ID: id, Error: err.Error(), Hint: backendHint(err.Error())}
	if id != "" {
		// detached: a cancelled ctx is often the very failure reported
		if f, gerr := d.store.GetFeature(context.WithoutCancel(ctx), domain.FeatureID(id)); gerr == nil &&
			f.IsFreeform() && !f.InCrew() && f.Stage != domain.StageDone {
			ev.Resumable = true
			ev.Stage = string(f.Stage)
			ev.Next = resumeCmd(id)
		}
	}
	d.out.Emit(ev)
	return Outcome{Status: StatusError, ID: id}, err
}

// backendHint returns a short remediation note when a failure's message
// looks like a backend disconnect, a stall or an auth problem — the
// conditions most often misread as a gummi bug. Empty for anything else.
func backendHint(msg string) string {
	l := strings.ToLower(msg)
	switch {
	case strings.Contains(l, "auth") || strings.Contains(l, "401") || strings.Contains(l, "403") ||
		strings.Contains(l, "unauthorized") || strings.Contains(l, "forbidden") || strings.Contains(l, "credential"):
		return "the backend agent looks unauthenticated — re-auth it (run the agent's login) and resume"
	case strings.Contains(l, "stall") || strings.Contains(l, "stream") || strings.Contains(l, "mid-session") ||
		strings.Contains(l, "aborted") || strings.Contains(l, "disconnect") || strings.Contains(l, "eof"):
		return "the backend agent's stream died mid-turn — usually a transient backend/network drop or lost auth; check the agent and resume"
	}
	return ""
}

// resumeCmd formats the copy-pasteable command that continues a stopped
// run — the `next` field on every stop. A value the caller supplies is a
// <placeholder>.
func resumeCmd(id string, args ...string) string {
	cmd := "gummi resume " + id
	if len(args) > 0 {
		cmd += " " + strings.Join(args, " ")
	}
	return cmd
}
