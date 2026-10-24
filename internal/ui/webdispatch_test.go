package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/webapi"
)

// dispatchPair is an agent and a session it dispatched, on a headless board.
func dispatchPair(t *testing.T) (*Bridge, domain.Feature, domain.Feature) {
	t.Helper()
	now := time.Now()
	mk := func(num int, id, title string) domain.Feature {
		return domain.Feature{
			ID: domain.FeatureID(id), Num: num, Kind: domain.KindFreeform, Title: title, Slug: strings.ReplaceAll(title, " ", "-"),
			Stage: domain.StageOpen, BranchScheme: domain.BranchSchemeKind, CreatedAt: now, UpdatedAt: now,
			MainCheckout: true, Budget: domain.Budget{Envelope: 400},
		}
	}
	ag, child := mk(1, "FF-001", "watcher"), mk(2, "FF-002", "bump lib")
	ag.Home = true
	b, _, _, _ := headlessBoardWith(t, agent.NewFake("ok"), []domain.Feature{ag, child}, nil)
	waitBoard(t, b, func(bd webapi.Board) bool { return len(bd.Rows) == 2 })
	err := b.Do(context.Background(), func(m *Shell) tea.Cmd {
		if err := m.store.RecordDispatch(context.Background(), state.Dispatch{Card: child.ID, Agent: ag.ID, Run: 1, Path: "bump", Envelope: 100, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		return m.loadRows
	})
	if err != nil {
		t.Fatal(err)
	}
	return b, ag, child
}

func TestWebAgentThreadCarriesDispatchedSession(t *testing.T) {
	b, ag, child := dispatchPair(t)
	d := webDocsThread(t, b, string(ag.ID))
	th, err := d.Thread(context.Background(), 1<<40)
	if err != nil {
		t.Fatal(err)
	}
	var got *webapi.DispatchBlock
	for _, it := range th.Items {
		if it.T == webapi.ItemDispatch && it.Key == "dispatch:"+string(child.ID) {
			got = it.Dispatch
		}
	}
	if got == nil {
		t.Fatalf("no dispatch item, even past every event: %+v", th.Items)
	}
	if got.Card != string(child.ID) || !strings.Contains(got.Title, "bump lib") || got.State != "waiting for input" || got.Envelope != 400 || got.Rev == "" {
		t.Errorf("block %+v", got)
	}
}
