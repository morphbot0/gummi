package engine

import (
	"context"
	"time"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
)

// limitSlack is how long past a backend's stated reset time the resume
// waits, so the first turn does not land a second before the window opens.
const limitSlack = time.Minute

// limitResumeLine is the turn sent once the limit lifts. It is only a nudge:
// the backend resumes its own conversation, which holds what it was doing.
const limitResumeLine = "Your usage limit has reset. Please continue where you left off."

// resumeAfterLimit handles a turn that failed on the backend's usage limit
// ("You've hit your session limit · resets 3:20am (UTC)"): rather than leave
// the card failed, it notes when the limit lifts and sets a timer that sends
// the session a turn then. Reading the message and waiting cost no model
// call. It reports whether err was a usage limit; the caller then treats the
// turn as paused, not failed.
func (ff *FreeformSession) resumeAfterLimit(sess *Session, err error) bool {
	e := ff.engine
	now := e.now()
	reset, ok := agent.ParseUsageLimit(err.Error(), now)
	if !ok {
		return false
	}
	wait := reset.Sub(now) + e.limitSlack
	sess.setBusy(false)
	sess.appendSystem("the backend's usage limit was reached — this session carries on by itself after it resets at " +
		reset.Format("15:04 MST") + " (in " + wait.Round(time.Minute).String() + ")")
	e.persist(sess)

	ff.idleMu.Lock()
	if ff.limitTimer != nil {
		ff.limitTimer.Stop()
	}
	ff.limitTimer = time.AfterFunc(wait, func() { ff.resumeNow(sess) })
	ff.idleMu.Unlock()
	return true
}

// resumeNow sends the resume turn. If the limit has not really lifted (the
// clock the message names drifted), the turn fails on the same message and
// resumeAfterLimit sets the next wait.
func (ff *FreeformSession) resumeNow(sess *Session) {
	ff.mu.Lock()
	current := ff.sess == sess
	ff.mu.Unlock()
	if !current {
		return // the session was closed or replaced meanwhile
	}
	ctx := context.Background()
	if err := ff.Send(ctx, limitResumeLine); err != nil {
		sess.appendSystem("could not resume after the usage limit: " + err.Error())
		ff.engine.send(Event{Feature: ff.id, Stage: domain.StageOpen, Kind: EventUpdated})
	}
}
