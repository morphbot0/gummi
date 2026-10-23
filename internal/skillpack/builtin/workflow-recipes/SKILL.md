---
name: workflow-recipes
description: "Use when no bundled gummi workflow recipe fits and you write one inline as workflow_run's recipe_yaml, or when a planner step revises a run's plan with a gummi-workflow block: the YAML grammar, roles, templates, loops, joins and worked examples."
scope: general
source: gummi
---

# Writing a gummi workflow recipe

Start from a bundled recipe, not a blank page: `references/` holds
`deliver.yaml`, `fix-until-green.yaml` and `investigate.yaml` as gummi
ships them, and `workflow_recipes` with a `name` shows any recipe,
the workspace's own included. Pass yours to `workflow_run` as
`recipe_yaml`. A recipe that cannot run comes back as a list of
problems, with nothing proposed: fix them and call again.

## The shape

```yaml
name: test-first
description: Write the failing tests, then build until they pass
tier: standard              # every step's default: cheap, standard or strong
inputs: {}                  # name: hint; filled by workflow_run's inputs
steps:
  - id: tests
    role: tester
    prompt: |
      Write the tests the spec's done-when items name. They must fail
      now. Do not implement anything.

      {{spec}}
  - id: build
    repeat:
      max: 4
      until: checks
      onMax: pause
      steps:
        - id: implement
          role: implementer
          prompt: |
            Make the tests from the step below pass without changing them.

            {{tests.output}}

            {{spec}}
  - id: validate
    role: reviewer
    prompt: |
      Read {{run.diff}} against the spec. End with one line per
      done-when item: MET: DW-n or UNMET: DW-n <why>.

      {{spec}}
```

`steps` run in order. Each entry is exactly one of:

- **a step**: `id`, `role`, `prompt`; optional `tier`, `artifacts`
  (name → plain file name in the run's directory) and `captureOutput`
  (false keeps its final message from later steps).
- **`parallel`**: `id`, `join`, and `steps` of plain steps side by side
  in the same tree.
- **`repeat`**: `id`, `max`, one `until`, `onMax`, and `steps` (which may
  hold a `parallel`).

## Roles

| role | does | reads or writes |
|---|---|---|
| `scout` | reads, reproduces, maps | reads |
| `planner` | plans what follows; may rewrite the remaining steps | writes |
| `contractor` | design, interfaces, stubs | writes |
| `implementer` | builds | writes |
| `tester` | writes and extends tests | writes |
| `scribe` | docs and reports | writes |
| `reviewer` | reads the work against the spec | reads |

Every step has a full shell; a reader is told to change nothing, and
one that did is flagged in the report. gummi gives each step its role's
preamble: do not repeat it in the prompt. `tier` picks the model from
the profile (`step-<tier>`): `strong` where judgment matters most — a
deciding reviewer — `cheap` for mechanical work.

## Templates

| template | is |
|---|---|
| `{{spec}}` | the frozen spec's text — put it in every prompt that needs the goal |
| `{{inputs.<name>}}` | an input given at `workflow_run` |
| `{{artifacts.<name>}}` | an artifact's absolute path in the run's directory |
| `{{previous.output}}` | the step before's final message |
| `{{<id>.output}}` | an earlier step's; in a `repeat`, its latest round |
| `{{run.diff}}` | a file: the diff from the run's start to now |

A step sees only its filled prompt. Name every output and artifact it
needs; it has no other way to know what came before.

## Lines a step's output may carry

- `FAILED: <why>` as its last line fails the step; outside a
  `parallel`, that ends the run partial.
- `DECISION: …` and `OPEN: …` go into the run's report.
- `MET: DW-n` / `UNMET: DW-n <why>`, in the **run's last step**, judge
  the items without a check. End with a reviewer that writes them, or
  every `judge: true` item stays unjudged.

## Loops: `until` and `onMax`

| `until` | ends the loop when |
|---|---|
| `checks` | every done-when check passes in the tree |
| `{verdict: APPROVED, in: review}` | the artifact `review`'s JSON `verdict` is the word; a step in the loop must declare it |
| `{contains: LGTM}` | the loop's last step's output contains the text |

`onMax`: `pause` (the default) asks the person; `continue` goes on;
`abort` ends the run partial. A loop gummi cannot judge — the verdict
artifact was never written, a check ran no tests — always asks.

Give a verdict step an artifact and the exact JSON to write: a missing
file is captured from the output's last JSON object, but only if there
is one. Tell the implementer to read the last review first:
`If {{artifacts.review}} exists, address its findings first.`

## Joins: `parallel`

| `join` | the block |
|---|---|
| `all` | fails when one branch fails, stopping the rest |
| `allSettled` | waits for all; fails only if all fail |
| `any` | settles on the first success, stopping the rest |

Put readers side by side (two reviewers on different tiers). Two
writers in one tree overwrite each other: gummi warns, and you should
split them into steps in order instead.

## Other shapes

A light review loop: implement, then one reviewer, round until it says
so.

```yaml
name: build-and-review
description: Implement, then review until a reviewer passes it
steps:
  - id: loop
    repeat:
      max: 3
      until: { contains: "VERDICT: PASS" }
      steps:
        - id: implement
          role: implementer
          prompt: |
            Implement the spec below. If there is a review below, fix
            its findings first.

            {{spec}}
        - id: review
          role: reviewer
          prompt: |
            Review {{run.diff}} against the spec; run its checks. List
            findings, then end with VERDICT: PASS or VERDICT: CHANGES.

            {{spec}}
  - id: validate
    role: reviewer
    prompt: |
      End with MET: DW-n or UNMET: DW-n <why> for each done-when item.

      {{spec}}
```

`contains` matches anywhere in the output: pick a marker the step
writes only when it means it, and ask for it on the last line.

A planner who decides the steps: a `planner` step that ends its output
with a fenced `gummi-workflow` block of entries replaces the run's
remaining steps with them, once they pass the same checks and the
person allows the revised plan.

````yaml
name: planned
description: Scout, then let a planner lay out the build
steps:
  - id: scout
    role: scout
    prompt: |
      Map what the spec below touches: files, tests, risks.

      {{spec}}
  - id: plan
    role: planner
    prompt: |
      Plan the build from the map below. End with a gummi-workflow
      block listing the steps, like:

      ```gummi-workflow
      - id: implement
        role: implementer
        prompt: "…"
      ```

      {{scout.output}}

      {{spec}}
````

## Before you propose

- Every id is unique across the recipe, nested ones included.
- Every `{{…}}` names an input, an artifact some step declares, or an
  earlier step.
- Each loop has a `max` and one `until`; an `until` verdict's artifact is
  declared inside the loop.
- The last step judges the `judge: true` items.
- If it worked, ask the person to keep it with **Save as recipe…** on
  the run's block.
