package worktree

import (
	"regexp"
	"slices"
	"strings"
	"sync"
)

// Co-authors are the people a workspace names in a Co-authored-by trailer
// on every commit made in it (the `coauthors` config key, set from the
// settings). gummi appends the trailers to the commits it makes itself —
// checkpoints, a person's `commit`, squashes and landings — a commit-msg
// hook adds them to every other commit made under gummi (coauthorhook.go),
// and MatchesAttribution lets exactly those lines through. Any other Co-authored-by line is still
// what it was: an agent signing its own work.
//
// The list is the process's, like the credentials store: every gummi
// command reads it once where it opens the workspace, and the settings
// replace it when a person saves.

const coAuthorTrailer = "Co-authored-by: "

var coAuthors struct {
	mu   sync.RWMutex
	list []string
	// the co-author hook: its directory, the workspace root it is scoped
	// to, and whether this process has named it to git
	hooks, root string
	hooked      bool
}

// UseCoAuthors names the co-authors, each "Name <email>", for every commit
// this process makes from here on, and through the co-author hook for
// every commit made under it. An empty list means no trailer. The error is
// the hook's: the commits gummi writes itself carry the trailers either way.
func UseCoAuthors(list []string) error {
	coAuthors.mu.Lock()
	defer coAuthors.mu.Unlock()
	coAuthors.list = slices.Clone(list)
	return syncCoAuthorHook()
}

// CoAuthors is the list UseCoAuthors named.
func CoAuthors() []string {
	coAuthors.mu.RLock()
	defer coAuthors.mu.RUnlock()
	return slices.Clone(coAuthors.list)
}

// CoAuthorTrailers is the configured co-authors as the trailer lines a
// commit message ends with.
func CoAuthorTrailers() []string {
	list := CoAuthors()
	for i, who := range list {
		list[i] = coAuthorTrailer + who
	}
	return list
}

// trailerLineRe is one line of a trailer block: a token, a colon, a value.
var trailerLineRe = regexp.MustCompile(`^[A-Za-z0-9-]+: \S`)

// WithCoAuthors is message ending with the configured co-author trailers,
// each added once: one the message already carries is left where it is. A
// blank message stays blank — it means git writes its own.
func WithCoAuthors(message string) string {
	var missing []string
	for _, t := range CoAuthorTrailers() {
		if !hasLine(message, t) {
			missing = append(missing, t)
		}
	}
	body := strings.TrimRight(message, " \t\r\n")
	if len(missing) == 0 || body == "" {
		return message
	}
	// trailers join a trailer block the message already ends with, and
	// otherwise start one of their own
	sep := "\n\n"
	if i := strings.LastIndex(body, "\n\n"); i >= 0 {
		sep = "\n"
		for _, line := range strings.Split(body[i+2:], "\n") {
			if !trailerLineRe.MatchString(line) {
				sep = "\n\n"
				break
			}
		}
	}
	return body + sep + strings.Join(missing, "\n")
}

// withoutCoAuthors is s less the lines that are a configured co-author
// trailer: what is left is what the attribution scan reads.
func withoutCoAuthors(s string) string {
	trailers := CoAuthorTrailers()
	if len(trailers) == 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	lines = slices.DeleteFunc(lines, func(line string) bool {
		line = strings.TrimSpace(line)
		return slices.ContainsFunc(trailers, func(t string) bool { return strings.EqualFold(line, t) })
	})
	return strings.Join(lines, "\n")
}

func hasLine(s, want string) bool {
	for _, line := range strings.Split(s, "\n") {
		if strings.EqualFold(strings.TrimSpace(line), want) {
			return true
		}
	}
	return false
}
