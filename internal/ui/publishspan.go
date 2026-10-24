package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/publish"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/ui/theme"
	"github.com/morphis/gummi/internal/webapi"
)

// A session that spans repositories (DESIGN §27.7) opens one pull request
// in each: its own and every attached one not landed, each PR's body
// naming the others (publish.PlanAll and publish.DoAll). It is the menu's
// "open pull requests…" on both faces, confirmed like every publish on
// the facts read, one fingerprint per repository.

const (
	// publishSpanID is the menu entry that opens a span's confirm.
	publishSpanID = "prcreateall"
	// publishSpanAct is what the page's action carries to open the span's
	// dialog rather than one repository's.
	publishSpanAct = "span"
)

// spansUnlanded reports whether the card holds an attached repository that
// has not landed: a span has somewhere besides the card's own to publish.
func spansUnlanded(r featureRow) bool {
	for _, v := range r.Repos {
		if !v.Primary && !v.Landed {
			return true
		}
	}
	return false
}

// spanOnes is each repository of the session a span may open a PR in, its
// own first, each as that repository sees the card. req is the act every
// one of them runs, before its fingerprint is set.
func (p publishDoer) spanOnes(ctx context.Context, req publish.Request) (domain.Feature, []publish.One, error) {
	f := p.f
	if cur, err := p.store.GetFeature(ctx, f.ID); err == nil {
		f = cur
	}
	atts, err := p.store.ListAttachments(ctx, f.ID)
	if err != nil {
		return f, nil, err
	}
	anns, err := p.store.ListDiffAnnotations(ctx, f.ID)
	if err != nil {
		return f, nil, err
	}
	var ones []publish.One
	add := func(repo, label string, view domain.Feature, link publish.Link) error {
		mgr, err := p.pool.ManagerFor(ctx, &view)
		if err != nil {
			return err
		}
		var mine []domain.DiffAnnotation
		for _, a := range anns {
			if a.Repo == view.TreeRepo {
				mine = append(mine, a)
			}
		}
		in := publish.InputFor(ctx, mgr, &view, p.busy, mine)
		// a fork's target is chosen in the one-repository confirm; a span
		// opens where each repository already says, or refuses that one
		in.BaseRepo = ""
		ones = append(ones, publish.One{Repo: repo, Label: label, Branch: view.BranchName(), Tree: mgr, In: in, Req: req, Link: link})
		return nil
	}
	own := f.Repo
	if own == "" {
		own = "workspace"
	}
	if err := add("", own, f, func(ctx context.Context, ref domain.PullRequestRef) error {
		return p.store.SetPullRequest(ctx, f.ID, ref)
	}); err != nil {
		return f, nil, err
	}
	for _, a := range atts {
		if a.Landed() {
			continue
		}
		// a PR opened for an attached repository is shown, not linked: the
		// card's one link is its own repository's
		if err := add(a.Repo, a.Repo, f.AttachedView(a), func(context.Context, domain.PullRequestRef) error { return nil }); err != nil {
			return f, nil, err
		}
	}
	return f, ones, nil
}

