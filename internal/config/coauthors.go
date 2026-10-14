package config

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// MaxCoAuthors is how many co-authors a workspace may name, and
// maxCoAuthorLen how long one may be: a trailer block is read by people.
const (
	MaxCoAuthors   = 10
	maxCoAuthorLen = 200
)

const coAuthorPrefix = "co-authored-by:"

// ParseCoAuthor reads one co-author as a person writes it, "Name <email>"
// (a pasted "Co-authored-by: " in front is taken off), and returns it in
// the one spelling a trailer carries.
func ParseCoAuthor(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) >= len(coAuthorPrefix) && strings.EqualFold(s[:len(coAuthorPrefix)], coAuthorPrefix) {
		s = strings.TrimSpace(s[len(coAuthorPrefix):])
	}
	if len(s) > maxCoAuthorLen {
		return "", errors.New("that co-author is too long")
	}
	open := strings.LastIndexByte(s, '<')
	if open <= 0 || !strings.HasSuffix(s, ">") {
		return "", fmt.Errorf("a co-author is written Name <email>, got %q", s)
	}
	name, email := strings.TrimSpace(s[:open]), strings.TrimSpace(s[open+1:len(s)-1])
	bad := func(r rune) bool { return r < ' ' || r == '<' || r == '>' || r == 0x7f }
	if name == "" || strings.ContainsFunc(name, bad) {
		return "", fmt.Errorf("a co-author's name may not be empty or hold < > or control characters, got %q", s)
	}
	if strings.ContainsFunc(email, bad) || strings.ContainsAny(email, " \t") || !strings.Contains(email, "@") {
		return "", fmt.Errorf("%q does not look like an email address", email)
	}
	return name + " <" + email + ">", nil
}

// ParseCoAuthors reads a list of co-authors, dropping blank entries and
// repeats and keeping the order they were given in.
func ParseCoAuthors(list []string) ([]string, error) {
	var out []string
	for _, s := range list {
		if strings.TrimSpace(s) == "" {
			continue
		}
		who, err := ParseCoAuthor(s)
		if err != nil {
			return nil, err
		}
		if !containsFold(out, who) {
			out = append(out, who)
		}
	}
	if len(out) > MaxCoAuthors {
		return nil, fmt.Errorf("at most %d co-authors", MaxCoAuthors)
	}
	return out, nil
}

func containsFold(list []string, s string) bool {
	for _, have := range list {
		if strings.EqualFold(have, s) {
			return true
		}
	}
	return false
}

// SetCoAuthors writes the co-authors into the workspace config at path,
// leaving every other key, and its comments, where they were. An empty
// list removes the key. A missing file is created.
func SetCoAuthors(path string, list []string) error {
	list, err := ParseCoAuthors(list)
	if err != nil {
		return err
	}
	var value *yaml.Node
	if len(list) > 0 {
		value = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, who := range list {
			value.Content = append(value.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: who})
		}
	}
	return setKey(path, "coauthors", value, false)
}
