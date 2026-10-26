package ui

import (
	"context"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/morphis/gummi/internal/attachment"
	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/state"
	"github.com/morphis/gummi/internal/webapi"
	"github.com/morphis/gummi/internal/worktree"
)

// New cards from the web face. Both halves are the TUI's own card form
// (cardform.go): GET /api/form reads the choices off a form opened the
// way `n` opens it, and POST /api/cards fills one in and submits it — so
// the form's validation, its result and the shell's follow-up (createCard,
// cardCreated: stack placement, the session starting at once) are the
// ones the terminal runs, not a second copy.

// webKindValue is a card type as the form's kind choice names it.
func webKindValue(ct domain.CardType) string { return string(ct.Kind) }

// WebForm is GET /api/form: the new-card form's choices, for repo (the
// workspace default when empty).
func (m *Shell) WebForm(repo string) (webapi.Form, error) {
	d := m.openCardForm()
	if problem := m.webFormRepo(d, repo); problem != "" {
		return webapi.Form{}, refuse(WebBadRequest, problem)
	}
	f := webapi.Form{
		Kinds:     make([]webapi.Choice, 0, 1),
		Profiles:  append([]string{}, d.profiles...),
		Repos:     []string{},
		Branches:  append([]string{}, d.baseCands...),
		Stackable: []webapi.CardRef{},
		Envelope:  m.envelopePrefill(),
		Sessions:  m.webSessionModels(),
	}
	// The form makes a session, the one kind there is to make.
	ct := domain.CardType{Kind: domain.KindFreeform}
	f.Kinds = append(f.Kinds, webapi.Choice{Value: webKindValue(ct), Label: ct.Name(), Detail: firstLineOf(cardPlaceholder)})
	if d.repo.shown() {
		f.Repos = append(f.Repos, d.repo.options()...)
	}
	for _, c := range d.stackVisible() {
		f.Stackable = append(f.Stackable, webapi.CardRef{ID: string(c.ID), Title: c.Title})
	}
	return f, nil
}