// prepareSpan reads every repository's facts and plans a create on each,
// with the words the PRs start from: the session's own repository's.
func (p publishDoer) prepareSpan(ctx context.Context) webapi.PublishSpan {
	var out webapi.PublishSpan
	env := publish.Env{GH: p.gh}
	_, ones, err := p.spanOnes(ctx, publish.Request{Act: publish.ActCreate})
	if err != nil {
		out.Error = publishErr(publish.AsError(err))
		return out
	}
	if fx, perr := publish.Resolve(ctx, env, ones[0].Tree, ones[0].In); perr == nil {
		out.Title, out.Body = publish.Text(ctx, env, ones[0].Tree, ones[0].In.Card.F, fx)
	}
	if strings.TrimSpace(out.Title) == "" {
		out.Title = p.f.Title
	}
	for i := range ones {
		ones[i].Req.Title, ones[i].Req.Body = out.Title, out.Body
	}
	labels := map[string]string{}
	for _, o := range ones {
		labels[o.Repo] = o.Label
	}
	for _, pl := range publish.PlanAll(ctx, env, ones) {
		r := webapi.PublishSpanRepo{Repo: pl.Repo, Label: labels[pl.Repo], Error: publishErr(pl.Err)}
		if pl.Err == nil || pl.Facts.Branch != "" {
			fx := pl.Facts
			r.Summary, r.Fingerprint = pl.Plan.Summary, pl.Fingerprint
			r.Branch, r.Ahead, r.Base, r.BaseRepo, r.Hook, r.DraftWhy = fx.Branch, fx.Ahead, fx.Base, fx.BaseRepo, fx.Hook, fx.ReadyWhy
		}
		out.Repos = append(out.Repos, r)
	}
	if len(out.Repos) == 0 {
		out.Error = &webapi.PublishError{Code: string(publish.CodeNothingToPublish), Text: "no repository of this session has commits to publish"}
	}
	return out
}

// runSpan is the span itself, under the card's lock: each confirmed
// repository's facts are read again and must digest as the ones the person
// confirmed for it; a repository they did not confirm is left alone.
func (p publishDoer) runSpan(ctx context.Context, req webapi.PublishSpanRequest) webapi.PublishSpanResult {
	if strings.TrimSpace(req.Title) == "" {
		return webapi.PublishSpanResult{Error: &webapi.PublishError{Code: string(publish.CodeConfirmationNeeded), Text: "a pull request needs a title"}}
	}
	if p.locks != nil {
		release, err := p.locks.Acquire(p.f.ID)
		if err != nil {
			return webapi.PublishSpanResult{Error: &webapi.PublishError{Code: string(publish.CodeBusy), Text: cardLockedNotice(p.f.ID, err)}}
		}
		defer release()
	}
	f, ones, err := p.spanOnes(ctx, publish.Request{Act: publish.ActCreate, Title: strings.TrimSpace(req.Title), Body: req.Body, Draft: req.Draft})
	if err != nil {
		return webapi.PublishSpanResult{Error: publishErr(publish.AsError(err))}
	}
	picked := map[string]string{}
	for _, pk := range req.Repos {
		picked[pk.Repo] = pk.Fingerprint
	}
	var run []publish.One
	for _, o := range ones {
		if fp, ok := picked[o.Repo]; ok {
			o.Req.Fingerprint = fp
			run = append(run, o)
		}
	}
	if len(run) == 0 {
		return webapi.PublishSpanResult{Error: &webapi.PublishError{Code: string(publish.CodeNothingToPublish), Text: "no repository was confirmed"}}
	}
	var out webapi.PublishSpanResult
	for i, o := range publish.DoAll(ctx, publish.Env{GH: p.gh}, run) {
		res := o.Result
		if res.Pushed != "" || res.PR.URL != "" {
			// the card's thread says what was published, in every
			// repository; best-effort, as GitHub already has it
			_ = p.store.AppendPublish(ctx, f.ID, f.Stage, state.PublishPayload{
				Act: string(res.Act), Pushed: res.Pushed, Repo: res.Repo, Number: res.PR.Number, URL: res.PR.URL, Draft: res.Draft, By: p.by,
			}, time.Now())
		}
		one := webapi.PublishSpanOutcome{Repo: o.Repo, Label: run[i].Label, Error: publishErr(o.Err)}
		if o.Err == nil {
			one.Result = &webapi.PublishResult{Act: string(publish.ActCreate), Pushed: res.Pushed, URL: res.PR.URL, Number: res.PR.Number, Draft: res.Draft, LinkError: res.LinkErr}
		}
		out.Repos = append(out.Repos, one)
	}
	return out
}

