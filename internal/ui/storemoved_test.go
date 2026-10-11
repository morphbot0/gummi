package ui

import (
	"context"
	"testing"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
)

// Another process writing to the store — a card minted from the CLI, a
// run beside the board that stopped at its gate — reaches the board on
// the next probe, rows and needs-you queue alike, without a restart.
func TestACommitByAnotherProcessReachesTheBoard(t *testing.T) {
	m, _ := chatWorkspace(t, agent.NewFake("ok"))
	ctx := context.Background()
	// the first probe only takes the store's version
	m = pump(t, m, m.probeForeign())
	if m.storeVersion == 0 {
		t.Fatal("the probe read no data_version")
	}

	other, err := state.OpenStore(m.ws.DBFile())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	f := domain.Feature{ID: "FD-002", Num: 2, Title: "Made elsewhere", Slug: "made-elsewhere", Stage: domain.StagePlan}
	if err := other.CreateFeature(ctx, &f); err != nil {
		t.Fatal(err)
	}
	if err := other.OpenDecision(ctx, f.ID, domain.StagePlan, state.DecisionPayload{ID: "gate-elsewhere", Kind: state.DecisionKindGate, Question: "plan is ready for your decision"}, fixedTime); err != nil {
		t.Fatal(err)
	}

	m = pump(t, m, m.probeForeign())
	if _, ok := m.rowByID(f.ID); !ok {
		t.Fatal("a card another process made never reached the board")
	}
	if _, needs := m.inbox.get(f.ID); !needs {
		t.Error("the gate another process raised is not in the needs-you queue")
	}
}

// The probe reads the disk off the update loop while the loop goes on
// replacing the board's rows, so it takes what it needs of them before
// it leaves the loop. Run under -race, this fails if it reads them later.
func TestTheProbeReadsNoRowsOffTheLoop(t *testing.T) {
	m, _ := chatWorkspace(t, agent.NewFake("ok"))
	done := make(chan struct{})
	probe := m.probeForeign()
	go func() {
		defer close(done)
		probe()
	}()
	for i := 0; i < 100; i++ {
		m.rows = append([]featureRow(nil), m.rows...)
		m.foreignTicks++
	}
	<-done
}
