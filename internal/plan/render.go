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
	for _, rp := range p.Repos {
		for _, e := range rp.Errors {
			fmt.Fprintf(w, "error: %s: %s\n", rp.Repo, e)
		}
	}
	s := p.Summary
	_, err := fmt.Fprintf(w, "\n%d findings in %d of %d repos: %d pr, %d apply, %d report, %d check, %d skip. Plan only, nothing changed.\n",
		s.Findings, s.ReposWithFindings, s.Repos, s.ByAction[ActionPR], s.ByAction[ActionApply], s.ByAction[ActionReport], s.ByAction[ActionCheck], s.ByAction[ActionSkip])
	return err
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
