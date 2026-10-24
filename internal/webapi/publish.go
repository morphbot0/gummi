package webapi

// Publishing a card (DESIGN §22): the facts a person confirms before an act
// and what the act did. The facts are resolved, not intended — the server
// reads them again when the act runs and refuses it ("facts-changed") when
// they no longer digest as Fingerprint.

// PublishFacts is GET /api/cards/{id}/publish?act=push|create|update|ready|draft
// (and, for a create on a fork, &baseRepo= for the repository chosen).
// Error is set, and the rest mostly empty, when the act is refused.
type PublishFacts struct {
	Act         string        `json:"act"`
	Error       *PublishError `json:"error,omitempty"`
	Summary     string        `json:"summary,omitempty"`
	Fingerprint string        `json:"fingerprint,omitempty"`
	Branch      string        `json:"branch,omitempty"`
	Tip         string        `json:"tip,omitempty"`
	TipSubject  string        `json:"tipSubject,omitempty"`
	Ahead       int           `json:"ahead,omitempty"`
	Base        string        `json:"base,omitempty"`
	Remote      string        `json:"remote,omitempty"`
	PushURL     string        `json:"pushUrl,omitempty"`
	Push        string        `json:"push,omitempty"`
	Head        string        `json:"head,omitempty"`
	// BaseRepo is the repository the PR opens in. BaseRepos, on a fork,
	// are the two it can open in; BaseRepo is then empty until a person
	// has chosen (the refusal "base-unchosen").
	BaseRepo  string   `json:"baseRepo,omitempty"`
	BaseRepos []string `json:"baseRepos,omitempty"`
	GH        string   `json:"gh,omitempty"`
	// Hook is a pre-push hook git will run with the person's credential;
	// the page asks for it to be acknowledged before the act.
	Hook string     `json:"hook,omitempty"`
	PR   *PublishPR `json:"pr,omitempty"`
	// Draft is the state a created PR opens in; DraftLocked says the
	// quality floor decides it (DraftWhy), not the person.
	Draft       bool   `json:"draft,omitempty"`
	DraftLocked bool   `json:"draftLocked,omitempty"`
	DraftWhy    string `json:"draftWhy,omitempty"`
	// ToDraft says this push returns a ready PR to draft first: its tip
	// is not verified, and a ready PR never gains unverified commits.
	ToDraft  bool     `json:"toDraft,omitempty"`
	Title    string   `json:"title,omitempty"`
	Body     string   `json:"body,omitempty"`
	Commands []string `json:"commands,omitempty"`
	// Steps are what the act does, in order: the dialog draws them as a
	// checklist while the act runs, each one marked by the "work" change
	// that names it.
	Steps []PublishStep `json:"steps,omitempty"`
}

// PublishStep is one stretch of an act a person waits on.
type PublishStep struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// PublishPR is a linked pull request as GitHub has it now.
type PublishPR struct {
	Number  int    `json:"number"`
	URL     string `json:"url"`
	State   string `json:"state"`
	Draft   bool   `json:"draft,omitempty"`
	HeadSHA string `json:"headSha,omitempty"`
}

// PublishError is a typed refusal or failure: Code is the same word on
// every face and the CLI ("dirty", "not-verified", "facts-changed" …).
type PublishError struct {
	Code string `json:"code"`
	Text string `json:"text"`
	Fix  string `json:"fix,omitempty"`
	// PR is an open pull request a "pr-exists" names, to link instead.
	PR int `json:"pr,omitempty"`
}

// PublishRequest is POST /api/cards/{id}/publish: the act, the
// fingerprint of the facts the person read, and their words.
type PublishRequest struct {
	Act         string `json:"act"`
	Fingerprint string `json:"fingerprint"`
	Title       string `json:"title,omitempty"`
	Body        string `json:"body,omitempty"`
	Draft       bool   `json:"draft,omitempty"`
	// BaseRepo is the repository the facts were read for, when the person
	// chose one.
	BaseRepo string `json:"baseRepo,omitempty"`
}

