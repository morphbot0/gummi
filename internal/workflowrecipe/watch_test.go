package workflowrecipe

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/morphis/gummi/internal/domain"
)

const watchRecipe = `
name: ship-ish
inputs:
  trust: { hint: who, default: "role:maintain" }
steps:
  - id: build
    role: implementer
    prompt: "build {{spec}} {{watch.event}}"
  - id: open
    pr: { act: create, draft: true }
  - id: shepherd
    watch:
      until: green-approved
      trust: "{{inputs.trust}}"
      max: 6
      quiet: 7d
      onClosed: continue
      checks: [ci]
      on:
        checks-failed:
          recipe: fix-until-green
          recipeYAML: |
            name: fix-until-green
            inputs:
              event: { hint: e, default: "" }
            steps:
              - id: fix
                role: implementer
                prompt: "fix {{inputs.event}}"
          max: 2
        changes-requested: { goto: build }
        comment:
          - id: answer
            role: implementer
            prompt: "answer {{event}}"
`

func TestParseWatchAndPRSteps(t *testing.T) {
	r, err := Parse([]byte(watchRecipe))
	if err != nil {
		t.Fatal(err)
	}
	if problems, warnings := Check(r); len(problems)+len(warnings) > 0 {
		t.Fatalf("problems %q warnings %q", problems, warnings)
	}
	open := r.Steps[1].Step
	if open.PR == nil || open.PR.Act != "create" || !open.PR.Draft || !open.Agentless() || open.Kind() != "pr" {
		t.Errorf("pr step = %+v", open)
	}
	w := r.Steps[2].Step.Watch
	if w == nil || w.Until != domain.UntilGreenApproved || w.Max != 6 || w.Quiet != 7*24*time.Hour ||
		w.OnClosed != domain.OnClosedContinue || w.Trust != "{{inputs.trust}}" || !reflect.DeepEqual(w.Checks, []string{"ci"}) {
		t.Errorf("watch = %+v", w)
	}
	if h := w.On[domain.EventChecksFailed]; h.Form() != "recipe" || h.Recipe != "fix-until-green" || len(h.Entries) != 1 || h.Max != 2 {
		t.Errorf("checks-failed = %+v", h)
	}
	if h := w.On[domain.EventChangesRequested]; h.Form() != "goto" || h.Goto != "build" {
		t.Errorf("changes-requested = %+v", h)
	}
	if h := w.On[domain.EventComment]; h.Form() != "steps" || len(h.Steps) != 1 || h.Steps[0].ID != "answer" {
		t.Errorf("comment = %+v", h)
	}
	if got := r.PassEntries("shepherd", domain.EventChangesRequested); len(got) != 1 || got[0].ID() != "build" {
		t.Errorf("goto pass skips pr and the watch: %v", got)
	}
	data, err := Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(data)
	if err != nil {
		t.Fatalf("%v\n%s", err, data)
	}
	if !reflect.DeepEqual(r, back) {
		t.Errorf("round trip differs:\n%s", data)
	}
}

