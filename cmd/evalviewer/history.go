// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"time"
)

// runSummary is the machine-readable result of one eval run. Nightly runs
// store one per night so trends can be built from history.
type runSummary struct {
	GeneratedAt time.Time         `json:"generated_at"`
	Commit      string            `json:"commit,omitempty"`
	RunURL      string            `json:"run_url,omitempty"`
	Providers   []providerSummary `json:"providers"`
	Regressions []checkSummary    `json:"regressions"`
	PreExisting []checkSummary    `json:"pre_existing"`
	Unbaselined []checkSummary    `json:"unbaselined,omitempty"`
	Flaky       []checkSummary    `json:"flaky"`
}

type providerSummary struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`
	Checks    int    `json:"checks"`
	Passing   int    `json:"passing"`
	// Runs counts eval runs with usage; Agent and Grader are summed over them.
	Runs   int      `json:"runs"`
	Agent  RunUsage `json:"agent"`
	Grader RunUsage `json:"grader"`
}

type checkSummary struct {
	Provider string `json:"provider"`
	Eval     string `json:"eval"`
	Check    string `json:"check"`
	Passes   int    `json:"passes"`
	Runs     int    `json:"runs"`
}

func (r report) runSummary(now time.Time, commit, runURL string) runSummary {
	s := runSummary{
		GeneratedAt: now.UTC(),
		Commit:      commit,
		RunURL:      runURL,
		Regressions: checkSummaries(r.regressions),
		PreExisting: checkSummaries(r.preExisting),
		Unbaselined: checkSummaries(r.unbaselined),
		Flaky:       checkSummaries(r.flaky),
	}
	for _, p := range r.providers {
		ps := providerSummary{Name: p.name, Available: !p.unavailable()}
		if !ps.Available {
			ps.Error = p.firstError()
		}
		for _, c := range p.cases {
			ps.Checks++
			if !c.failing() {
				ps.Passing++
			}
		}
		total := p.usageTotals()
		ps.Runs, ps.Agent, ps.Grader = total.runs, total.agent, total.grader
		s.Providers = append(s.Providers, ps)
	}
	return s
}

func checkSummaries(cases []*caseStats) []checkSummary {
	out := make([]checkSummary, 0, len(cases))
	for _, c := range cases {
		out = append(out, checkSummary{Provider: c.provider, Eval: c.test, Check: checkLabel(c), Passes: c.passes, Runs: c.runs()})
	}
	return out
}

// loadHistory reads every summary.json under dir, oldest first.
func loadHistory(dir string) ([]runSummary, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	var history []runSummary
	err = fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "summary.json" {
			return nil
		}
		data, err := root.ReadFile(path)
		if err != nil {
			return err
		}
		var s runSummary
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
		history = append(history, s)
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(history, func(a, b runSummary) int { return a.GeneratedAt.Compare(b.GeneratedAt) })
	return history, nil
}

// trendMarkdown renders the most recent runs, newest first, as one table per
// provider.
func trendMarkdown(history []runSummary, limit int) string {
	if len(history) == 0 {
		return "No eval history found.\n"
	}
	if limit > 0 && len(history) > limit {
		history = history[len(history)-limit:]
	}

	var names []string
	for _, s := range history {
		for _, p := range s.Providers {
			if !slices.Contains(names, p.Name) {
				names = append(names, p.Name)
			}
		}
	}
	slices.Sort(names)

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Eval trend (last %d %s)\n", len(history), plural(len(history), "run", "runs"))
	for _, name := range names {
		fmt.Fprintf(&sb, "\n### %s\n\n", providerDisplayName(name))
		sb.WriteString("| Date | Commit | Checks passing | Input / output tokens per run | Cost per run | Latency per run |\n")
		sb.WriteString("|------|--------|----------------|-------------------------------|--------------|-----------------|\n")
		for _, s := range slices.Backward(history) {
			i := slices.IndexFunc(s.Providers, func(p providerSummary) bool { return p.Name == name })
			if i < 0 {
				continue
			}
			p := s.Providers[i]
			date := s.GeneratedAt.Format("2006-01-02")
			if s.RunURL != "" {
				date = fmt.Sprintf("[%s](%s)", date, s.RunURL)
			}
			commit := cmp.Or(shortCommit(s.Commit), "–")
			if !p.Available {
				fmt.Fprintf(&sb, "| %s | %s | ⚪ Unavailable: %s | – | – | – |\n", date, commit, tableCell(truncateString(oneLine(p.Error), 120)))
				continue
			}
			fmt.Fprintf(&sb, "| %s | %s | %d/%d | %s | %s | %s |\n", date, commit, p.Passing, p.Checks,
				perRun(p, func(u RunUsage) string {
					return formatTokens(float64(u.InputTokens)/float64(p.Runs)) + " / " + formatTokens(float64(u.OutputTokens)/float64(p.Runs))
				}),
				perRun(p, func(u RunUsage) string { return formatCost(u.CostUSD / float64(p.Runs)) }),
				perRun(p, func(u RunUsage) string { return formatDuration(float64(u.LatencyMS) / float64(p.Runs)) }))
		}
	}
	return sb.String()
}

func perRun(p providerSummary, format func(RunUsage) string) string {
	if p.Runs == 0 {
		return "–"
	}
	return format(p.Agent)
}

func shortCommit(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
