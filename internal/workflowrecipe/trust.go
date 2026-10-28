package workflowrecipe

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/morphis/gummi/internal/domain"
)

var (
	loginRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)
	teamRe  = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
)

// ParseTrust reads a watch's trust (DESIGN §28.3): a comma-separated list
// of @login, @org/team and role:write|maintain|admin. Empty is the login
// gummi publishes as. Anything else is refused, naming the entry.
func ParseTrust(s string) (domain.TrustFilter, error) {
	var t domain.TrustFilter
	if strings.TrimSpace(s) == "" {
		t.Self = true
		return t, nil
	}
	seenLogin, seenTeam := map[string]bool{}, map[domain.OrgTeam]bool{}
	for _, raw := range strings.Split(s, ",") {
		e := strings.TrimSpace(raw)
		switch {
		case e == "":
			return t, fmt.Errorf("trust has an empty entry between commas")
		case strings.HasPrefix(e, "role:"):
			role := strings.TrimPrefix(e, "role:")
			switch role {
			case "write", "maintain", "admin":
			default:
				return t, fmt.Errorf("trust entry %q: a role is role:write, role:maintain or role:admin", e)
			}
			if t.MinRole != "" {
				return t, fmt.Errorf("trust entry %q: a trust names one role", e)
			}
			t.MinRole = role
		case strings.HasPrefix(e, "@"):
			name := strings.TrimPrefix(e, "@")
			if org, team, isTeam := strings.Cut(name, "/"); isTeam {
				if !loginRe.MatchString(org) || !teamRe.MatchString(team) {
					return t, fmt.Errorf("trust entry %q: a team is @org/team-name", e)
				}
				ot := domain.OrgTeam{Org: strings.ToLower(org), Team: strings.ToLower(team)}
				if !seenTeam[ot] {
					seenTeam[ot] = true
					t.Teams = append(t.Teams, ot)
				}
				continue
			}
			if !loginRe.MatchString(name) {
				return t, fmt.Errorf("trust entry %q: a login is @ and a GitHub user name", e)
			}
			name = strings.ToLower(name)
			if !seenLogin[name] {
				seenLogin[name] = true
				t.Logins = append(t.Logins, name)
			}
		default:
			return t, fmt.Errorf("trust entry %q: write a person as @login, a team as @org/team, a role as role:write", e)
		}
	}
	return t, nil
}
