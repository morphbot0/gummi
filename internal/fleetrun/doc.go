// Package fleetrun answers one question about a workspace: how is it
// running, and how did it get here.
//
// cardrun answers the same question at the scale of one card, from the
// card's own record. This package is that at the scale of the whole
// board: every card's record folded into one report — where the credits
// went over a window and over all time, where the hours went, how many
// lanes ran at once, and the timeline itself, per-card spans a surface
// can draw rather than sums it has to take on faith.
//
// It renders nothing, reads nothing, and touches no clock: Fold is a
// pure function of its arguments, so the stats tab and any future
// reader of the same question cannot drift apart on what the workspace
// spent. That is the same seam cardrun, verdict, rounds and gatepolicy
// already are, one scale up.
//
// # What the window charges, and to what
//
// A pass is charged to the window it started in. The rule is plain and
// therefore auditable: a long pass that spans a boundary belongs
// entirely to the window it began in, no window double-counts, and no
// number is apportioned — a fraction of a pass's cost is not a number
// the record contains.
//
// Spend on turns that are not passes (a goal's lead, a one-shot scribe,
// a backend's helper call) is charged the same way, at the one moment
// the record holds for it: its rollup row's last sample. Spend the
// card's counter holds that no row records (a decomposition at ingest)
// is charged at the card's creation. cardrun itemizes both as the run's
// Charges, and this package only windows them — so a lane is the card's
// own spend, sliced by time, and a window that holds a card's whole life
// holds exactly the total its board row and its run tab print. That is
// the promise the seam exists for: the tab cannot disagree with the
// cards it is made of. Before non-pass spend was charged, a goal's lane
// showed its passes and dropped its lead, and every verified card read
// two credits short of its own row.
//
// The all-time column is the workspace's own counters, the same figure
// every board row prints, with its stage and model buckets read off the
// rollup rows plus what the counters hold that no row does
// (cardrun.Unrecorded) — so the buckets sum to the total they sit under,
// and a window holding every card's whole life comes to that total too.
//
// Tokens follow the credits they were spent with, at both scales: the
// window's token count covers exactly the spend its credit figure
// covers, and the all-time count comes off the same rollup rows the
// all-time credits are read from. They are reported beside credits and
// never converted into them — a report that quietly priced tokens would
// be inventing the one figure only a provider can state — and the cache
// share is named because a window served from the prompt cache spent
// tokens the bill never saw.
//
// The window clock is the card-run clock re-derived over the window
// rather than summed from the per-card clocks, for one reason: the
// per-card clock stops a card's life at its last closed session, which
// is the right reading for a report read after the fact and the wrong
// one for a window whose right edge is now — a card mid-session would
// contribute nothing to "agent working" while three lanes run. The
// window clock therefore counts an open session to the right edge, and
// extends a card's life to now when anything on it is still live. The
// waiting-on-you spans are the same derivation the card's own clock
// sums (cardrun.DecisionSpans), clamped to the window instead of to the
// card's life — one correlation rule, two horizons. The same holds for
// an agent's open question: a session blocked in ask_user is time on
// you, not agent time, even though the session is still open and its
// block still draws — the window clock subtracts the ask's span from the
// agent (cardrun.AskSpans, cardrun.WorkingTime), so a lane that reads
// "on you since 14:02" is never beside a headline that says nobody was
// waiting. Lane blocks still draw the whole session — it was open — but
// peak concurrency and the busiest stretch are about agents working, and
// are read off the blocks less their asks (cardrun.WorkingSpans): a lane
// parked on a question does not run at once with another, and an hour
// in which one card waited on you is not the busiest hour.
//
// # The curator
//
// The workspace's skill curator (DESIGN §23.4) is a background job, not
// a card, and its spend is never a card's. The fold takes its ledger as
// plain runs (Input.Curator) and gives it a lane of its own
// (Report.Curator) beside the cards' rather than among them: a run is
// charged to the window it started in, the rule a pass follows, but it
// is in no bucket, no top-cards list and no card count, and
// Report.Credits — the figure the cards' lanes and buckets sum to —
// leaves it out. Report.Spent is the two together. It stays out of the
// window clock and the concurrency figures too: those measure cards'
// lives and cards working at once, and a job that never waits on a
// person has no share of either to give.
//
// # What running means
//
// "Running" is the board's word, not the record's. A session left open
// by a process that died reads on the record exactly like one working,
// and one blocked on its own question is open on the record while the
// board says the card needs you. The fold therefore takes the board's
// own set (Input.Busy) — what its header counts — and only reads the
// record where there is no board to ask: an open pass on a card nobody
// is being asked about.
package fleetrun
