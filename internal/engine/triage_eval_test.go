//go:build triageeval

package engine

// The triage eval: testdata/triage-eval.yaml's requests, each read by the
// real triage prompt on a real model, scored against what it should have
// said. It spends real money and needs the claude CLI signed in, so it is
// behind the triageeval build tag, never in `go test ./...`: run it with
// `make triage-eval`. GUMMI_TRIAGE_EVAL_MODEL picks the model (default
// haiku, the cheap slot triage falls back to); GUMMI_TRIAGE_EVAL_MIN the
// share of cases that must match (default 0.8).

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/morphis/gummi/internal/agent"
	"github.com/morphis/gummi/internal/workflowrecipe"
)

type triageCase struct {
	Want    string `yaml:"want"`
	Request string `yaml:"request"`
}

func TestTriageEval(t *testing.T) {
	raw, err := os.ReadFile("testdata/triage-eval.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var set struct {
		Cases []triageCase `yaml:"cases"`
	}
	if err := yaml.Unmarshal(raw, &set); err != nil {
		t.Fatal(err)
	}
	found, err := workflowrecipe.List(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var recipes []WorkflowRecipe
	for _, f := range found {
		recipes = append(recipes, WorkflowRecipe{Name: f.Name, Description: f.Recipe.Description})
	}
	model := envOr("GUMMI_TRIAGE_EVAL_MODEL", "haiku")
	minShare, err := strconv.ParseFloat(envOr("GUMMI_TRIAGE_EVAL_MIN", "0.8"), 64)
	if err != nil {
		t.Fatal(err)
	}
	ag, err := agent.NewClaudeCode("")
	if err != nil {
		t.Skip(err)
	}
	defer ag.Close()

	type result struct{ got, why, reply string }
	results := make([]result, len(set.Cases))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, c := range set.Cases {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			reply, err := triageOnce(ag, model, t.TempDir(), triagePrompt(c.Request, recipes))
			if err != nil {
				results[i] = result{got: "error", why: err.Error()}
				return
			}
			name, why, ok := parseTriage(reply, recipes)
			if !ok {
				name = "unparsed"
			}
			results[i] = result{got: name, why: why, reply: reply}
		}()
	}
	wg.Wait()

	matched := 0
	confusion := map[string]map[string]int{}
	var b strings.Builder
	for i, c := range set.Cases {
		r := results[i]
		mark := "ok  "
		if r.got == c.Want {
			matched++
		} else {
			mark = "MISS"
		}
		if confusion[c.Want] == nil {
			confusion[c.Want] = map[string]int{}
		}
		confusion[c.Want][r.got]++
		fmt.Fprintf(&b, "%s want %-15s got %-15s %s\n       %s\n", mark, c.Want, r.got, cutRunes(c.Request, 90), r.why)
	}
	b.WriteString("\nwant → got:\n")
	for want, got := range confusion {
		fmt.Fprintf(&b, "  %-15s %v\n", want, got)
	}
	share := float64(matched) / float64(len(set.Cases))
	fmt.Fprintf(&b, "\n%d of %d matched (%.0f%%) on %s\n", matched, len(set.Cases), 100*share, model)
	t.Log("\n" + b.String())
	if share < minShare {
		t.Errorf("triage matched %.0f%% of the eval, under %.0f%%", 100*share, 100*minShare)
	}
}

// triageOnce runs the triage one-shot as FreeformSession.triage does,
// outside any card: a read-only session, the system hint, one prompt.
func triageOnce(ag agent.Agent, model, dir, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), triageTimeout)
	defer cancel()
	s, err := ag.NewSession(ctx, agent.SessionOpts{
		WorkDir: dir, Role: agent.RoleTriage, Model: model, Permission: agent.PermissionAllowAll,
		ReadOnly: true, SystemHints: []string{triageSystemHint},
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = s.Close() }()
	if err := s.Send(ctx, prompt); err != nil {
		return "", err
	}
	var text assistantText
	for {
		select {
		case ev, ok := <-s.Events():
			if !ok {
				return text.String(), nil
			}
			switch ev.Kind {
			case agent.EventTextDelta:
				text.delta(ev.Text)
			case agent.EventMessage:
				text.message(ev.Text)
			case agent.EventIdle, agent.EventBudgetExhausted:
				return text.String(), nil
			case agent.EventError:
				return "", ev.Err
			}
		case <-ctx.Done():
			return "", fmt.Errorf("no answer in %s", triageTimeout.Round(time.Second))
		}
	}
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func cutRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
