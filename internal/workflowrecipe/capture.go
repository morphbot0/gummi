package workflowrecipe

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

// fenceRe finds a fenced block of any language, its body captured.
var fenceRe = regexp.MustCompile("(?s)```[^\\n`]*\\n(.*?)```")

// CaptureArtifact is the fallback for a step that declared an artifact and
// ended without writing it (§25.4): what gummi writes in its place, from
// the step's output. A .json artifact takes the output's last JSON object;
// any other takes the body of its last fenced block. ok is false when the
// output holds neither, and the artifact stays unwritten.
func CaptureArtifact(output, file string) (string, bool) {
	if strings.EqualFold(filepath.Ext(file), ".json") {
		return lastJSONObject(output)
	}
	ms := fenceRe.FindAllStringSubmatch(output, -1)
	if len(ms) == 0 {
		return "", false
	}
	body := ms[len(ms)-1][1]
	if strings.TrimSpace(body) == "" {
		return "", false
	}
	return body, true
}

// lastJSONObject is the last top-level JSON object in text: an object
// nested in another is part of it, never one of its own.
func lastJSONObject(text string) (string, bool) {
	var last string
	for i := 0; i < len(text); {
		j := strings.IndexByte(text[i:], '{')
		if j < 0 {
			break
		}
		start := i + j
		dec := json.NewDecoder(strings.NewReader(text[start:]))
		var obj map[string]json.RawMessage
		if err := dec.Decode(&obj); err != nil {
			i = start + 1
			continue
		}
		end := start + int(dec.InputOffset())
		// an empty {} in prose is not the artifact a step wrote
		if len(obj) > 0 {
			last = text[start:end]
		}
		i = end
	}
	if last == "" {
		return "", false
	}
	return last + "\n", true
}
