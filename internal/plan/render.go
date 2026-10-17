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
			msg := it.Finding.Message
			if it.Note != "" {
				msg += " (" + it.Note + ")"
			}
			if it.Invalid != "" {
				msg += " [INVALID: " + firstLine(it.Invalid) + "]"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", rp.Repo, it.Policy, it.Finding.Subject, it.Action, msg)
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	failed := 0
	for _, rp := range p.Repos {
		for _, e := range rp.Errors {
			fmt.Fprintf(w, "error: %s: %s\n", rp.Repo, e)
		}
		if len(rp.Errors) > 0 {
			failed++
		}
	}
	_, err := fmt.Fprintf(w, "\n%s\n", summaryLine(p.Summary, failed))
	return err
}

// summaryLine leads with the repos checked so a clean run does not read as
// an empty one. Zero action counts are left out.
func summaryLine(s Summary, failed int) string {
	line := "Checked " + count(s.Repos, "repo") + ": "
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
