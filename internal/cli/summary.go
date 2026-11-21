package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/temikus/meles/internal/plan"
)

// GitHub rejects a step summary over 1 MiB; stay clear of it.
const summaryLimit = 900 << 10

// writeJobSummary appends the plan and the outcome lines to the file GitHub
// Actions names in GITHUB_STEP_SUMMARY. Outside Actions it does nothing. A
// failure is a warning: the report must not fail the run.
func writeJobSummary(stderr io.Writer, title string, p plan.Plan, outcome []string) {
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return
	}
	var md bytes.Buffer
	_ = plan.WriteMarkdown(&md, p)
	var b bytes.Buffer
	fmt.Fprintf(&b, "## %s\n\n%s", title, md.Bytes())
	if len(outcome) > 0 {
		b.WriteString("\n**Outcome**\n\n")
		for _, l := range outcome {
			fmt.Fprintf(&b, "- %s\n", l)
		}
	}
	if b.Len() > summaryLimit {
		line, _, _ := bytes.Cut(md.Bytes(), []byte("\n"))
		b.Reset()
		fmt.Fprintf(&b, "## %s\n\n%s\n\nThe full report is too large for a job summary. See the job log.\n", title, line)
	}
	b.WriteString("\n")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		_, err = f.Write(b.Bytes())
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, "warning: job summary:", err)
	}
}
