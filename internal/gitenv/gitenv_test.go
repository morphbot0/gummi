package gitenv

import (
	"os"
	"testing"
)

// Two owners' entries sit side by side after the environment's own, and
// one leaving takes only its own.
func TestOwnersShareTheEnvironment(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.abbrev")
	t.Setenv("GIT_CONFIG_VALUE_0", "12")
	t.Cleanup(func() {
		Set("a", nil)
		Set("b", nil)
	})
	Set("b", [][2]string{{"b.one", "1"}})
	Set("a", [][2]string{{"a.one", "1"}, {"a.two", "2"}})
	want := map[string]string{
		"GIT_CONFIG_COUNT": "4",
		"GIT_CONFIG_KEY_0": "core.abbrev", "GIT_CONFIG_VALUE_0": "12",
		"GIT_CONFIG_KEY_1": "a.one", "GIT_CONFIG_KEY_2": "a.two", "GIT_CONFIG_KEY_3": "b.one",
	}
	for k, v := range want {
		if got := os.Getenv(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	Set("a", nil)
	if got := os.Getenv("GIT_CONFIG_COUNT"); got != "2" {
		t.Errorf("GIT_CONFIG_COUNT after one owner left = %q", got)
	}
	if got := os.Getenv("GIT_CONFIG_KEY_1"); got != "b.one" {
		t.Errorf("GIT_CONFIG_KEY_1 = %q", got)
	}
	if _, ok := os.LookupEnv("GIT_CONFIG_KEY_2"); ok {
		t.Error("an entry past the count was left behind")
	}
	Set("b", nil)
	if got := os.Getenv("GIT_CONFIG_COUNT"); got != "1" {
		t.Errorf("GIT_CONFIG_COUNT with no owner = %q, want the environment's own", got)
	}
}
