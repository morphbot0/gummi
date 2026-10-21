package domain

// DraftSeed is the content a feature draft is seeded with, mapped
// onto the spec template's sections (DESIGN §11.2). Empty fields fall
// back to the template's %% prompt, so a partial seed still yields
// a valid draft. The considered/chosen approach sections are never
// seeded — converging on an approach is brainstorm's job, not ingestion's.
type DraftSeed struct {
	Problem       string   // → ## Problem
	Constraints   string   // → ## Implementation notes
	Acceptance    string   // → ## Verification plan
	OpenQuestions []string // → %% markers under Problem (the checklist)
}

// DraftProvenance is the ingestion metadata rendered into a seeded draft's
// header: where it came from and what it depends on. Kept separate from
// DraftSeed (which is section content) so the template can render the two
// distinctly.
type DraftProvenance struct {
	Source    string   // .gummi/ingest/<name>.md, relative to the repo root
	Refs      []string // source section headings / ranges
	DependsOn []string // resolved dependency labels, e.g. "FD-002 payment-webhooks"
	// Adopted is the work this card was minted on top of, when it was
	// minted onto an existing branch. Nil for every card gummi cut a
	// branch for, which is nearly all of them.
	Adopted *AdoptedWork
}

// Empty reports whether there is no provenance to render.
//
// Adopted is deliberately not part of this: the inherited work renders as
// its own section rather than in the ingested-from header, so a card that
// was adopted but not ingested has an empty provenance and must not emit
// a header saying it came from nowhere.
func (p DraftProvenance) Empty() bool {
	return p.Source == "" && len(p.Refs) == 0 && len(p.DependsOn) == 0
}
