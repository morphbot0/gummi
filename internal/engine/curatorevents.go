package engine

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/morphis/gummi/internal/cardrun"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
)

// What a workflow card teaches the curator (DESIGN §23.4, §23.5). Stage
// sessions keep no memory: their lessons are in the card-event log — a
// critique or review that asked for changes, a verify that failed, a stage
// sent back down the graph, a question a person answered. The passes and
// the check tallies are read through cardrun, the one fold of a card's
// log, so the curator cannot disagree with the run tab about what
// happened; the rest is read off the event payloads the store defines.

const (
	curatorEventsTool = "curator_events"
	// curatorMaxEventCards bounds how many cards curator_inputs lists for
	// their events, newest activity first.
	curatorMaxEventCards = 30
	// curatorMaxCardNotes bounds curator_events: the newest notes win.
	curatorMaxCardNotes = 40
	// curatorNoteLen bounds one quoted text (a pass's last word, a
	// question, an answer).
	curatorNoteLen = 600
)

// The kinds of note, as curator_inputs counts them.
const (
	noteChanges = "changes" // a critique or review asked for changes
	noteVerify  = "verify"  // a verify pass said fail or blocked
	noteCheck   = "check"   // a gummi check failed
	noteRerun   = "rerun"   // the card was sent back down the graph
	noteAsk     = "ask"     // an ask_user question was answered
	notePark    = "park"    // the card gave up or was blocked
)

// cardNote is one notable thing a card's log says.
type cardNote struct {
	At   time.Time
	Kind string
	Line string
}

// curatorNotes folds one card's whole log into its notable events at or after
// since, oldest first. Failed checks are tallied per check rather than per
// run (cardrun's own tally); each lands at the last failure's time.
func curatorNotes(evs []state.CardEvent, since time.Time) []cardNote {
	folded := state.FoldToolResults(evs)
	var notes []cardNote
	run := cardrun.Report(cardrun.Input{Events: evs})
	for _, s := range run.Sessions {
		if !s.Closed || s.EndInferred || s.Ended.Before(since) {
			continue
		}
		kind := ""
		switch s.Verdict {
		case "changes":
			kind = noteChanges
		case "fail", "blocked":
			kind = noteVerify
		default:
			continue
		}
		who := strings.TrimSpace(string(s.Stage) + " " + s.Flavor)
		line := fmt.Sprintf("%s pass said %s", who, s.Verdict)
		if s.Role != "" {
			line = fmt.Sprintf("%s pass (%s) said %s", who, s.Role, s.Verdict)
		}
		if last := lastAssistantIn(folded, s.Stage, s.Started, s.Ended); last != "" {
			line += ": " + oneLine(clip(last, curatorNoteLen))
		}
		notes = append(notes, cardNote{At: s.Ended, Kind: kind, Line: line})
	}

	var recent []state.CardEvent
	lastFail := map[string]time.Time{}
	for _, ev := range folded {
		if ev.At.Before(since) {
			continue
		}
		recent = append(recent, ev)
		switch ev.Kind {
		case state.EventGate:
			var p state.GatePayload
			if json.Unmarshal([]byte(ev.Payload), &p) != nil || !walksBack(domain.Stage(p.From), domain.Stage(p.To)) {
				continue
			}
			line := fmt.Sprintf("sent back %s → %s", p.From, p.To)
			if by := cmp.Or(p.By, p.Actor); by != "" {
				line += " by " + by
			}
			notes = append(notes, cardNote{At: ev.At, Kind: noteRerun, Line: line})
		case state.EventAsk:
			var p state.AskPayload
			if json.Unmarshal([]byte(ev.Payload), &p) != nil || strings.TrimSpace(p.Answer) == "" {
				continue
			}
			by := cmp.Or(p.By, p.Actor, "someone")
			notes = append(notes, cardNote{At: ev.At, Kind: noteAsk, Line: fmt.Sprintf("asked %q; %s answered %q",
				oneLine(clip(p.Question, curatorNoteLen)), by, oneLine(clip(p.Answer, curatorNoteLen)))})
		case state.EventPark:
			var p state.ParkPayload
			if json.Unmarshal([]byte(ev.Payload), &p) != nil ||
				(p.Reason != state.ParkReasonGaveUp && p.Reason != state.ParkReasonBlocked) {
				continue
			}
			line := "stopped: " + p.Reason
			if p.Detail != "" {
				line += ": " + oneLine(clip(p.Detail, curatorNoteLen))
			}
			notes = append(notes, cardNote{At: ev.At, Kind: notePark, Line: line})
		case state.EventTool:
			if ev.Status == state.StatusFail {
				var p state.ToolPayload
				if json.Unmarshal([]byte(ev.Payload), &p) == nil && p.Tool == "" {
					lastFail[p.Label] = ev.At
				}
			}
		}
	}
	// The check tally is cardrun's: it alone decides what is a gummi
	// check and what is an agent's tool.
	for _, c := range cardrun.Report(cardrun.Input{Events: recent}).Hands.Checks {
		if c.Fails == 0 {
			continue
		}
		at := since
		for label, t := range lastFail {
			if strings.HasPrefix(label, "check "+c.Name+":") && t.After(at) {
				at = t
			}
		}
		line := fmt.Sprintf("check %s failed %d of %d runs", c.Name, c.Fails, c.Runs)
		notes = append(notes, cardNote{At: at, Kind: noteCheck, Line: line})
	}
	sort.SliceStable(notes, func(i, j int) bool { return notes[i].At.Before(notes[j].At) })
	return notes
}

