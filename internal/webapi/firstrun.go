package webapi

// FirstRun is GET /api/firstrun: the first-run setup the board is still
// offering, with everything the page shows of it. Pending false is a board
// with nothing to offer; every other field is then empty.
type FirstRun struct {
	Pending bool `json:"pending"`
	// Agents are the known agent CLIs, usable or not.
	Agents []FirstRunAgent `json:"agents"`
	// Agent is the preselected one, empty when none is usable.
	Agent string `json:"agent"`
	// AskAgent is whether the agent screen is shown: not when exactly one
	// agent is usable.
	AskAgent bool `json:"askAgent"`
	// Roles are the confirm screen's rows, in order.
	Roles []FirstRunRole `json:"roles"`
	// Profile is the name of the profile the setup writes.
	Profile string `json:"profile"`
}

// FirstRunAgent is one agent CLI as the setup offers it.
type FirstRunAgent struct {
	Name string `json:"name"`
	// Status is "usable", "not installed" or "signed out".
	Status string `json:"status"`
	// Hint says what to do about an agent that is not usable.
	Hint string `json:"hint,omitempty"`
	// Models are what the models screen offers; [] when not usable.
	Models   []string `json:"models"`
	Strong   string   `json:"strong,omitempty"`
	Everyday string   `json:"everyday,omitempty"`
}

// FirstRunRole is one row of the confirm screen: a role and the model
// tier it runs on.
type FirstRunRole struct {
	Role string `json:"role"`
	// Tier is "strong" or "everyday".
	Tier string `json:"tier"`
}

// FirstRunRequest is POST /api/firstrun: the person's answer, or a skip.
type FirstRunRequest struct {
	Skip     bool   `json:"skip,omitempty"`
	Agent    string `json:"agent,omitempty"`
	Strong   string `json:"strong,omitempty"`
	Everyday string `json:"everyday,omitempty"`
}

// FirstRunDone answers a POST: the notice to show.
type FirstRunDone struct {
	Notice string `json:"notice"`
}
