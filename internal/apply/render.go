package apply

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/temikus/meles/internal/housekeeping"
	"github.com/temikus/meles/internal/plan"
	"github.com/temikus/meles/internal/policy"
)

// Title is the same for every housekeeping PR, so a changed title means a
// human edited it.
const Title = "chore(meles): housekeeping"

// body is the PR description: one section per policy, then the marker. It
// must render the same for the same items, or every run would edit the PR.
func body(items []plan.Item, controlRef string) string {
	var b strings.Builder
	b.WriteString("meles keeps this repo in line with the shared config in the control repo. ")
	b.WriteString("Merge to take the changes. If you close this PR instead, each finding's `onClose` decides what happens: ")
	b.WriteString("`accept` findings are left alone, `reopen` findings come back on the next run.\n")
	last := ""
	for _, it := range items {
		if it.Policy != last {
			fmt.Fprintf(&b, "\n### %s\n\n", it.Policy)
			last = it.Policy
		}
		fmt.Fprintf(&b, "- **%s**: %s", it.Finding.Message, describe(it))
		if it.OnClose != "" {
			fmt.Fprintf(&b, " (on close: %s)", it.OnClose)
		}
		if it.Note != "" {
			fmt.Fprintf(&b, ". %s", it.Note)
		}
		b.WriteString("\n")
	}
	if controlRef != "" {
		fmt.Fprintf(&b, "\nRendered from control repo commit `%s`.\n", controlRef)
	}
	b.WriteString("\n" + housekeeping.Marker{Items: markerItems(items)}.Encode() + "\n")
	return b.String()
}

func describe(it plan.Item) string {
	fc := it.Change.(policy.FileChange)
	switch {
	case fc.Delete:
		return "deletes `" + fc.Path + "`"
	case it.Finding.Fix == policy.FixUpdate:
		return "updates `" + fc.Path + "`"
	default:
		return "creates `" + fc.Path + "`"
	}
}

func commitMessage(items []plan.Item) string {
	var b strings.Builder
	b.WriteString(Title + "\n\n")
	for _, it := range items {
		fmt.Fprintf(&b, "- %s: %s\n", it.Policy, it.Finding.Message)
	}
	b.WriteString("\n" + housekeeping.Trailer + "\n")
	return b.String()
}

// closedBody marks a PR meles closes itself, keeping what it carried.
func closedBody(old string) string {
	m, _ := housekeeping.Decode(old)
	text, _, _ := strings.Cut(old, "<!-- meles:v1 ")
	m.ClosedBy = housekeeping.ClosedByMeles
	return strings.TrimRight(text, "\n") + "\n\nClosed by meles: nothing is left to change.\n\n" + m.Encode() + "\n"
}

// acceptBody tells whoever closed the PR how to record the decision.
func acceptBody(items []plan.Item, today time.Time) string {
	var b strings.Builder
	b.WriteString(housekeeping.NoteMarker + "\n")
	b.WriteString("Closed without merging, so meles leaves these findings alone (`onClose: accept`). ")
	b.WriteString("They come back in a year, or sooner if the drift changes shape.\n\n")
	b.WriteString("To record why, add this to `.meles.yml` in this repo:\n\n```yaml\nexempt:\n")
	until := today.AddDate(1, 0, 0).Format(time.DateOnly)
	for _, it := range items {
		fmt.Fprintf(&b, "  - policy: %s\n    subject: %s\n    reason: \"TODO: why this repo differs\"\n    until: %s\n",
			it.Finding.Kind, it.Finding.Subject, until)
	}
	b.WriteString("```\n")
	return b.String()
}

func itoa(n int) string { return strconv.Itoa(n) }
