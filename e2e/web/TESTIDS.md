# gummi web: test ids

Every region and control a test targets carries `data-testid` (Playwright's
`page.getByTestId`). Ids with `<…>` are filled per item. The page builds them
in `internal/web/assets/*.js`; keep this list in step when adding one.

## Shell

| id | what |
|---|---|
| `boot` | the "Opening the board…" placeholder |
| `app` | the board (hidden while pairing) |
| `topbar` | the header bar |
| `workspace`, `ws-repo`, `ws-head` | repo and head branch in the header |
| `pill-needs` | "N need you" — click opens the next card that needs you (`n`) |
| `pill-running`, `pill-today` | running count, today's spend |
| `conn` | connection pill; `data-state` = `connecting` \| `live` \| `reconnecting` (a dot alone at ≤ 1100px and on a phone, its words in its label) |
| `presence`, `viewer` | who is viewing; one `viewer` per person |
| `btn-palette`, `btn-keys`, `btn-theme` | ⌘K palette, `?` sheet, theme toggle (`data-theme` = current) |
| `rail-toggle` | compact/full rail (`[`), `aria-pressed` = full |
| `toasts`, `toast` | the toast stack and each toast |
| `work-toast` | what the board is waiting on GitHub for on a card (a PR's checks or threads being read, a publish another face started): it stays, with a clock, until the work ends |
| `toast-show`, `toast-cmds`, `toast-copy`, `toast-close` | on a toast that carries commands to run (a stack replay's pushes): unfold the commands, the commands themselves, copy them, dismiss it (it stays a minute, and while pointed at or focused) |

## Pairing

`pair`, `pair-form`, `pair-name` (`pair-name-hint` under it), `pair-code`, `pair-submit`, `pair-error`
(wrong code, with "N tries left"), `pair-note`, `pair-new` (print a new code),
`pair-refused` (the form shown again after a request was rejected or lapsed).

A browser paired while another has the board, with a code other than the one
printed at start, waits to be let in: `pending` (the waiting screen),
`pending-text`, `pending-expires` (m:ss left), `pending-error`,
`pending-cancel` (withdraw the request).

On a page at the board: `approvals-slot` (always present, hidden when nobody
waits), `approvals` (the request banner), `approval-<deviceId>` (one request),
and inside it `approval-person`, `approval-device`, `approval-source`,
`approval-via`, `approval-time`, `approval-ua`, `approval-approve`,
`approval-reject`; `approvals-live` (screen-reader line that says a new
request once).

## Rail

| id | what |
|---|---|
| `rail` | the rail landmark |
| `rail-filter` | filter box |
| `rail-kinds`, `rail-kind-<TAG>` | kind chips (`all`, `FD`, `BG`, `RS`, `FF`, `GL`, `AG` for agents) |
| `rail-repos`, `rail-repo-<name>` | repo chips (`all`, then each repo; `default` for the default repo); hidden unless the board spans more than one repo |
| `rail-row-repo-<ID>` | a card's repo chip; shown only when the board spans repos and the card is not in the default repo |
| `rail-cards` | the scrolling list |
| `rail-group-<status>` | `needs`, `running`, `paused`, `idle`, `todo`, `done` |
| `rail-row-<ID>` | a card row; `data-status`, `aria-current="true"` when open. It shows the card's number (`#12`), never its id |
| `rail-row-type-<ID>` | what kind of card the row is, in a word (Feature, Bug, Research, Goal, Session, Agent) |
| `rail-more-done`, `rail-empty` | "Show all N" landed ("+N" on the compact rail); no cards / no match |
| `rail-filtered` | compact rail only: a filter still applies; opens the full rail |
| `rail-foot`, `rail-new-session`, `rail-new-agent`, `rail-new`, `rail-fleet`, `rail-more` | foot buttons (`rail-new-session` opens a session draft, `rail-new-agent`, behind the button's arrow, the same draft as an agent's, `rail-new` the new-card form) |
| `rail-group-agents` | the agents' own group, above the status groups |
| `rail-more-menu`, `menu-stacks`, `menu-schedules`, `menu-skills`, `menu-profiles`, `menu-doctor`, `menu-push`, `menu-unpair` | the More menu |

## Notifications

`push-dialog`, `push-body`, `push-state` (`data-state` = `on` | `off` |
`denied` | `unsupported` | `insecure` | `unavailable` | `unknown`),
`push-on`, `push-off`, `push-error`.

## Card head

`card-head`, `card-kind` (the type word; `data-kind` = `FD` | `BG` | `RS` | `GL` | `FF` | `AG`),
`card-id` (shows the card's number, `#12`; `data-id` = its id — read the id from there), `card-title`, `card-stages`,
`stage-<stage>` (a past stage jumps to it in the thread), `card-branch`, `card-scratch` (a research card: scratch tree, no branch), `card-home` (an agent: its home's path),
`card-spend`, `card-context` (context-window meter, shown once the running
session reports a limit), `card-error`, `toggle-panel` (`]`), `card-actions` (menu),
`card-actions-menu`, `action-<id>` (menu item), `action-btn-<id>` (pause/resume
shown as a button), `action-dialog`, `action-question` (what it asks, or the
server's question (a 202) — verbatim, line breaks kept; an action that asks a
yes is sent bare first and never pre-confirmed, and its yes is the token the
question came with), `action-input` (the field its `needs` asks for,
prefilled with its default), `action-hint` (under a landing's or a squash's
message: where it came from and what it becomes, or — with a spinner, the box read-only and
`action-confirm` saying "Drafting…" — that gummi is drafting it), `action-error` (the board's refusal, in the
dialog), `action-confirm`, `action-cancel`, `land-method` (the landing method select
above a landing's message, in the menu's dialog and in the verify answer's
landing dialog, where a card may keep its commits; its options are `squash`
and `merge`. Choosing `merge` hides `action-input` and `action-hint`),
`keep-branch` (the "Keep the worktree and branch" checkbox a closing carries: on
a landing, in the menu's dialog and the verify answer's landing dialog, and on a
hand-off's confirmation — `action-dialog` or `decision-confirm` — only where the
branch would be removed).

## Thread

| id | what |
|---|---|
| `conversation` | the centre landmark |
| `thread` | the scroller |
| `thread-items` | the polite live region holding items |
| `stage-group-<stage>` | a stage segment (`<details>`; `open` when unfolded; the current one has class `cur`) |
| `thread-item` | an item without a more specific id; `data-key`, `data-type` on every item |
| `receipt`, `verify` (`verify-avatar` its avatar), `check-<name>`, `activity` (a run of tool calls, open while it is being added to and folded after; `activity-last` the step its folded line ends on), `thread-decision`, `thread-consult` (a consult question or answer, where it was asked) | item kinds |
| `verify-no-checks` | a verify item's head when gummi had no gummi-checks to run (never "all passed") |
| `thread-loading`, `thread-empty`, `thread-unavailable`, `thread-error` | empty states |
| `thread-live` | the live block's container |
| `live`, `live-streaming` (its `live-interrupted` mark when the session stopped mid-message), `live-elsewhere`, `live-error`, `live-consult`, `live-consult-head`, `live-consult-notice` (the consult runs on a backend that cannot confine it), `live-freeform`, `live-freeform-head`, `live-stage-earlier` / `live-freeform-earlier` (shows the turns a long conversation's block left out) | live block parts |
| `thinking` | the agent's reasoning, folded |

## Decision and composer

| id | what |
|---|---|
| `decision-slot`, `decision` | the pinned decision (`data-kind`, `data-ref`) |
| `decision-question`, `decision-against`, `decision-more`, `decision-jump` | its parts |
| `decision-option-<optionId>` | a numbered answer (`aria-pressed` = highlighted; on a multi-pick question, picked) |
| `decision-all` | a phone's "More answers": the pinned decision shows its first answer alone until pressed (`pairedPage` presses it for a spec) |
| `decision-note` and its kinds `decision-answered`, `decision-moved`, `decision-needs`, `decision-busy`, `decision-newcard`, `decision-error`, `decision-pick` | what happened to the last answer (a 409 said who got there first or that the card moved; a 202 question what it still needs; enter with nothing highlighted) |
| `decision-confirm`, `decision-confirm-question`, `decision-confirm-yes`, `decision-confirm-no` | a confirmation an answer's flow asked for (a 202 `confirm` question), drawn beside the decision (never inside its capped box) and focused when it appears; the question is the server's, verbatim (line breaks kept); yes sends the answer again with the token it came with as `confirm` |
| `decision-carry` | "+ N diff comments" on an answer that takes them |
| `nextup-slot`, `nextup` | "X also needs you" chip after an answer (on a phone's document tab, in the docked bar) |
| `dock`, `composer`, `composer-input`, `composer-says`, `composer-send` | composer; `composer-says` is the enter line (the server's reading of the line, or the decision's answer) |
| `composer-model`, `model-picker-btn` | a session's model beside Send (a draft's, or an open session's); absent on a card of the retired kinds |
| `model-picker`, `model-search`, `model-<agent>-<model>`, `model-typed-<agent>`, `model-recent-<agent>-<model>` | the picker: search or type an id; a suggested model (`default` for the agent's own), a typed id, a recent pair |
| `composer-draft`, `draft-repo`, `draft-base`, `draft-budget`, `draft-budget-pop`, `draft-budget-<n>`, `draft-budget-input`, `draft-budget-set`, `draft-budget-error` | a session draft's row above the line: repository, base, budget (`0` is uncapped) |
| `draft-hero`, `draft-starter`, `draft-cancel` | a session draft's empty conversation, its starter lines, and leaving the draft |
| `composer-note` | a line about the last send: handed back mid-turn, or refused |

## Panel

| id | what |
|---|---|
| `panel` | the right landmark |
| `panel-tabs`, `tab-memory`, `tab-spec`, `tab-diff`, `tab-log`, `tab-pr`, `tab-stats`, `tab-step` | tabs (`aria-selected`; `tab-step` only while a workflow step is open) |
| `tab-terminal` | the Terminal tab (a board served with `--terminal`, an open session or a card with a worktree); a phone reaches it from the card's menu, `action-terminal`, and shows the tab only while it is open |
| `terminal`, `terminal-screen`, `terminal-note`, `terminal-again`, `terminal-off` | the shell (`data-state` = `open` \| `closed`), xterm's screen, the note over it (opening, reconnecting, exited), its retry / new-shell button, and the pane on a card with no worktree |
| `terminal-end` | end the shell (a second press confirms) |
| `terminal-keys`, `terminal-key-<id>` | a touch screen's key bar and its keys: `esc`, `tab`, `ctrl`, `alt` (`aria-pressed` while held), `left`, `down`, `up`, `right`, `home`, `end`, `pgup`, `pgdn`, `dash`, `slash`, `pipe`, `tilde` |
| `panel-close` | hide the panel |
| `panel-pane` | the tab body; `data-tab` = open tab |
| `panel-loading`, `panel-unavailable`, `panel-error` | tab states (unavailable = the route answers 501) |
| `panel-draft` | the pane under a new session's draft (no tabs: nothing has documents yet) |
| `resizer` | panel resizer (drag, ←/→, double-click resets) |

Spec: `spec`, `spec-toc` (the Sections button), `spec-toc-menu`, `spec-toc-<i>`, `spec-doc`, `spec-src`, `spec-title`,
`spec-section-<i>`, `spec-comment-<i>`, `spec-note-draft`, `spec-note-input`,
`spec-note-save`, `spec-note`, `spec-prompt` (gummi's own `%%` prompts),
`spec-note-hint` (a note is one line: line breaks become spaces),
`spec-closed` (a done or landed card's spec takes no more notes, and offers
no Comment, Resolve or Request changes),
`spec-note-resolve`, `spec-note-resolution` (a resolution marker, folded onto
the note above it: "resolved by" when it closes it, "answered by" when an
agent answered a person's note that only a person can close), `spec-checks`, `spec-check-<name>`, `spec-pending` (how many notes hold the
gate), `spec-request-changes` (sends them to the stage that owns them: the TUI's R), `spec-none`.

A session's specs (DESIGN §25.3): the tab (`tab-spec`) is Specs. With a spec
or more, `specs` holds `specs-list` — one `specs-item-<n>` per spec
(`aria-current` on the one shown) with its state `specs-state-<n>` (draft,
running, met, partial) — then `specs-new` (+ New spec), which opens
`specs-new-form`, `specs-new-title`, `specs-new-start`; the spec shown is
drawn beneath with the ids below. `spec-start-session` (start the first one
from the template, on `spec-none`), `spec-session` (its bar), `spec-rev`,
`spec-ready` (its done-when items), `spec-propose-run` (asks the session
for a run on the spec: sends `/workflow on spec n`), `spec-edit`, `spec-editor`, `spec-save`,
`spec-cancel`, `spec-stale` (the spec changed while it was being edited),
`spec-revisions`, `spec-revision-<n>`; `spec-runs`, the spec's workflow runs
newest first, one `spec-run-<n>` each (its glyph, recipe, state,
`spec-run-met`, spend and when it ended), which jumps to the run's block in
the thread, unfolded whole.

A session's workflow runs (DESIGN §25): in the thread, a turn a run sent (its
report): `thread-workflow`.

A run's block, where its run was proposed — among the session's turns or the
thread's settled items (§25.9, `workflow.js`): `run-<n>`
(`data-state`, `data-rev`; `data-folded="yes"` while an ended run is folded
to one line: `run-line`, which unfolds it, with `run-line-text`), its
`run-head` with `run-title`, `run-state`,
`run-spent`, `run-elapsed` (the time it worked, pauses left out),
`run-reason`, `run-phase` (what gummi does between its steps),
`run-always` with `run-askagain` (on the newest run of a session whose runs
are always allowed) and the acts `run-pause`, `run-resume`, `run-stop` and
`run-withdraw` (a queued run), each of the last two opening
`run-<act>-dialog` (`run-<act>-cancel`, `run-<act>-confirm`), and on an
ended run `run-fold` (▾, folds it to its line); `run-strip` with one `run-strip-<id>`
per top-level entry of the recipe; `run-pending` (the run waits on the pinned
decision); `run-baseline` (while it is proposed or queued, or open before its
first step: the done-when checks run before the run — `baseline-running`
while they run, else one `baseline-<DW-n>` per checked item, `data-verdict`,
amber when it proves nothing); `run-rows` with one `run-row-<key>` per step or folded iteration
(`data-card`, `data-state`; `sel` when open beside the thread), its
`run-row-line`, `run-row-flag` (a reading step that changed files),
`run-row-took` (how long an ended step took) and `run-open`
(`data-state="paused"` on a step stopped where it was while its run is
paused); a step's question `run-question` under its row,
with `run-question-option-<i>` (the recommended first) — or, on one that
takes several answers, `run-question-pick-<i>` checkboxes and
`run-question-send-picked` — `run-question-reply` and `run-question-send`;
`run-artifacts` with one `run-artifact-<name>` per artifact the recipe
declares, which opens `artifact-dialog` (`artifact-text`, `artifact-close`)
once written; an ended run's `run-results` (`run-result-<DW-n>`, with
`run-result-change` saying it before and after: `failed before the run, now passes`),
`run-steps` (its rows, folded), `run-commits`, `run-report`; and `run-files`
with `run-diff`, which opens `run-diff-dialog` (`run-diff-body` with
`step-diff-file`s, `run-diff-close`), `run-retry` (Retry failed steps, on
the newest run that ended partial or stopped), `run-revert`, which opens `revert-dialog` (`revert-paths`, `revert-note`,
`revert-cancel`, `revert-confirm`), and, on a run whose recipe the session
wrote inline, `run-save` (Save as recipe…, from the proposal on), which opens
`save-dialog` (`save-name`, `save-error`, `save-cancel`, `save-confirm`), or
`run-saved` once it is saved.

An open run's live strip above the composer (§25.9, `workflow.js`), in
`run-live-slot`: `run-live` (`data-run`, `data-mood`: running, asking,
deciding, budget or paused) with `run-live-name`, `run-live-strip` (the
recipe's entries; dropped first on a narrow page), `run-live-now` (the step
at work and its action, a step's question, or what a paused strip says),
`run-live-money`, and its acts `run-live-jump` (scrolls the thread to the
block, which flashes), `run-live-open` (the step at work; with several,
`Open… ▾` opens `run-live-pick` with one `run-live-pick-<id>` each),
`run-live-answer` (opens the asking step on its question),
`run-live-topup` (opens `topup-dialog`: `topup-amount` in dollars,
`topup-error`, `topup-cancel`, `topup-confirm`) and `run-live-resume`. At the top of the thread,
`run-above` says the open run's block is scrolled out of view above, and
jumps to it.

A step opened beside the thread (§25.10): the `tab-step` surface holds
`step-panel` with `step-head` (`step-crumb`, whose first entry `step-close`
goes back to the session, `step-title`, `step-live` or `step-state`,
`step-spent`), `step-revert` (Revert to before this step…, which opens
`revert-dialog` for the step), the tabs `step-tab-transcript`, `step-tab-diff`,
`step-tab-brief` (a step has no terminal tab: its tree is the session's),
their bodies `step-transcript` (`step-transcript-empty`), `step-diff`
(`step-diff-file`, `step-diff-none`), `step-brief` (`step-artifacts`), and
the composer `step-composer` (`step-input`, `step-send`). A step that asks
carries its question in the panel as `run-question`, as under its row.

A run's proposal on its pinned decision (§25.5): `run-proposal` with
`proposal-tree` (the tree it runs in, and whether it holds uncommitted work),
`proposal-spec`, `proposal-step-<id>`, on a revised plan `proposal-replaces`
with one `proposal-replaced-<id>` per step it drops, `proposal-checks`, `proposal-warnings`,
`proposal-money`, `proposal-baseline` (the checks run in the tree before the
run, as `run-baseline` has them); under its answers, `decision-feedback` and
`decision-feedback-send`.

Memory: `memory`, `memory-src` (where the files live), `memory-global`,
`memory-plan`, `memory-dead-ends` (a document's title and its rendered
markdown; nothing written yet reads as a placeholder), `memory-none`
(why a card has none — the answer for a card of the retired kinds).

Request changes that sends a card back to an earlier stage asks first:
`changes-confirm` (the dialog), `changes-question`, `changes-go`,
`changes-cancel`, `changes-error`.

Diff: `diff-head`, `diff-rev`, `diff-add`, `diff-del` (totals of the files
listed: all, or only those changed since), `diff-since`, `diff-since-all`, `diff-since-new`,
`diff-fresh` (branch moved banner), `diff-fresh-show`, `diff-pending`,
`diff-request-changes` (sends the open comments to the implementer, or with an
earlier stage's spec note back to it: the TUI's R),
`diff-files`, `diff-file-<i>`, `diff-filebox-<i>`, `diff-viewed-<i>`,
`diff-line-<idx>` (idx = raw diff line index, the annotation coordinate; click
`.o`/`.n` to comment), `diff-unfold-<i>` ("Show N lines" on a file folded
because it is large, or the diff already drew many lines; a file with an open
comment is never folded, and its lines have no `diff-line-*` until unfolded),
`diff-orphans` (a file's comments whose line is gone; each says so in
`annotation-gone-<id>`), `diff-other` (comments on files the diff does not
show — a pulled PR thread on an untouched file, a deleted file's — above the
files, and under `diff-none` too; `annotation-where-<id>` names the file),
`annotation-<id>`, `annotation-edit-<id>` (not on a pulled PR thread),
`annotation-edit-input-<id>`, `annotation-edit-save-<id>`,
`annotation-resolve-<id>`, `annotation-delete-<id>`, `annotation-draft`,
`annotation-input`, `annotation-save`, `annotation-adrift` (an unsent comment
whose line the moved diff no longer has), `diff-none`. A done or landed card's
line numbers open no comment box.

PR: `pr`, `pr-state`, `pr-open` (open threads), `pr-fetched`, `pr-refresh`,
`pr-pull` (off, as `pr-checks-send` is, while the board is reading the PR for the card), `pr-thread-<i>`, `pr-thread-show-<i>` (opens the diff at the
thread's file and line), `pr-push` (what to push and when, or why there is
nothing to push), `pr-push-cmd`, `pr-push-copy`, `pr-none`, `pr-link` (link a pull
request, when the card's menu offers it).

Publishing waits: `publish-reading` (the dialog while GitHub answers for its
facts), `publish-steps` and `publish-step-<id>` (the act's steps once
confirmed; `data-state` is `todo`, `run`, `done` or `fail`; ids are `check`,
`todraft`, `push`, `create`, `edit`, `ready`, `draft`).

Log: `log`, `log-head`, `log-why` (why the history is read-only),
`log-none`, `log-commit-<i>` (oldest first), `log-show-<i>` (a commit's
changes, in `log-patch`; a large file there folds behind `log-unfold-<i>`),
`log-reword-<i>`, `log-editor-<i>`,
`log-editor-save-<i>`, `log-squash-<i>` (`aria-pressed`), `log-plan` (the
draft's bar), `log-plan-line` (what the dry run says), `log-confirm`
(rewriting pushed commits), `log-confirm-cmd`, `log-confirm-copy`,
`log-reset`, `log-apply`, `log-push`, `log-push-cmd` (the force push a
rewrite of pushed commits leaves), `log-push-copy`.

Stats: `stats`, `stats-spent`, `stats-left` (a session's), `stats-passes`,
`stats-rework`, `stats-models` (a session's), `stats-table`, `stats-none`,
`stats-bars` (`-stage`, `-role`, `-model`), `stats-redo`, `stats-clock`
(`-agent`, `-you`, `-idle`, its segments), `stats-hands`, `stats-tools`,
`stats-no-tools` (the backend reports no tool calls), `stats-checks`,
`stats-judgment` (`stats-gates`, `stats-asks`), `stats-envelope`.

## Phone (≤ 760px)

`mobile-card` (a card's screen bar: hidden over the cards), `card-back` (back
to the cards), `card-back-needs` (its count of cards that need you),
`tab-thread` (the card's thread, first in its tab row; `aria-selected` on the
shown one), `mdec-card` (the card the docked decision bar is for, over the
cards), `mobile-decision` (docked decision bar), `mdec-toggle`, `mdec-go` (the
first answer as one button, while the bar is folded),
`mdec-option-<optionId>`, `mdec-note`, `mdec-confirm`, `mdec-confirm-question`,
`mdec-confirm-yes`, `mdec-confirm-no` (the bar's own copy of the confirmation).

## Overlays and views

| id | what |
|---|---|
| `scrim` | any overlay's backdrop |
| `palette`, `palette-input`, `palette-results`, `palette-row-<ID>`, `palette-view-<name>` (a view, or `newsession`, `push`) | ⌘K |
| `keys-help` | the `?` sheet |
| `menu` | a popup menu opened with `openMenu` (its `testid` option overrides `menu`, e.g. `rail-more-menu`, `card-actions-menu`) |
| `modal`, `modal-close` | a dialog opened with `openModal` (its `testid` option overrides `modal`) |
| `view-<name>`, `view-<name>-body` | a registered view (`newcard`, `fleet`, `stacks`, `doctor`) |
| `not-available` | body of a view nobody registered yet |
| `unpair-dialog`, `unpair-confirm` | unpairing |

## Stacks (`views/stacks.js`)
`stacks-list`, `stacks-count`, `stacks-new`, `stacks-empty`, `stacks-error`;
new stack form `stack-form`, `stack-form-card`, `stack-form-name`,
`stack-form-error`, `stack-form-cancel`, `stack-form-submit`.
One stack: `stack-<stackId>` (class `focus` when opened on it), `stack-name`,
`stack-restack`, `stack-add`, `stack-rename`, `stack-delete` (empty stacks
only); `stack-add-form`, `stack-add-card`, `stack-add-pos`, `stack-add-submit`;
`stack-rename-form`, `stack-rename-input`, `stack-rename-save`;
`stack-remove-dialog`/`-cancel`/`-confirm`, `stack-delete-dialog`/`-cancel`/`-confirm`.
Chain: `stack-chain`, `stack-base`, `stack-member-<ID>` (`data-pos`, and
`data-stale`, `data-landed`, `data-running`, `data-dirty` when set),
`stack-open-<ID>`, `member-branch`, `member-blocker`, `marker-landed`, `marker-handedoff`,
`marker-stale`, `marker-running`, `marker-dirty`, `marker-notree`,
`marker-needs`, `stack-up-<ID>` (toward the base), `stack-down-<ID>`,
`stack-remove-<ID>`.
Restack answer: `stack-result`, `stack-result-close`, `stack-replayed`,
`stack-conflict`, `stack-waiting`, `stack-push`, `stack-push-line`,
`stack-push-copy`, `stack-push-copy-all`.
Card head: `card-stack` (opens the stacks view on the card's stack).

## Profiles (`views/profiles.js`)
`profiles-count`, `profiles-new`, `profiles-list`, `profiles-empty`,
`profiles-problem` (why the file is not in force), `profiles-notice`,
`profiles-path`. One profile: `profile-<name>`, `profile-<name>-default`
(the badge), `profile-<name>-edit`, `profile-<name>-make-default`,
`profile-<name>-delete`, and its confirm `profile-<name>-confirm`
(`-yes`/`-no`). Form `profile-form`: `profile-form-name`,
`profile-form-sandbox`, `profile-form-<role>` per role (opens the
composer's `model-picker`; `model-blank` leaves the role unset),
`profile-form-default`, `profile-form-error`, `profile-form-error-detail`,
`profile-form-cancel`, `profile-form-save`.

## First-run setup (`views/firstrun.js`)
`view-firstrun` (the view, opened on the start that seeded profiles.yaml),
`firstrun-step` (`n/m`), `firstrun-agent-<name>` (one agent, its radio,
status and hint), `firstrun-strong`, `firstrun-everyday` (the two model
selects), `firstrun-roles` (the confirm's role → agent/model list),
`firstrun-skip`, `firstrun-back`, `firstrun-continue`, `firstrun-save`,
`firstrun-error`.

## Schedules (`views/schedules.js`)
`schedules-list`, `schedules-count`, `schedules-new`, `schedules-empty`,
`schedules-notice`; form `schedule-form` (create and edit — the same one),
`schedule-form-name`, `schedule-form-kind-mint`/`-heartbeat`,
`schedule-form-kind-fixed` (an edit's read-only kind),
`schedule-form-target`, `schedule-form-repo`, `schedule-form-pair` (the
agent and model: opens the composer's `model-picker`, its first row
`model-blank` the profile's default; `data-backend`/`data-model` the pick),
`schedule-form-envelope`, `schedule-form-every`,
`schedule-form-cron`, `schedule-form-tz`, `schedule-form-prompt`,
`schedule-form-preview` (`-cron`, `-fires`, `-error` and
`schedule-form-envelope-hint` inside it), `schedule-form-error`,
`schedule-form-error-detail`, `schedule-form-cancel`, `schedule-form-submit`.
One row: `schedule-<id>` (`data-enabled`), `schedule-<id>-state`,
`schedule-<id>-next`, `schedule-<id>-status`, `schedule-<id>-orphan`,
`schedule-<id>-toggle`, `schedule-<id>-edit`, `schedule-<id>-run`,
`schedule-<id>-rm`,
`schedule-<id>-confirm` (`-yes`/`-no` via the shared strip),
`schedule-<id>-result`.

## Skills (`views/skills.js`)
`skills-config`, `skills-spend`, `skills-next`, `skills-running`,
`skills-published`, `skills-run`, `skills-run-confirm` (`-yes`/`-no` via the
shared strip), `skills-notice`, `skills-error`, `skills-count`,
`skills-empty`, `skills-table`. The curator's settings: `skills-configure`,
`skills-form`, `skills-form-pair` (the agent and model, through the
composer's `model-picker`; `data-backend`/`data-model` the pick),
`skills-form-schedule`, `skills-form-budget`, `skills-form-monthly`,
`skills-form-error`, `skills-form-save`, `skills-form-cancel`,
`skills-form-off`, `skills-off-confirm`. One skill: `skill-<name>` (`data-owner`),
`skill-<name>-owner`, `skill-<name>-standing`, `skill-<name>-pin` /
`skill-<name>-unpin` (curator skills only). Runs: `curator-runs`,
`curator-runs-empty`, one run `curator-run-<id>` (`data-outcome`),
`curator-run-<id>-digest`, `curator-run-<id>-revert` (runs that changed the
pack), `curator-run-<id>-confirm`.

## Tool views
Shared by the views below: `view-error` (a refusal, as the server said it),
`<prefix>-confirm`, `<prefix>-confirm-yes`, `<prefix>-confirm-no` (an inline
confirm strip).
Doctor (`doctor`): `doctor-ready` (`data-ready`), `doctor-rerun`,
`doctor-deep`, `doctor-deep-confirm` (+ `-yes`/`-no`), `doctor-checks`,
`doctor-check-<name>` (`data-status` = `ok` | `warn` | `fail` | `unknown`),
`doctor-loading`.
Fleet stats (`fleet`): `fleet`, `fleet-window` (the window switch),
`fleet-window-24h` / `-7d` / `-30d` / `-all`
(`aria-pressed`), `fleet-span`, `fleet-headline`, `fleet-spent`, `fleet-tokens`, `fleet-busiest`,
`fleet-estimated`, `fleet-alltime`, `fleet-rework`, `fleet-lanes`,
`fleet-clock`, `fleet-clock-agent` / `-you` / `-idle`, `fleet-by-stage`,
`fleet-stage-bars`, `fleet-by-model`, `fleet-model-bars`, `fleet-timeline`,
`fleet-timeline-scroll`, `fleet-legend`, `fleet-lane-<ID>` (marks inside:
`.blk.st-<stage>` a session, `.open` still running; `.wait` / `.wait.open`
on you; `.gate`; `.land`), `fleet-lane-curator` (the skill curator's
lane, above the cards'; `.blk.cur` a run), `fleet-curator-spent`,
`fleet-tip`, `fleet-empty`, `fleet-error`.
New card (`view-newcard`): `newcard-loading`, `newcard-form`,
`newcard-about`, `newcard-title`, `newcard-desc`,
`newcard-profile`, `newcard-envelope`, `newcard-repo`, `newcard-base`
(`newcard-base-hint` under it), `newcard-stack`,
`newcard-error` (a refusal no field owns), `newcard-error-<field>` (beside
its field: `title`, `desc`, `envelope`, `profile`, `repo`,
`base`, `stack`), `newcard-create`, `newcard-cancel`.
