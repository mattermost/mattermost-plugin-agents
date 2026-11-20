// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeSummary(t *testing.T, dir, night string, s runSummary) {
	t.Helper()
	path := filepath.Join(dir, night, "run", "summary.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTrendFromHistory(t *testing.T) {
	dir := t.TempDir()
	day := func(d int) time.Time { return time.Date(2026, 10, d, 6, 0, 0, 0, time.UTC) }

	healthy := buildReport(buildResultSet([]EvalLogLine{
		graded(catOpenAI, "is a cat", 0, true),
		graded(sadOpenAI, "is sad", 0, false),
	}), nil, []string{"openai"})
	broken := buildReport(buildResultSet([]EvalLogLine{errored(catOpenAI, 0, "403 model_not_found")}), nil, []string{"openai"})

	writeSummary(t, dir, "2026-10-12", healthy.runSummary(day(12), "aaaaaaa1111", ""))
	writeSummary(t, dir, "2026-10-13", broken.runSummary(day(13), "bbbbbbb2222", ""))
	writeSummary(t, dir, "2026-10-14", healthy.runSummary(day(14), "ccccccc3333", "https://example.com/run/3"))

	history, err := loadHistory(dir)
	if err != nil {
		t.Fatal(err)
	}
	trend := trendMarkdown(history, 2)

	newest := "| [2026-10-14](https://example.com/run/3) | ccccccc | 1/2 |"
	unavailable := "| 2026-10-13 | bbbbbbb | ⚪ Unavailable: 403 model_not_found |"
	for _, want := range []string{"## Eval trend (last 2 runs)", "### OpenAI", newest, unavailable} {
		if !strings.Contains(trend, want) {
			t.Errorf("trend missing %q:\n%s", want, trend)
		}
	}
	if strings.Index(trend, newest) > strings.Index(trend, unavailable) {
		t.Errorf("trend should list the newest run first:\n%s", trend)
	}
	if strings.Contains(trend, "aaaaaaa") {
		t.Errorf("trend should be limited to the 2 most recent runs:\n%s", trend)
	}
}

func TestRunSummaryCountsFailures(t *testing.T) {
	r := buildReport(buildResultSet([]EvalLogLine{
		graded(catOpenAI, "is a cat", 0, true),
		graded(sadOpenAI, "is sad", 0, false),
	}), buildResultSet([]EvalLogLine{graded(sadOpenAI, "is sad", 0, false)}), []string{"openai"})

	s := r.runSummary(time.Now(), "", "")

	if len(s.Providers) != 1 || s.Providers[0].Checks != 2 || s.Providers[0].Passing != 1 {
		t.Errorf("providers = %+v, want one provider with 1/2 passing", s.Providers)
	}
	if len(s.PreExisting) != 1 || s.PreExisting[0].Check != "is sad" || len(s.Regressions) != 0 {
		t.Errorf("pre-existing = %+v, regressions = %+v; want the failure to be pre-existing", s.PreExisting, s.Regressions)
	}
}

func TestUsageIsSummedPerProvider(t *testing.T) {
	withUsage := func(line EvalLogLine, agent, grader RoleUsage) EvalLogLine {
		line.Usage = &TrialUsage{Agent: agent, Grader: grader}
		return line
	}
	r := buildReport(buildResultSet([]EvalLogLine{
		withUsage(graded(catOpenAI, "is a cat", 0, true),
			RoleUsage{InputTokens: 1_000_000, CachedReadTokens: 500_000, OutputTokens: 2_000, CostUSD: 0.60},
			RoleUsage{InputTokens: 1_000, OutputTokens: 100, CostUSD: 0.01}),
		withUsage(graded(catOpenAI, "is a cat", 1, true),
			RoleUsage{InputTokens: 1_000_000, CachedReadTokens: 500_000, OutputTokens: 2_000, CostUSD: 0.60},
			RoleUsage{InputTokens: 1_000, OutputTokens: 100, CostUSD: 0.01}),
		// A model without a known price has tokens but no cost.
		withUsage(graded(catAnthropic, "is a cat", 0, true),
			RoleUsage{InputTokens: 10_000, OutputTokens: 500},
			RoleUsage{InputTokens: 1_000, OutputTokens: 100, CostUSD: 0.01}),
	}), nil, []string{"openai", "anthropic"})

	summary := r.summaryMarkdown()
	for _, want := range []string{
		"| OpenAI | ✅ Passing | 1/1 | 2.0M in / 4.0K out (50% cached) | $1.22 ($1.20 + $0.02 grading) |",
		"| Anthropic | ✅ Passing | 1/1 | 10.0K in / 500 out | $0.01+ ($0.00+ + $0.01 grading) |",
		"Total cost at list prices: $1.23+ ($1.20+ + $0.03 grading)",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary is missing %q:\n%s", want, summary)
		}
	}

	data, err := json.Marshal(r.runSummary(time.Now(), "abcdef1234", ""))
	if err != nil {
		t.Fatal(err)
	}
	var stored runSummary
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if trend := trendMarkdown([]runSummary{stored}, 0); !strings.Contains(trend, "| 1/1 | $1.22 ($1.20 + $0.02 grading) |") {
		t.Errorf("trend is missing the OpenAI cost:\n%s", trend)
	}
}