// PublishResult is what an act did, or — Error set and nothing published
// past what it names — why it did not.
type PublishResult struct {
	Act    string        `json:"act"`
	Error  *PublishError `json:"error,omitempty"`
	Pushed string        `json:"pushed,omitempty"`
	URL    string        `json:"url,omitempty"`
	Number int           `json:"number,omitempty"`
	Draft  bool          `json:"draft,omitempty"`
	// ToDraft is a ready PR returned to draft ahead of an unverified push.
	ToDraft bool `json:"toDraft,omitempty"`
	// LinkError is a PR opened that could not be linked to the card.
	LinkError string `json:"linkError,omitempty"`
}

// PublishOffer is the PR tab's publish strip: whether publishing is set up
// here (Why says what is missing when it is not) and which acts fit the
// card now, in the order the strip offers them.
type PublishOffer struct {
	Available bool     `json:"available"`
	Why       string   `json:"why,omitempty"`
	Acts      []string `json:"acts,omitempty"`
	// Unpushed counts the card's commits GitHub's PR head does not have.
	Unpushed int  `json:"unpushed,omitempty"`
	Draft    bool `json:"draft,omitempty"`
}

// PublishSpan is GET /api/cards/{id}/publish/span: a session that spans
// repositories (DESIGN §27.7) opening one pull request in each, the bodies
// naming one another. Repos is its own repository first, then every
// attached one not landed that has commits to publish; one that cannot
// be published carries its refusal and is left out of the act. Title and
// Body are what every PR starts from. Error is set when nothing can be
// read at all.
type PublishSpan struct {
	Error *PublishError     `json:"error,omitempty"`
	Title string            `json:"title,omitempty"`
	Body  string            `json:"body,omitempty"`
	Repos []PublishSpanRepo `json:"repos,omitempty"`
}

// PublishSpanRepo is one repository's side of a span: where its branch
// goes, the plan in one sentence, and the fingerprint the confirm sends
// back for it.
type PublishSpanRepo struct {
	// Repo is the attached repository's name, "" for the session's own.
	Repo        string        `json:"repo,omitempty"`
	Label       string        `json:"label"`
	Error       *PublishError `json:"error,omitempty"`
	Summary     string        `json:"summary,omitempty"`
	Fingerprint string        `json:"fingerprint,omitempty"`
	Branch      string        `json:"branch,omitempty"`
	Ahead       int           `json:"ahead,omitempty"`
	Base        string        `json:"base,omitempty"`
	BaseRepo    string        `json:"baseRepo,omitempty"`
	Hook        string        `json:"hook,omitempty"`
	// DraftWhy says the quality floor opens this repository's PR as a
	// draft whatever the person chooses.
	DraftWhy string `json:"draftWhy,omitempty"`
}

// PublishSpanRequest is POST /api/cards/{id}/publish/span: the words every
// PR opens with, and the repositories the person confirmed, each with the
// fingerprint of the facts they read for it.
type PublishSpanRequest struct {
	Title string            `json:"title"`
	Body  string            `json:"body,omitempty"`
	Draft bool              `json:"draft,omitempty"`
	Repos []PublishSpanPick `json:"repos"`
}

// PublishSpanPick is one confirmed repository of a span.
type PublishSpanPick struct {
	Repo        string `json:"repo,omitempty"`
	Fingerprint string `json:"fingerprint"`
}

// PublishSpanResult is what a span came to: each repository's result or
// failure, in order — one failing does not stop the others. Error is set
// when nothing was attempted.
type PublishSpanResult struct {
	Error *PublishError        `json:"error,omitempty"`
	Repos []PublishSpanOutcome `json:"repos,omitempty"`
}

// PublishSpanOutcome is one repository's part of a span.
type PublishSpanOutcome struct {
	Repo   string         `json:"repo,omitempty"`
	Label  string         `json:"label"`
	Result *PublishResult `json:"result,omitempty"`
	Error  *PublishError  `json:"error,omitempty"`
}
