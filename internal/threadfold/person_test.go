package threadfold

import (
	"encoding/json"
	"testing"

	"github.com/morphis/gummi/internal/state"
)

func payload(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A person the web face names is a person everywhere the thread asks
// "did a human do this?", exactly as the terminal's bare "user" is, and
// the receipts name them.
func TestANamedPersonIsAHuman(t *testing.T) {
	simon := state.PersonActor("Simon")
	if !HumanGateActor(simon) || HumanGateActor(state.ActorAutopilot) {
		t.Error("HumanGateActor misreads a named person or autopilot")
	}
	if got := GateCrosser(state.GatePayload{Actor: simon}); got != "Simon" {
		t.Errorf("GateCrosser = %q, want Simon", got)
	}
	if got := GateCrosser(state.GatePayload{Actor: state.ActorUser}); got != "you" {
		t.Errorf("GateCrosser(user) = %q, want you", got)
	}
	if got := AskAnswerer(state.AskPayload{Actor: simon, By: simon}); got != "Simon" {
		t.Errorf("AskAnswerer = %q, want Simon", got)
	}
	// a person's crossing is not a machine decision line
	gate := state.CardEvent{Kind: state.EventGate, Payload: payload(t, state.GatePayload{From: "plan", To: "implement", Actor: simon})}
	if line := DecisionLine(gate); line != "" {
		t.Errorf("DecisionLine for a person's crossing = %q, want none", line)
	}
}
