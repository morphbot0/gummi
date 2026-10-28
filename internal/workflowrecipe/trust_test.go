package workflowrecipe

import (
	"reflect"
	"strings"
	"testing"

	"github.com/morphis/gummi/internal/domain"
)

func TestParseTrust(t *testing.T) {
	got, err := ParseTrust("@alice, @Acme/Core, role:maintain, @ALICE")
	if err != nil {
		t.Fatal(err)
	}
	want := domain.TrustFilter{
		Logins:  []string{"alice"},
		Teams:   []domain.OrgTeam{{Org: "acme", Team: "core"}},
		MinRole: "maintain",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if self, err := ParseTrust(""); err != nil || !self.Self {
		t.Errorf("empty trust = %+v, %v; want Self", self, err)
	}
	for in, why := range map[string]string{
		"alice":                       "write a person as @login",
		"@":                           "a login is @",
		"@a/b/c":                      "a team is @org/team",
		"role:read":                   "role:write, role:maintain or role:admin",
		"role:triage":                 "role:write, role:maintain or role:admin",
		"@alice,,@bob":                "empty entry",
		"role:write, role:admin":      "names one role",
		"@-alice":                     "a login is @",
		"@alice,":                     "empty entry",
		"@org/":                       "a team is @org/team",
		"@bad name":                   "a login is @",
		"@org/te am":                  "a team is @org/team",
		strings.Repeat("a", 3):        "write a person as @login",
		"@" + strings.Repeat("a", 40): "a login is @",
	} {
		if _, err := ParseTrust(in); err == nil || !strings.Contains(err.Error(), why) {
			t.Errorf("ParseTrust(%q) = %v, want %q", in, err, why)
		}
	}
}