func firstLineOf(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// webFormRepo moves the form's repo row to repo and offers that repo's
// branches, the way the form opens on its preselected one (carddoor.go).
func (m *Shell) webFormRepo(d *cardForm, repo string) string {
	if repo == "" {
		return ""
	}
	if !slices.Contains(d.repo.options(), repo) || !d.repo.shown() {
		if m.wt != nil {
			if err := m.wt.ClashError(repo); err != nil {
				return err.Error()
			}
		}
		return "repository " + strconv.Quote(repo) + " is not configured"
	}
	d.repo.selectName(repo)
	d.setRepoData(m.baseBranches, m.repoBranches)
	return ""
}

// Form is GET /api/form: WebForm's choices, with each installed agent's
// model list replaced by the merged view (SessionModelChoices) — the
// agent's own catalog where it can say one, plus the workspace's
// profile ids. The merge asks a backend and possibly runs its CLI, so it
// happens here, off the loop; WebForm built the same rows inline from
// SessionSuggestions alone.
func (b *Bridge) Form(ctx context.Context, repo string) (webapi.Form, error) {
	// the repository and branch lists are a git read the loop must not make
	// (the same call routes_create.go's handler made inline)
	b.RefreshRepos(ctx)
	var (
		f    webapi.Form
		werr error
	)
	if err := b.Do(ctx, func(m *Shell) tea.Cmd {
		f, werr = m.WebForm(repo)
		return nil
	}); err != nil {
		return webapi.Form{}, err
	}
	if werr != nil {
		return webapi.Form{}, werr
	}
	mergeSessionCatalog(ctx, b.shell.engine, &f.Sessions)
	return f, nil
}

// webFill sets the form's rows from a request, refusing a value no row
// offers — the same set the form's own cycling can reach.
func (m *Shell) webFill(d *cardForm, req webapi.CreateCardRequest) string {
	if problem := m.webFormRepo(d, req.Repo); problem != "" {
		return problem
	}
	if req.Backend != "" || strings.TrimSpace(req.Model) != "" {
		if problem := m.checkSessionPick(req.Backend, req.Model); problem != "" {
			return problem
		}
		d.sessionBackend, d.sessionModel = req.Backend, strings.TrimSpace(req.Model)
	}
	if req.Home {
		// an agent: the third "runs in" answer, in no repository at all
		switch {
		case req.Repo != "":
			return "an agent is in no repository — drop repo"
		case req.Base != "" || req.StackOn != "":
			return "an agent holds no branch — drop base and stackOn"
		}
		d.mainCheckout, d.home = true, true
	} else if req.MainCheckout {
		// the session's "runs in" choice
		if req.Base != "" {
			return "a session in the main checkout forks from no branch — drop base"
		}
		if req.StackOn != "" {
			return "a session in the main checkout has no branch to stack on — drop stackOn"
		}
		d.mainCheckout = true
	}
	if len(req.Attach) > 0 {
		if req.Home || req.MainCheckout {
			return "a session in the main checkout, or an agent, has no branch to attach repositories beside"
		}
		seen := map[string]bool{req.Repo: true}
		for _, n := range req.Attach {
			switch {
			case !slices.Contains(m.repoNames, n):
				return "repository " + strconv.Quote(n) + " is not in the workspace's pool"
			case seen[n]:
				return "repository " + strconv.Quote(n) + " is named twice, or is the session's own"
			}
			seen[n] = true
		}
	}
	text := strings.TrimSpace(req.Title)
	var body []string
	if s := strings.TrimSpace(req.Description); s != "" {
		body = append(body, s)
	}
	if len(req.Attachments) > 0 {
		if m.engine == nil {
			return m.noAgent(" (attachments need the workspace's store)")
		}
		refs, err := m.engine.Attachments().Resolve(req.Attachments)
		if err != nil {
			return err.Error()
		}
		for _, ref := range refs {
			body = append(body, attachment.Link(ref))
		}
	}
	if len(body) > 0 {
		text += "\n\n" + strings.Join(body, "\n\n")
	}
	if why := tooLong(d.text.CharLimit, "the card's text", text); why != "" {
		return why
	}
	d.text.SetValue(text)
	if name := strings.TrimSpace(req.Name); name != "" {
		if why := tooLong(d.name.CharLimit, "the session's name", name); why != "" {
			return why
		}
		d.name.SetValue(name)
	}
	if req.Envelope != nil {
		if why := tooLong(d.env.CharLimit, "that budget", domain.DollarsInput(*req.Envelope)); why != "" {
			return why
		}
		d.env.SetValue(domain.DollarsInput(*req.Envelope))
	}
	if req.Profile != "" {
		i := slices.Index(d.profiles, req.Profile)
		if i < 0 {
			return "profile " + strconv.Quote(req.Profile) + " is not configured"
		}
		d.profile = i
	}
	if req.Base != "" {
		if !slices.Contains(d.baseCands, req.Base) {
			return "branch " + strconv.Quote(req.Base) + " is not in the repository"
		}
		d.base = req.Base
	}
	if req.StackOn != "" {
		id := domain.FeatureID(req.StackOn)
		i := slices.IndexFunc(d.stackVisible(), func(c stackCand) bool { return c.ID == id })
		if !d.asksStack() || i < 0 {
			return m.cardName(id) + " is not a card this one can stack on"
		}
		c := d.stackVisible()[i]
		d.stackOnto, d.stackLabel = c.ID, c.Title
	}
	return ""
}

// RefreshRepos reads the repositories again — the discovered set first, then
// each repository's branches — off the board's loop, since it runs git, and
// installs the snapshot on the board. A clone made since launch is then a
// choice in the form the caller is about to open, with its branches.
func (b *Bridge) RefreshRepos(ctx context.Context) {
	var (
		wt  *worktree.Pool
		seq uint64
	)
	if b.Do(ctx, func(m *Shell) tea.Cmd { wt, seq = m.wt, m.nextRepoSeq(); return nil }) != nil || wt == nil {
		return
	}
	snap := readRepos(ctx, wt, seq)
	_ = b.Do(ctx, func(m *Shell) tea.Cmd { m.installRepos(snap); return nil })
}

// CreateCard is POST /api/cards: the new-card form, filled in and
// submitted as its Create button submits it. It answers the new card.
func (b *Bridge) CreateCard(ctx context.Context, req webapi.CreateCardRequest, person string) (webapi.Card, error) {
	if req.Kind != "" && req.Kind != string(domain.KindFreeform) {
		// the workflow kinds are retired: a session is the card there is
		return webapi.Card{}, refuse(WebBadRequest, "no card kind "+strconv.Quote(req.Kind)+" — only sessions are made now")
	}
	// A session is started from its first message, the way a person
	// starts a conversation, and takes its title from that message's first
	// line (domain.SplitFreeform) — the draft has no title field to fill.
	if strings.TrimSpace(req.Title) == "" && strings.TrimSpace(req.Description) == "" {
		return webapi.Card{}, refuse(WebBadRequest, "a card needs a title")
	}
	// the name the request carries may be a clone made since launch
	b.RefreshRepos(ctx)
	in := webInput{actor: state.PersonActor(person)}
	out, werr := b.intent(ctx, "", in, webWait, func(m *Shell, _ featureRow) (tea.Cmd, error) {
		d := m.openCardForm()
		if problem := m.webFill(d, req); problem != "" {
			return nil, refuse(WebBadRequest, problem)
		}
		done, cmd := d.submit()
		if !done {
			return nil, refuse(WebBadRequest, d.errText)
		}
		return cmd, nil
	})
	if werr != nil {
		return webapi.Card{}, werr
	}
	if out.created == "" {
		if e := out.err(); e != nil {
			return webapi.Card{}, e
		}
		return webapi.Card{}, refuse(WebConflict, "the card was not created")
	}
	if len(req.Attach) > 0 && b.shell.engine != nil {
		for _, n := range req.Attach {
			if _, err := b.shell.engine.AttachRepo(ctx, out.created, n, ""); err != nil {
				return webapi.Card{}, refuse(WebConflict, "the session "+string(out.created)+" was created, but attaching "+n+" failed: "+err.Error())
			}
		}
	}
	return b.Card(ctx, string(out.created))
}
