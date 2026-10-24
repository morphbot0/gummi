// Package cardrun answers one question about a card: how did it run.
//
// gummi already answers the other two. The inbox says what needs a human;
// the thread says what happened; the week view says what a run produced
// and whether it was worth it. None of them says where the money and the
// hours went — which pass burned the credits, how long the card sat
// waiting on a person, what it did with its hands. The nearest thing is
// the thread's folded receipt, one line per finished stage session, which
// is the right idea at the wrong scale: per session, never totalled and
// never compared.
//
// This package is that at the scale of a whole card. It takes the
// durable record — the event log, the realized-spend rollup, the round
// counters, the check baseline — and returns one Run value holding every
// figure a surface might want. It renders nothing, reads nothing, and
// touches no clock: Report is a pure function of its arguments, so the
// TUI's run tab, the web page's stats and the week view all read the
// same numbers and cannot drift apart on what a card cost. That is the
// same seam internal/verdict, internal/rounds and internal/gatepolicy
// already are, for the same reason.
//
// # What is derived and what is stored
//
// Almost everything here is derived. Turn counts, tool counts, session
// durations, who crossed which gate — all of it is read back out of the
// event log rather than written down beside it, because a stored copy of
// a derivable fact is a second source of truth free to drift from the
// first (DESIGN §6.3, the rule that makes a folded receipt read credits
// from stage_spend rather than from its own payload).
//
// Two facts are exceptions, and both are exceptions for the same reason:
// nothing can recover them afterwards. A session's realized spend is
// keyed by session in stage_spend because the rollup is written per usage
// sample, so it is complete even for a pass that never exited. And a
// session's peak context occupancy is stamped on its stage_exit, because
// the row holding the live figure is deleted the moment the stage ends.
//
// # Whose time an open question is
//
// A session that calls ask_user stays open on the record — the question
// is answered from inside the tool call — but from the moment it asks
// until it is answered nothing but the reader can move it. That stretch
// is time on you, not agent time: every clock here subtracts an ask's
// span from the session it fell in (AskSpans, WorkingTime) before it
// charges the agent, and the span then counts as waiting on you like
// any other open decision. Other decisions standing open while a
// session runs — a gate pre-opened as a crossing is attempted — leave
// the session's time with the agent, because the session is working.
// fleetrun's window clock applies the same two helpers, so the card's
// run tab, the stats tab and the timeline cannot disagree about it.
//
// A question holds only the pass that asked it. One nobody answered —
// the session was stopped mid-question, the card was moved on by hand —
// stops taking time from the agent when that pass ends, and stops being
// time on you when the card leaves the stage it was asked in: the rule
// state.OpenDecisions applies, under which a decision whose stage has
// moved on is dead rather than waiting. Every decision span obeys the
// second half, so no wait outlives the stage that could have answered
// it.
//
// # Where a credit is counted
//
// The card's counter (Feature.Spend) is the one total: the figure its
// board row prints, the envelope is drawn against, and Money.Credits
// reports. The session-keyed rollup is how that total is attributed,
// and every credit of it lands in exactly one place — a pass, matched
// to its rows by the session key its own start stamps (attachSpend), or
// a Charge: a row no pass claims, or counter spend no row records
// (Unrecorded). A charge carries the one moment the record holds for
// it, which is what lets the workspace fold window it; a lane there is
// therefore this card's spend sliced by time, and adds up to this
// total over a window that holds the card's life.
//
// # What a pass is
//
// A session is one run of one stage by one role in one flavour — a plan
// written, a plan critiqued, an implementation, the critique that sent it
// back, the implementation again. Two sessions of the same (stage, role,
// flavour) mean the card did that work twice, and the second one is
// rework: the single most useful fact about a run, and the one the
// per-stage rollup cannot express, since it sums exactly the dimension
// that tells them apart.
package cardrun
