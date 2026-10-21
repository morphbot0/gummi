package ui

import (
	"regexp"
	"strings"

	"github.com/morphis/gummi/internal/domain"
	"github.com/morphis/gummi/internal/engine"
	"github.com/morphis/gummi/internal/workflow"
)

// A card of the retired workflow (FD, BG, RS) is kept to read: its stage,
// its next steps and the verdict its last stage session reached are still
// drawn, and nothing runs it. This file is the little of the stage loop's
// vocabulary that reading one needs.

// reviewVerdict is what a legacy card's stage session concluded.
type reviewVerdict int

const (
	verdictUnclear reviewVerdict = iota
	verdictPass
	verdictChanges
	verdictFail
	verdictBlocked
)

func (v reviewVerdict) String() string {
	switch v {
	case verdictPass:
		return "pass"
	case verdictChanges:
		return "changes"
	case verdictFail:
		return "fail"
	case verdictBlocked:
		return "blocked"
	}
	return "unclear"
}

// verdictFromTool maps a recorded submit_verdict value to a verdict.
func verdictFromTool(v string) reviewVerdict {
	switch v {
	case "pass":
		return verdictPass
	case "changes":
		return verdictChanges
	case "fail":
		return verdictFail
	case "blocked":
		return verdictBlocked
	}
	return verdictUnclear
}

// verdictLineRe finds a VERDICT: line in a session's last reply, for a
// session that never recorded one through the tool.
var verdictLineRe = regexp.MustCompile(`(?im)^[ \t]*[#>*+-]*[ \t]*[*_]{0,2}VERDICT[*_]{0,2}:\s*[*_]{0,2}(pass|changes|fail|blocked)\b`)

// rawVerdict is the session's own verdict, before any floor.
func rawVerdict(snap engine.Snapshot) reviewVerdict {
	if v := verdictFromTool(snap.Verdict); v != verdictUnclear {
		return v
	}
	for i := len(snap.Transcript) - 1; i >= 0; i-- {
		if snap.Transcript[i].Author != engine.AuthorAssistant {
			continue
		}
		if m := verdictLineRe.FindAllStringSubmatch(snap.Transcript[i].Content, -1); len(m) > 0 {
			return verdictFromTool(strings.ToLower(m[len(m)-1][1]))
		}
		break
	}
	return verdictUnclear
}

// sessionVerdict is the session's verdict with gummi's floor applied: a
// floor only ever downgrades.
func sessionVerdict(snap engine.Snapshot) reviewVerdict {
	raw := rawVerdict(snap)
	switch snap.VerdictFloor {
	case "fail":
		if raw == verdictPass || raw == verdictBlocked {
			return verdictFail
		}
	case "blocked":
		if raw == verdictPass {
			return verdictBlocked
		}
	}
	return raw
}

// flooredPass reports that a blocked verdict is gummi's floor refusing a
// pass the session itself wrote.
func flooredPass(snap engine.Snapshot) bool {
	return rawVerdict(snap) == verdictPass && snap.VerdictFloor == "blocked"
}

// forwardEdge is the primary forward stage out of f's current stage.
func forwardEdge(f domain.Feature) domain.Stage {
	nexts := workflow.Next(f.Stage)
	if len(nexts) == 0 {
		return f.Stage
	}
	// forward edges are listed before rerun edges
	return nexts[0]
}
