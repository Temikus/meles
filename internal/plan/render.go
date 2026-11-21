package plan

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// WriteTable prints repos with findings. Clean repos are left out on purpose:
// a wall of green rows hides the few that need attention.
func WriteTable(w io.Writer, p Plan) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if p.Summary.Findings > 0 {
		fmt.Fprintln(tw, "REPO\tPOLICY\tSUBJECT\tACTION\tMESSAGE")
	}
	for _, rp := range p.Repos {
		for _, it := range rp.Items {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", rp.Repo, it.Policy, it.Finding.Subject, it.Action, it.message())
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, rp := range p.Repos {
		for _, e := range rp.Errors {
			fmt.Fprintf(w, "error: %s: %s\n", rp.Repo, e)
		}
	}
	_, err := fmt.Fprintf(w, "\n%s\n", summaryLine(p.Summary, p.failed()))
	return err
}

// WriteMarkdown prints the plan for a GitHub job summary. The first line is
// the summary line; the rows match WriteTable.
func WriteMarkdown(w io.Writer, p Plan) error {
	var b strings.Builder
	b.WriteString(summaryLine(p.Summary, p.failed()) + "\n")
	if p.Summary.Findings > 0 {
		b.WriteString("\n| Repo | Policy | Subject | Action | Message |\n|---|---|---|---|---|\n")
	}
	for _, rp := range p.Repos {
		for _, it := range rp.Items {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", cell(rp.Repo), cell(it.Policy), cell(it.Finding.Subject), it.Action, cell(it.message()))
		}
	}
	var errs []string
	for _, rp := range p.Repos {
		for _, e := range rp.Errors {
			errs = append(errs, rp.Repo+": "+e)
		}
	}
	list(&b, "Errors", errs)
	list(&b, "Warnings", p.Warnings)
	_, err := io.WriteString(w, b.String())
	return err
}

func list(b *strings.Builder, title string, lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(b, "\n**%s**\n\n", title)
	for _, l := range lines {
		fmt.Fprintf(b, "- %s\n", firstLine(l))
	}
}

// cell keeps s inside one Markdown table cell.
func cell(s string) string {
	return strings.NewReplacer("|", `\|`, "\n", " ").Replace(s)
}

// message is the finding's message with its note and validator verdict.
func (it Item) message() string {
	msg := it.Finding.Message
	if it.Note != "" {
		msg += " (" + it.Note + ")"
	}
	if it.Invalid != "" {
		msg += " [INVALID: " + firstLine(it.Invalid) + "]"
	}
	return msg
}

// failed counts repos that could not be checked.
func (p Plan) failed() int {
	n := 0
	for _, rp := range p.Repos {
		if len(rp.Errors) > 0 {
			n++
		}
	}
	return n
}

// summaryLine leads with the repos checked so a clean run does not read as
// an empty one. Failed repos do not count as checked. Zero action counts are
// left out.
func summaryLine(s Summary, failed int) string {
	line := "Checked " + count(s.Repos, "repo") + ": "
	if failed > 0 {
		line = fmt.Sprintf("Checked %d of %s: ", s.Repos-failed, count(s.Repos, "repo"))
	}
	if s.Findings == 0 {
		line += "no findings"
	} else {
		var acts []string
		for _, a := range []Action{ActionPR, ActionApply, ActionReport, ActionCheck, ActionSkip} {
			if n := s.ByAction[a]; n > 0 {
				acts = append(acts, fmt.Sprintf("%d %s", n, a))
			}
		}
		line += fmt.Sprintf("%s in %s (%s)", count(s.Findings, "finding"), count(s.ReposWithFindings, "repo"), strings.Join(acts, ", "))
	}
	if failed > 0 {
		line += fmt.Sprintf(", %d failed", failed)
	}
	return line + "."
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// WriteJSON prints the plan as indented JSON.
func WriteJSON(w io.Writer, p Plan) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(p)
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}