// lastAssistantIn is the last assistant message a pass at stage wrote
// between from and to: where a critique states its findings and a verify
// its failure.
func lastAssistantIn(evs []state.CardEvent, stage domain.Stage, from, to time.Time) string {
	last := ""
	for _, ev := range evs {
		if ev.Kind != state.EventMessage || ev.Stage != stage || ev.At.Before(from) || ev.At.After(to) {
			continue
		}
		var m struct{ Author, Content string }
		if json.Unmarshal([]byte(ev.Payload), &m) == nil && m.Author == string(AuthorAssistant) && strings.TrimSpace(m.Content) != "" {
			last = m.Content
		}
	}
	return last
}

// walksBack reports whether from → to goes down the graph's stage order:
// a rerun edge taken, not a gate crossed forward.
func walksBack(from, to domain.Stage) bool {
	fi, ti := slices.Index(domain.Stages, from), slices.Index(domain.Stages, to)
	return fi >= 0 && ti >= 0 && ti < fi
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// eventCards lists the cards with notable events since the curator last read
// them (or ever, for a card it never read), newest
// activity first, each with its notes counted: what curator_inputs shows.
func (ct *curatorTurn) eventCards(ctx context.Context) ([]eventCard, error) {
	store := ct.e.cfg.Store
	if store == nil {
		return nil, nil
	}
	// every card's own cutoff is its last read, so the scan starts at the
	// oldest of them: the beginning, while any card is still unread
	evs, err := store.WorkspaceEvents(ctx, time.Time{})
	if err != nil {
		return nil, err
	}
	// newest notable-looking activity per card, before reading any card
	// in full
	latest := map[domain.FeatureID]time.Time{}
	for _, ev := range evs {
		switch {
		case ev.Kind == state.EventStageExit, ev.Kind == state.EventGate,
			ev.Kind == state.EventAsk, ev.Kind == state.EventPark,
			ev.Kind == state.EventTool && ev.Status == state.StatusFail,
			ev.Kind == state.EventToolResult && ev.Status == state.StatusFail:
			if ev.At.After(latest[ev.Feature]) && ev.At.After(ct.seen.events[string(ev.Feature)]) {
				latest[ev.Feature] = ev.At
			}
		}
	}
	ids := make([]domain.FeatureID, 0, len(latest))
	for id := range latest {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if !latest[ids[i]].Equal(latest[ids[j]]) {
			return latest[ids[i]].After(latest[ids[j]])
		}
		return ids[i] < ids[j]
	})
	var out []eventCard
	for _, id := range ids {
		if len(out) == curatorMaxEventCards {
			break
		}
		all, err := store.Events(ctx, id)
		if err != nil {
			continue
		}
		notes := curatorNotes(all, ct.seen.events[string(id)])
		if len(notes) == 0 {
			continue
		}
		c := eventCard{ID: id, Counts: map[string]int{}}
		for _, n := range notes {
			c.Counts[n.Kind]++
		}
		out = append(out, c)
	}
	return out, nil
}

type eventCard struct {
	ID     domain.FeatureID
	Counts map[string]int
}

// summary says a card's counts in one clause.
func (c eventCard) summary() string {
	var parts []string
	for _, k := range []struct{ kind, one, many string }{
		{noteChanges, "1 critique or review asked for changes", "%d critiques or reviews asked for changes"},
		{noteVerify, "1 failed verify", "%d failed verifies"},
		{noteCheck, "1 failing check", "%d failing checks"},
		{noteRerun, "sent back once", "sent back %d times"},
		{noteAsk, "1 answered question", "%d answered questions"},
		{notePark, "stopped once", "stopped %d times"},
	} {
		switch n := c.Counts[k.kind]; {
		case n == 1:
			parts = append(parts, k.one)
		case n > 1:
			parts = append(parts, fmt.Sprintf(k.many, n))
		}
	}
	return strings.Join(parts, ", ")
}

// readEvents is curator_events: one card's notable events across its whole
// history, the newest curatorMaxCardNotes of them.
func (ct *curatorTurn) readEvents(ctx context.Context, card string) (string, error) {
	store := ct.e.cfg.Store
	if store == nil {
		return "(no card events here)", nil
	}
	f, err := store.ResolveFeature(ctx, card)
	if err != nil {
		return "", fmt.Errorf("no card %q", card)
	}
	evs, err := store.Events(ctx, f.ID)
	if err != nil {
		return "", err
	}
	notes := curatorNotes(evs, time.Time{})
	since := ct.seen.events[string(f.ID)]
	var b strings.Builder
	fmt.Fprintf(&b, "Card %s %s (%s, at %s): %d notable events", f.ID, f.Name(), f.TypeName(), f.Stage, len(notes))
	if len(notes) > curatorMaxCardNotes {
		fmt.Fprintf(&b, ", the newest %d shown", curatorMaxCardNotes)
		notes = notes[len(notes)-curatorMaxCardNotes:]
	}
	b.WriteString("\n")
	for _, n := range notes {
		mark := ""
		if n.At.After(since) {
			mark = " (new)"
		}
		fmt.Fprintf(&b, "- %s%s %s\n", n.At.UTC().Format("2006-01-02 15:04"), mark, n.Line)
	}
	ct.markRead(true, string(f.ID))
	return clip(b.String(), curatorMaxInputLen), nil
}
