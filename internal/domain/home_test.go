package domain

import (
	"strings"
	"testing"
)

// TestAnAgentIsAMainCheckoutCardInNoRepository: an agent's home is the
// checkout it runs in, so the row carries MainCheckout with Home and names
// no repository; each way of saying otherwise is refused.
func TestAnAgentIsAMainCheckoutCardInNoRepository(t *testing.T) {
	f := mainCheckoutCard(1)
	f.Home = true
	if err := f.Validate(); err != nil {
		t.Fatalf("an agent does not validate: %v", err)
	}
	if got := f.CheckoutName(); got != "its home" {
		t.Errorf("CheckoutName = %q, want its home", got)
	}
	for name, breakIt := range map[string]func(*Feature){
		"main-checkout": func(f *Feature) { f.MainCheckout = false },
		"repository":    func(f *Feature) { f.Repo = "api" },
		"parented":      func(f *Feature) { f.ParentID = "FF-9" },
	} {
		g := *f
		breakIt(&g)
		if err := g.Validate(); err == nil || !strings.Contains(err.Error(), "agent") {
			t.Errorf("%s: Validate = %v, want a refusal that names the agent", name, err)
		}
	}
}