// publishSpanText is what a span came to, a line per repository, and
// whether any of them failed.
func publishSpanText(name string, r webapi.PublishSpanResult) (string, bool) {
	if r.Error != nil {
		return publishErrorText(name, r.Error), true
	}
	failed := false
	lines := make([]string, 0, len(r.Repos))
	for _, o := range r.Repos {
		if o.Error != nil {
			failed = true
			lines = append(lines, publishErrorText(o.Label, o.Error))
			continue
		}
		lines = append(lines, publishResultText(o.Label, *o.Result))
	}
	return name + ":\n" + strings.Join(lines, "\n"), failed
}

// --- the TUI's face ---

// publishSpanFactsMsg carries a span's prepared facts back to the loop.
type publishSpanFactsMsg struct {
	f    domain.Feature
	span webapi.PublishSpan
}

// openPublishSpan reads every repository's facts off the loop.
func (m *Shell) openPublishSpan(r featureRow) tea.Cmd {
	p := m.publishDoerFor(r)
	m.notice = noticeMsg{}
	m.markGHWork(r.F.ID, "reading where each repository's branch goes", "")
	return func() tea.Msg {
		return publishSpanFactsMsg{f: r.F, span: p.prepareSpan(context.Background())}
	}
}

func (m *Shell) handlePublishSpanFacts(msg publishSpanFactsMsg) {
	m.endGHWork(msg.f.ID)
	if e := msg.span.Error; e != nil {
		m.notice = noticeMsg{text: sanitize(publishErrorText(msg.f.Name(), e)), isErr: true}
		return
	}
	m.notice = noticeMsg{}
	m.Overlay.Push(newPublishSpanDialog(msg.f, msg.span, func(req webapi.PublishSpanRequest) tea.Cmd {
		r, ok := m.rowByID(msg.f.ID)
		if !ok {
			return nil
		}
		// busy is read on the loop, as the person confirms
		p := m.publishDoerFor(r)
		m.markGHWork(msg.f.ID, fmt.Sprintf("opening %d pull requests", len(req.Repos)), "")
		return func() tea.Msg {
			text, failed := publishSpanText(msg.f.Name(), p.runSpan(context.Background(), req))
			text = sanitize(text)
			return ghDoneMsg{id: msg.f.ID, inner: noticeMsg{text: text, web: text, isErr: failed, reload: true}}
		}
	}))
}

// publishSpanDialog is a span's confirm: a line per repository saying what
// happens there (or why it is left out), the one title every PR opens
// with, the draft choice and the pre-push hooks to acknowledge.
type publishSpanDialog struct {
	f        domain.Feature
	span     webapi.PublishSpan
	title    textinput.Model
	draft    bool
	hookOK   bool
	problem  string
	onSubmit func(webapi.PublishSpanRequest) tea.Cmd
}

func newPublishSpanDialog(f domain.Feature, span webapi.PublishSpan, onSubmit func(webapi.PublishSpanRequest) tea.Cmd) *publishSpanDialog {
	in := textinput.New()
	in.Placeholder = "pull request title"
	in.CharLimit = 256
	in.SetWidth(52)
	in.SetValue(span.Title)
	in.Focus()
	return &publishSpanDialog{f: f, span: span, title: in, onSubmit: onSubmit}
}

// ID implements overlay.Dialog.
func (d *publishSpanDialog) ID() string { return "publish-span" }

// picks are the repositories the confirm will publish: every one not refused.
func (d *publishSpanDialog) picks() []webapi.PublishSpanPick {
	var out []webapi.PublishSpanPick
	for _, r := range d.span.Repos {
		if r.Error == nil {
			out = append(out, webapi.PublishSpanPick{Repo: r.Repo, Fingerprint: r.Fingerprint})
		}
	}
	return out
}

func (d *publishSpanDialog) hooks() []string {
	var out []string
	for _, r := range d.span.Repos {
		if r.Error == nil && r.Hook != "" {
			out = append(out, r.Label+": "+r.Hook)
		}
	}
	return out
}

