package workflowrecipe

import "testing"

// A step that declared an artifact and did not write it has it captured
// from its output: a .json artifact from the last top-level JSON object,
// any other from the last fenced block; an output with neither leaves it
// unwritten.
func TestCaptureArtifact(t *testing.T) {
	for _, tc := range []struct {
		name, output, file, want string
		ok                       bool
	}{
		{
			"the last JSON object, nested ones inside it",
			"Merged.\n{\"verdict\": \"CHANGES\"}\nthen, revised:\n```json\n{\"verdict\": \"APPROVED\", \"findings\": [{\"at\": \"x.go\"}]}\n```\nDone.",
			"review.json", "{\"verdict\": \"APPROVED\", \"findings\": [{\"at\": \"x.go\"}]}\n", true,
		},
		{"a brace that is no JSON is passed over", "{not json} but {\"verdict\":\"APPROVED\"} {oops", "review.JSON", "{\"verdict\":\"APPROVED\"}\n", true},
		{"a trailing empty object in prose is not the artifact", "{\"verdict\":\"APPROVED\"}\nSee the {} placeholder.", "review.json", "{\"verdict\":\"APPROVED\"}\n", true},
		{"no JSON at all", "APPROVED, all good", "review.json", "", false},
		{"the last fenced block", "Design:\n```md\nfirst\n```\nand\n```\n# Design\n\nsecond\n```\n", "design.md", "# Design\n\nsecond\n", true},
		{"no fence", "# Design\n\njust prose", "design.md", "", false},
		{"an empty fence", "```\n```", "notes.txt", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := CaptureArtifact(tc.output, tc.file)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("CaptureArtifact = %q, %v; want %q, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}
