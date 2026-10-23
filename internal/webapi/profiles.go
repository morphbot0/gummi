package webapi

// Profiles is GET /api/profiles, and the answer to every edit of them:
// .gummi/profiles.yaml for a person to read and change. A file that does
// not load is still listed, beside why, so the profile that breaks it can
// be fixed or deleted from here.
type Profiles struct {
	Path string `json:"path"`
	// Problem is why the file does not load, or why the board did not
	// apply its latest change; empty when neither.
	Problem string `json:"problem,omitempty"`
	// Default is the default profile's name, empty for none.
	Default string `json:"default,omitempty"`
	// Profiles are the default first, the rest by name.
	Profiles []Profile `json:"profiles"`
	// Slots are the roles gummi reads, in the order to show them.
	Slots []ProfileSlot `json:"slots"`
	// Backends are the names a role's backend may take.
	Backends []string `json:"backends"`
	// DefaultBackend is what a role with no backend runs on.
	DefaultBackend string `json:"defaultBackend,omitempty"`
}

// Profile is one profile: its roles, the slots first in Slots order.
type Profile struct {
	Name    string        `json:"name"`
	Sandbox string        `json:"sandbox,omitempty"`
	Roles   []ProfileRole `json:"roles"`
}

// ProfileRole is one role of a profile. Backend empty is the default
// backend.
type ProfileRole struct {
	Role           string `json:"role"`
	Backend        string `json:"backend,omitempty"`
	Model          string `json:"model"`
	OutputTokenMax int    `json:"outputTokenMax,omitempty"`
}

// ProfileSlot is one role gummi reads and what it decides.
type ProfileSlot struct {
	Role string `json:"role"`
	Help string `json:"help"`
}

// ProfileRequest is PUT /api/profiles/{name}: the profile as a whole —
// added, or replacing the one of that name — and whether it becomes the
// default. A role left out of Roles is no longer declared.
type ProfileRequest struct {
	Roles       []ProfileRole `json:"roles"`
	Sandbox     string        `json:"sandbox,omitempty"`
	MakeDefault bool          `json:"makeDefault,omitempty"`
}
