# gummi: first-run setup

Read this once per repository, before the first `gummi run`. Once `gummi
doctor` reports `ready: true` you never need it again.

```
gummi doctor --json
```

It returns a structured checklist — repo, workspace, backend, profile, auth,
envelope, lock. It exits **7** while any check is failing and 0 once the
workspace is ready, in both output shapes, so `gummi doctor --json > d.json;
ec=$?` branches without parsing. 7 sits outside the run/resume exit table, so
a readiness failure can never be mistaken for a drive's typed status.

Repair each item that isn't `ok`:

- **backend** — set gummi's backend to the **same agent that is driving it**:
  give every role a `backend:` in `.gummi/profiles.yaml` (e.g. `backend:
  claude` if you are Claude Code, `backend: opencode` if you are opencode); export
  `GUMMI_AGENT` only to set the fallback default for roles that omit it.
  Matching the backend to yourself keeps the whole pipeline on one vendor's
  auth, and it sidesteps the cross-model trap where the claude backend
  forwards each role's model to the Claude CLI as `--model` and the CLI
  rejects any non-`claude-*` id (a mismatched run dies on the first role
  it reaches).
- **profile** — **do not rely on the auto-seeded default** (its
  implementer/scribe roles are OpenAI ids the claude backend cannot drive).
  Write the `default` profile in `.gummi/profiles.yaml` yourself, and **ask
  the human their model preferences first.** Offer a cost-tiered shape — a
  frontier model for the architect/reviewer roles, a cheaper one for
  implementer/scribe — with every role on a model your backend can drive (for
  the claude backend, every role a `claude-*` id). If the human has no
  preference, seed those tiered defaults. Tier down rather than pointing every
  role at the top frontier model — you don't need the biggest model on the
  scribe.
- **auth** — if a check reports auth is needed, gummi hands you the **exact
  command**. Give it to the **human** to run (e.g. surface it with the `!`
  prefix). You never handle secrets: API keys are referenced by environment
  variable **name**, never written as literal values.
- **envelope** — every run needs a credit envelope. Pass `--envelope N` per
  run, or set `GUMMI_ENVELOPE`. A run refuses to start without one.

Re-run `gummi doctor` until it reports `ready: true`, then proceed.