func TestCheckRefusesBadWatch(t *testing.T) {
	const head = "name: x\ninputs:\n  trust: { hint: t, default: \"\" }\nsteps:\n  - id: build\n    role: implementer\n    prompt: p\n"
	watch := func(body string) string {
		return head + "  - id: w\n    watch:\n" + body
	}
	const okOn = "      on:\n        comment: { goto: build }\n"
	cases := []struct{ name, yaml, want string }{
		{"second watch", watch(okOn) + "  - id: w2\n    watch:\n" + okOn, "one watch step"},
		{"watch in parallel", head + "  - id: p\n    parallel:\n      join: all\n      steps:\n        - id: w\n          watch:\n            on: {comment: {goto: build}}\n", "never inside a parallel"},
		{"watch in repeat", head + "  - id: r\n    repeat:\n      max: 2\n      until: checks\n      steps:\n        - id: w\n          watch:\n            on: {comment: {goto: build}}\n", "never inside a repeat"},
		{"pr in parallel", head + "  - id: p\n    parallel:\n      join: all\n      steps:\n        - id: o\n          pr: {act: push}\n", "never inside a parallel"},
		{"unknown pr act", head + "  - id: o\n    pr: {act: merge}\n", "is not push, create, update or ready"},
		{"draft on push", head + "  - id: o\n    pr: {act: push, draft: true}\n", "draft belongs to pr act create"},
		{"unknown until", watch("      until: never\n" + okOn), "is not merged or green-approved"},
		{"unknown onClosed", watch("      onClosed: shrug\n" + okOn), "is not abort or continue"},
		{"no handlers", watch("      until: merged\n"), "names at least one event"},
		{"unknown event", watch("      on:\n        review: { goto: build }\n"), "is not base-moved"},
		{"zero forms", watch("      on:\n        comment: { max: 1 }\n"), "names steps, a recipe or a goto"},
		{"two forms", watch("      on:\n        comment: { goto: build, recipe: fix-until-green }\n"), "one of steps, a recipe or a goto"},
		{"goto unknown", watch("      on:\n        comment: { goto: nope }\n"), "names no earlier top-level step"},
		{"goto the watch", watch("      on:\n        comment: { goto: w }\n"), "names no earlier top-level step"},
		{"recipe holds a watch", watch("      on:\n        comment:\n          recipe: x\n          recipeYAML: |\n            name: x\n            steps:\n              - id: w\n                watch:\n                  on: {comment: {goto: a}}\n"), "holds a watch step of its own"},
		{"recipe holds a pr", watch("      on:\n        comment:\n          recipe: x\n          recipeYAML: |\n            name: x\n            steps:\n              - id: o\n                pr: {act: push}\n"), "holds a pr step of its own"},
		{"bad trust", watch("      trust: \"alice\"\n" + okOn), "trust: trust entry"},
		{"bad trust default", strings.Replace(watch("      trust: \"{{inputs.trust}}\"\n"+okOn), `default: ""`, `default: "role:read"`, 1), "trust input trust's default"},
		{"templated trust", watch("      trust: \"a {{inputs.trust}}\"\n" + okOn), "a literal or one {{inputs"},
		{"event outside a handler", strings.Replace(head, "prompt: p", "prompt: '{{event}}'", 1) + "  - id: w\n    watch:\n" + okOn, "belongs to a watch handler"},
		{"handler holds a pr", watch("      on:\n        comment:\n          - id: o\n            pr: {act: push}\n"), "never publishes"},
		{"empty check", watch("      checks: ['']\n" + okOn), "names a check"},
		{"watch.event after the watch", watch("      on:\n        comment:\n          - id: a\n            role: implementer\n            prompt: '{{watch.event}}'\n"), "belongs to a step before"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Parse([]byte(tc.yaml))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			problems, _ := Check(r)
			if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
				t.Errorf("problems %q, want one with %q", problems, tc.want)
			}
		})
	}
}

func TestBundledShipRecipe(t *testing.T) {
	r := mustLoad(t, "ship")
	if problems, warnings := Check(r); len(problems)+len(warnings) > 0 {
		t.Fatalf("problems %q warnings %q", problems, warnings)
	}
	w, ok := r.WatchStep()
	if !ok {
		t.Fatal("no watch")
	}
	if g := w.Watch.On[domain.EventChangesRequested]; g.Goto != "" {
		var target domain.Step
		Walk(r.Steps, func(s domain.Step, _ []domain.Entry) {
			if s.ID == g.Goto {
				target = s
			}
		})
		if !strings.Contains(target.Prompt, "{{watch.event}}") {
			t.Errorf("goto %s never shows the reviewer's text: prompt %q", g.Goto, target.Prompt)
		}
	}
	for _, h := range w.Watch.On {
		if h.Recipe == "" {
			continue
		}
		child := mustLoad(t, h.Recipe)
		if p, wn := Check(child); len(p)+len(wn) > 0 {
			t.Errorf("%s: %q %q", h.Recipe, p, wn)
		}
		Walk(child.Steps, func(s domain.Step, _ []domain.Entry) {
			if s.Watch != nil || s.PR != nil {
				t.Errorf("%s holds a %s step", h.Recipe, s.Kind())
			}
		})
	}
}
