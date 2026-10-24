package webapi

import (
	"testing"

	"github.com/charmbracelet/x/exp/golden"
)

func TestDispatchShape(t *testing.T) {
	golden.RequireEqual(t, marshal(t, Item{
		Key: "dispatch:FF-7", Seq: 12, T: ItemDispatch, Time: at, Stage: "open",
		Dispatch: &DispatchBlock{
			Card: "FF-7", Title: "Bump lib", Repo: "target", State: "run 2 running", Run: 2, Done: 1, Of: 3,
			Step: "build/1/implement", Spent: 32, Envelope: 400, Last: "tests pass", Rev: "00ab",
		},
	}))
}
