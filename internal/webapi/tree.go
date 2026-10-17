package webapi

// A card's working tree, worked by hand (GET /api/cards/{id}/tree): what
// is staged and what is not, the stashes made on its branch, and the
// writes a person makes to it (POST /api/cards/{id}/tree).

// Tree is the card's working tree as it reads now.
type Tree struct {
	Branch string `json:"branch"`
	// Remote is where the branch pushes; Ahead and Behind count against
	// that remote's copy of it, and Tracked says there is one.
	Remote  string `json:"remote,omitempty"`
	Tracked bool   `json:"tracked,omitempty"`
	Ahead   int    `json:"ahead,omitempty"`
	Behind  int    `json:"behind,omitempty"`
	Head    string `json:"head,omitempty"`
	// Remotes are the repository's remotes, origin first: what a fetch may
	// name.
	Remotes []string `json:"remotes"`
	// Why, when set, is why the tree may not be written now (an agent is
	// working in it); everything is still shown.
	Why       string      `json:"why,omitempty"`
	Staged    []TreeFile  `json:"staged"`
	Changes   []TreeFile  `json:"changes"`
	Conflicts []TreeFile  `json:"conflicts"`
	Stashes   []TreeStash `json:"stashes"`
	// Signing says a commit made here is signed with the stored key.
	Signing bool `json:"signing,omitempty"`
}

// TreeFile is one changed path. Status is M, A, D, R, T, U (conflicted)
// or ? (untracked); From is a rename's old path.
type TreeFile struct {
	Path   string `json:"path"`
	From   string `json:"from,omitempty"`
	Status string `json:"status"`
	Add    int    `json:"add,omitempty"`
	Del    int    `json:"del,omitempty"`
	Binary bool   `json:"binary,omitempty"`
}

// TreeStash is one stash made on the card's branch, named by its commit.
type TreeStash struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	At      int64  `json:"at,omitempty"`
}

// TreeDiff is one path's diff, staged or not (GET
// /api/cards/{id}/tree/diff?path=&staged=1). Digest is what a hunk write
// names, so a hunk chosen from a diff that has since changed is refused.
type TreeDiff struct {
	Path   string    `json:"path"`
	Staged bool      `json:"staged,omitempty"`
	Digest string    `json:"digest"`
	File   *DiffFile `json:"file,omitempty"`
}

// TreeOp is a write to the working tree.
type TreeOp string

const (
	TreeStage      TreeOp = "stage"
	TreeUnstage    TreeOp = "unstage"
	TreeDiscard    TreeOp = "discard"
	TreeHunk       TreeOp = "hunk"
	TreeCommit     TreeOp = "commit"
	TreeStashPush  TreeOp = "stash"
	TreeStashApply TreeOp = "stash-apply"
	TreeStashDrop  TreeOp = "stash-drop"
	TreeFetch      TreeOp = "fetch"
)

// TreeRequest is one write. Paths name the files a stage, unstage or
// discard takes; a hunk write names Path, Hunk (its index in the diff),
// Lines (indexes into the hunk's body; empty is the whole hunk), Digest and
// HunkOp ("stage", "unstage", "discard"). A commit takes Message and Amend;
// a stash Message; a stash apply or drop Stash, the stash's commit. A fetch
// takes Remote: one of Tree.Remotes, FetchAll for every remote, or empty
// for the remote the branch pushes to.
type TreeRequest struct {
	Op      TreeOp   `json:"op"`
	Paths   []string `json:"paths,omitempty"`
	Path    string   `json:"path,omitempty"`
	Hunk    int      `json:"hunk,omitempty"`
	Lines   []int    `json:"lines,omitempty"`
	Digest  string   `json:"digest,omitempty"`
	HunkOp  string   `json:"hunkOp,omitempty"`
	Message string   `json:"message,omitempty"`
	Amend   bool     `json:"amend,omitempty"`
	Stash   string   `json:"stash,omitempty"`
	Remote  string   `json:"remote,omitempty"`
}

// FetchAll is the Remote a fetch of every remote names.
const FetchAll = "*"

// TreeResult is a write's answer: what it did, in words, the commit a
// commit made, and the tree as it now reads.
type TreeResult struct {
	Done string `json:"done"`
	SHA  string `json:"sha,omitempty"`
	Tree Tree   `json:"tree"`
}