// HandleKey implements overlay.Dialog.
func (d *publishSpanDialog) HandleKey(key tea.KeyPressMsg) (bool, tea.Cmd) {
	switch key.String() {
	case "esc":
		return true, nil
	case "ctrl+d":
		d.draft = !d.draft
		return false, nil
	case "ctrl+k":
		d.hookOK = !d.hookOK
		return false, nil
	case "enter":
		picks := d.picks()
		if len(picks) == 0 {
			d.problem = "no repository here can be published"
			return false, nil
		}
		if len(d.hooks()) > 0 && !d.hookOK {
			d.problem = "read the pre-push hooks, then ctrl+k to run them"
			return false, nil
		}
		title := strings.TrimSpace(d.title.Value())
		if title == "" {
			d.problem = "a pull request needs a title"
			return false, nil
		}
		return true, d.onSubmit(webapi.PublishSpanRequest{Title: title, Body: d.span.Body, Draft: d.draft, Repos: picks})
	}
	d.problem = ""
	d.title, _ = d.title.Update(key)
	return false, nil
}

// HandlePaste implements overlay.Paster.
func (d *publishSpanDialog) HandlePaste(msg tea.PasteMsg) tea.Cmd {
	d.title, _ = d.title.Update(msg)
	return nil
}

// View implements overlay.Dialog.
func (d *publishSpanDialog) View(s *theme.Styles, w, h int) string {
	var b strings.Builder
	b.WriteString(s.DialogTitle.Render("open pull requests · "+d.f.Name()) + "\n\n")
	for _, r := range d.span.Repos {
		if r.Error != nil {
			b.WriteString(s.Faint.Render(sanitize(publishErrorText(r.Label+": left out", r.Error))) + "\n")
			continue
		}
		line := sanitize(r.Label + ": " + r.Summary)
		if r.DraftWhy != "" {
			line += s.Faint.Render(" (draft — " + sanitize(r.DraftWhy) + ")")
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + d.title.View() + "\n")
	b.WriteString(s.Faint.Render("each description names the other repositories' pull requests") + "\n")
	box := "[ ]"
	if d.draft {
		box = "[x]"
	}
	b.WriteString(box + " open as drafts" + s.Faint.Render("  ctrl+d") + "\n")
	if hooks := d.hooks(); len(hooks) > 0 {
		box := "[ ]"
		if d.hookOK {
			box = "[x]"
		}
		b.WriteString("\n" + s.Warning.Render(box+" run "+sanitize(strings.Join(hooks, ", "))+" with your credential") + s.Faint.Render("  ctrl+k") + "\n")
	}
	if d.problem != "" {
		b.WriteString("\n" + s.Error.Render(d.problem) + "\n")
	}
	b.WriteString("\n" + s.Faint.Render("enter confirm · esc cancel"))
	return s.DialogFrame.Width(max(min(w-4, 76), 30)).Render(b.String())
}

// --- the web page's face ---

// PublishSpanFacts is GET /api/cards/{id}/publish/span: every repository's
// facts and the fingerprints the span's confirm sends back.
func (d *WebDocs) PublishSpanFacts(ctx context.Context) webapi.PublishSpan {
	if off := d.publishOff(); off != nil {
		return webapi.PublishSpan{Error: off}
	}
	return d.publishDoer().prepareSpan(ctx)
}

// PublishSpan is POST /api/cards/{id}/publish/span: the span the person
// confirmed. person is who confirmed it, as the thread will name them.
func (d *WebDocs) PublishSpan(ctx context.Context, req webapi.PublishSpanRequest, person string) webapi.PublishSpanResult {
	if off := d.publishOff(); off != nil {
		return webapi.PublishSpanResult{Error: off}
	}
	p := d.publishDoer()
	p.by = state.PersonActor(person)
	return p.runSpan(ctx, req)
}
