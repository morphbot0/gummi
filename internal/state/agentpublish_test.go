package state

import (
	"context"
	"testing"
)

// A session's allowance to publish defaults off and persists across a
// re-open once switched on.
func TestAgentPublishAllowance(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/state.db"
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	f := feat(1, "Publish allowance")
	if err := s.CreateFeature(ctx, f); err != nil {
		t.Fatal(err)
	}
	if on, err := s.AgentPublish(ctx, f.ID); err != nil || on {
		t.Fatalf("default = %v, %v; want off", on, err)
	}
	if err := s.SetAgentPublish(ctx, f.ID, true); err != nil {
		t.Fatal(err)
	}
	// a card write does not flip it
	cur, _ := s.GetFeature(ctx, f.ID)
	if err := s.UpdateFeature(ctx, &cur); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if on, _ := s2.AgentPublish(ctx, f.ID); !on {
		t.Fatal("allowance lost across reopen")
	}
	if err := s2.SetAgentPublish(ctx, f.ID, false); err != nil {
		t.Fatal(err)
	}
	if on, _ := s2.AgentPublish(ctx, f.ID); on {
		t.Fatal("allowance not switched off")
	}
	if err := s2.SetAgentPublish(ctx, "FF-999", true); err == nil {
		t.Fatal("setting a missing card should fail")
	}
}
