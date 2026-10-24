package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
)

// One line says the same of a child whether the agent asks for it or a face
// draws it.
func TestDispatchViewLine(t *testing.T) {
	base := DispatchView{Card: "FF-2", Title: "child", Run: "no open run", Spent: 1, Envelope: 5}
	for _, c := range []struct {
		name string
		v    DispatchView
		want string
		not  string
	}{
		{"open", base, "FF-2 child: open, no open run, spent", "envelope spent"},
		{"closed", func() DispatchView { v := base; v.Closed, v.Exhausted, v.Asking = true, true, true; return v }(), "closed", "waiting"},
		{"spent", func() DispatchView { v := base; v.Exhausted = true; return v }(), "envelope spent", "waiting"},
		{"asking", func() DispatchView { v := base; v.Asking = true; return v }(), "waiting for the person", "envelope spent"},
		{"gone", DispatchView{Card: "FF-9", Gone: true}, "FF-9: gone", "spent"},
	} {
		got := c.v.Line()
		if !strings.Contains(got, c.want) || strings.Contains(got, c.not) {
			t.Errorf("%s: %q", c.name, got)
		}
	}
}

func TestDispatchViewsAndDispatchedBy(t *testing.T) {
	e, store, f, _ := watchEngine(t, newWFFake(), 2000)
	ctx := context.Background()
	open := mainCheckoutCard(2, "open child")
	done := mainCheckoutCard(3, "done child")
	done.Stage = domain.StageDone
	for _, c := range []domain.Feature{open, done} {
		createFeature(t, store, c)
	}
	for i, c := range []domain.FeatureID{open.ID, done.ID, "FF-77"} {
		if err := store.RecordDispatch(ctx, state.Dispatch{Card: c, Agent: f.ID, Run: 1, Path: "p" + string(rune('a'+i)), Envelope: 4}); err != nil {
			t.Fatal(err)
		}
	}
	vs := e.DispatchViews(ctx, f.ID)
	if len(vs) != 3 || vs[0].Closed || vs[0].Asking || !vs[1].Closed || !vs[2].Gone || vs[2].Envelope != 4 {
		t.Fatalf("views: %+v", vs)
	}
	if a, ok := e.DispatchedBy(ctx, open.ID); !ok || a != f.ID {
		t.Errorf("DispatchedBy = %v %v", a, ok)
	}
	if _, ok := e.DispatchedBy(ctx, f.ID); ok {
		t.Error("an agent reads as dispatched")
	}
	// a card no agent dispatched is no one's note, and a dispatched one is
	// noted without a panic when its agent holds no live session
	e.NoteDispatcherClosed(ctx, f.ID)
	e.NoteDispatcherClosed(ctx, open.ID)
	// Exhausted is not read of a closed session
	if vs[1].Exhausted {
		t.Errorf("a closed session reads as spent: %+v", vs[1])
	}
}
