// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"strings"
	"testing"
)

func graded(name, rubric string, run int, pass bool) EvalLogLine {
	return EvalLogLine{Name: name, Package: "react", RunNumber: run, Rubric: rubric, Pass: pass, Reasoning: "grader says " + rubric, Output: "agent output"}
}

func errored(name string, run int, msg string) EvalLogLine {
	return EvalLogLine{Name: name, Package: "react", RunNumber: run, Error: msg}
}

func usageLine(name string, run int, inputTokens int64) EvalLogLine {
	return EvalLogLine{Name: name, RunNumber: run, Usage: &UsageRecord{
		Agent: RunUsage{Model: "gpt-6.1-sol", Calls: 1, InputTokens: inputTokens, OutputTokens: 100, CostUSD: 0.01, LatencyMS: 1500},
	}}
}

const (
	catOpenAI    = "TestReactEval/[openai]_react_cat_message"
	catAnthropic = "TestReactEval/[anthropic]_react_cat_message"
	sadOpenAI    = "TestReactEval/[openai]_react_negative_message"
)

func TestBuildReport(t *testing.T) {
	tests := []struct {
		name              string
		results           []EvalLogLine
		baseline          []EvalLogLine
		noBaseline        bool
		expectedProviders []string
		wantRegressions   []string
		wantPreExisting   []string
		wantUnbaselined   []string
		wantFlaky         []string
		wantUnavailable   []string
		wantTokenGrowth   []string
	}{
		{
			name:            "all passing",
			results:         []EvalLogLine{graded(catOpenAI, "is a cat", 0, true)},
			baseline:        []EvalLogLine{graded(catOpenAI, "is a cat", 0, true)},
			wantRegressions: nil,
		},
		{
			name:            "new failure is a regression",
			results:         []EvalLogLine{graded(catOpenAI, "is a cat", 0, false)},
			baseline:        []EvalLogLine{graded(catOpenAI, "is a cat", 0, true)},
			wantRegressions: []string{"openai TestReactEval/react_cat_message is a cat"},
		},
		{
			name:            "failure also on baseline is pre-existing",
			results:         []EvalLogLine{graded(catOpenAI, "is a cat", 0, false)},
			baseline:        []EvalLogLine{graded(catOpenAI, "is a cat", 0, false)},
			wantPreExisting: []string{"openai TestReactEval/react_cat_message is a cat"},
		},
		{
			name:            "baseline failure on another provider does not hide a regression",
			results:         []EvalLogLine{graded(catOpenAI, "is a cat", 0, false), graded(catAnthropic, "is a cat", 0, true)},
			baseline:        []EvalLogLine{graded(catOpenAI, "is a cat", 0, true), graded(catAnthropic, "is a cat", 0, false)},
			wantRegressions: []string{"openai TestReactEval/react_cat_message is a cat"},
		},
		{
			name:            "failure on a provider missing from the baseline is not a regression",
			results:         []EvalLogLine{graded(catOpenAI, "is a cat", 0, false), graded(catAnthropic, "is a cat", 0, true)},
			baseline:        []EvalLogLine{graded(catAnthropic, "is a cat", 0, true)},
			wantUnbaselined: []string{"openai TestReactEval/react_cat_message is a cat"},
		},
		{
			name:            "failure on a provider unavailable on the baseline is not a regression",
			results:         []EvalLogLine{graded(catOpenAI, "is a cat", 0, false), graded(catAnthropic, "is a cat", 0, true)},
			baseline:        []EvalLogLine{errored(catOpenAI, 0, "401 unauthorized"), graded(catAnthropic, "is a cat", 0, true)},
			wantUnbaselined: []string{"openai TestReactEval/react_cat_message is a cat"},
		},
		{
			name:            "baseline where every provider errored counts as no baseline",
			results:         []EvalLogLine{graded(catOpenAI, "is a cat", 0, false)},
			baseline:        []EvalLogLine{errored(catOpenAI, 0, "401 unauthorized")},
			wantRegressions: []string{"openai TestReactEval/react_cat_message is a cat"},
		},
		{
			name:            "without a baseline every failure is reported",
			results:         []EvalLogLine{graded(catOpenAI, "is a cat", 0, false)},
			noBaseline:      true,
			wantRegressions: []string{"openai TestReactEval/react_cat_message is a cat"},
		},
		{
			name: "majority of passing runs is flaky, not failing",
			results: []EvalLogLine{
				graded(catOpenAI, "is a cat", 0, true),
				graded(catOpenAI, "is a cat", 1, false),
				graded(catOpenAI, "is a cat", 2, true),
			},
			baseline:  []EvalLogLine{graded(catOpenAI, "is a cat", 0, true)},
			wantFlaky: []string{"openai TestReactEval/react_cat_message is a cat"},
		},
		{
			name: "majority of failing runs is a regression",
			results: []EvalLogLine{
				graded(catOpenAI, "is a cat", 0, false),
				graded(catOpenAI, "is a cat", 1, false),
				graded(catOpenAI, "is a cat", 2, true),
			},
			baseline:        []EvalLogLine{graded(catOpenAI, "is a cat", 0, true)},
			wantRegressions: []string{"openai TestReactEval/react_cat_message is a cat"},
		},
		{
			name:              "provider where every run errored is unavailable, not a regression",
			results:           []EvalLogLine{errored(catOpenAI, 0, "401 unauthorized"), errored(sadOpenAI, 0, "401 unauthorized"), graded(catAnthropic, "is a cat", 0, true)},
			baseline:          []EvalLogLine{graded(catOpenAI, "is a cat", 0, true)},
			expectedProviders: []string{"anthropic", "openai"},
			wantUnavailable:   []string{"openai"},
		},
		{
			name:              "expected provider without results is unavailable",
			results:           []EvalLogLine{graded(catAnthropic, "is a cat", 0, true)},
			baseline:          []EvalLogLine{graded(catAnthropic, "is a cat", 0, true)},
			expectedProviders: []string{"anthropic", "azure"},
			wantUnavailable:   []string{"azure"},
		},
		{
			name:            "one errored eval on a working provider is a regression",
			results:         []EvalLogLine{errored(catOpenAI, 0, "tools not supported"), graded(sadOpenAI, "is sad", 0, true)},
			baseline:        []EvalLogLine{graded(catOpenAI, "is a cat", 0, true), graded(sadOpenAI, "is sad", 0, true)},
			wantRegressions: []string{"openai TestReactEval/react_cat_message "},
		},
		{
			name: "errored run outvoted by graded runs is flaky",
			results: []EvalLogLine{
				errored(catOpenAI, 0, "timeout"),
				graded(catOpenAI, "is a cat", 1, true),
				graded(catOpenAI, "is a cat", 2, true),
			},
			baseline:  []EvalLogLine{graded(catOpenAI, "is a cat", 0, true)},
			wantFlaky: []string{"openai TestReactEval/react_cat_message "},
		},
		{
			name:            "input token growth over both thresholds is reported",
			results:         []EvalLogLine{graded(catOpenAI, "is a cat", 0, true), usageLine(catOpenAI, 0, 6000)},
			baseline:        []EvalLogLine{graded(catOpenAI, "is a cat", 0, true), usageLine(catOpenAI, 0, 4000)},
			wantTokenGrowth: []string{"openai TestReactEval/react_cat_message"},
		},
		{
			name:     "large relative growth of a small prompt is not reported",
			results:  []EvalLogLine{graded(catOpenAI, "is a cat", 0, true), usageLine(catOpenAI, 0, 900)},
			baseline: []EvalLogLine{graded(catOpenAI, "is a cat", 0, true), usageLine(catOpenAI, 0, 300)},
		},
		{
			name: "token growth is compared per run",
			results: []EvalLogLine{
				graded(catOpenAI, "is a cat", 0, true), usageLine(catOpenAI, 0, 4000),
				graded(catOpenAI, "is a cat", 1, true), usageLine(catOpenAI, 1, 4000),
			},
			baseline: []EvalLogLine{graded(catOpenAI, "is a cat", 0, true), usageLine(catOpenAI, 0, 4000)},
		},
		{
			name:       "token growth needs a baseline",
			results:    []EvalLogLine{graded(catOpenAI, "is a cat", 0, true), usageLine(catOpenAI, 0, 6000)},
			noBaseline: true,
		},
		{
			name:            "usage lines are not counted as results",
			results:         []EvalLogLine{usageLine(catOpenAI, 0, 1000), graded(catAnthropic, "is a cat", 0, true)},
			baseline:        []EvalLogLine{graded(catAnthropic, "is a cat", 0, true)},
			wantUnavailable: []string{"openai"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var baseline resultSet
			if !tc.noBaseline {
				baseline = buildResultSet(tc.baseline)
			}
			r := buildReport(buildResultSet(tc.results), baseline, tc.expectedProviders)

			assertCases(t, "regressions", r.regressions, tc.wantRegressions)
			assertCases(t, "pre-existing", r.preExisting, tc.wantPreExisting)
			assertCases(t, "unbaselined", r.unbaselined, tc.wantUnbaselined)
			assertCases(t, "flaky", r.flaky, tc.wantFlaky)

			var unavailable []string
			for _, p := range r.providers {
				if p.unavailable() {
					unavailable = append(unavailable, p.name)
				}
			}
			assertStrings(t, "unavailable", unavailable, tc.wantUnavailable)

			var growth []string
			for _, c := range r.tokenGrowth {
				growth = append(growth, c.provider+" "+c.test)
			}
			assertStrings(t, "token growth", growth, tc.wantTokenGrowth)

			comment := r.commentMarkdown()
			if (comment != "") != (len(tc.wantRegressions)+len(tc.wantTokenGrowth) > 0) {
				t.Errorf("comment should be written only for regressions or token growth; got %q", comment)
			}
		})
	}
}

func TestCommentMarkdown(t *testing.T) {
	results := buildResultSet([]EvalLogLine{
		graded(catOpenAI, "names a cat | heart emoji", 0, false),
		graded(sadOpenAI, "is sad", 0, true),
	})
	r := buildReport(results, resultSet{}, nil)
	comment := r.commentMarkdown()

	for _, want := range []string{
		commentMarker,
		"| TestReactEval › react cat message | names a cat \\| heart emoji | OpenAI | 0/1 |",
		"grader says names a cat | heart emoji",
		"agent output",
		"GOEVALS=1 LLM_PROVIDER=openai go test ./react -run '^TestReactEval$/^\\[openai\\]_react_cat_message$' -v",
	} {
		if !strings.Contains(comment, want) {
			t.Errorf("comment missing %q:\n%s", want, comment)
		}
	}
	if strings.Contains(comment, "is sad") {
		t.Errorf("comment should not mention passing checks:\n%s", comment)
	}
}

func TestReproduceCommandQuoting(t *testing.T) {
	tests := []struct {
		name string
		line EvalLogLine
		want string
	}{
		{
			name: "apostrophe in the test name",
			line: graded("TestDirectMessageConversations/[openai]_bot_dm_knows_it_doesn't_know_what_it_can_do", "refers to docs", 0, false),
			want: `GOEVALS=1 LLM_PROVIDER=openai go test ./react -run '^TestDirectMessageConversations$/^\[openai\]_bot_dm_knows_it_doesn'\''t_know_what_it_can_do$' -v`,
		},
		{
			name: "shell syntax in the provider and package",
			line: EvalLogLine{Name: "TestReactEval/[openai;curl_evil|sh]_cat", Package: "react$(id)", Rubric: "is a cat"},
			want: `GOEVALS=1 LLM_PROVIDER='openai;curl_evil|sh' go test './react$(id)' -run '^TestReactEval$/^\[openai;curl_evil\|sh\]_cat$' -v`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := buildReport(buildResultSet([]EvalLogLine{tc.line}), nil, nil)
			if got := reproduceCommand(r.regressions[0]); got != tc.want {
				t.Errorf("reproduceCommand() = %s\nwant %s", got, tc.want)
			}
		})
	}
}

func TestNestedEvalNames(t *testing.T) {
	name := "TestSearchPostsOutputQualityEval/keyword/[openai]_search_posts_keyword_quality"
	results := buildResultSet([]EvalLogLine{graded(name, "lists results", 0, false)})
	r := buildReport(results, nil, nil)

	if len(r.regressions) != 1 || r.regressions[0].provider != "openai" {
		t.Fatalf("regressions = %+v, want one openai regression", r.regressions)
	}
	c := r.regressions[0]
	if got, want := displayTestName(c.test), "TestSearchPostsOutputQualityEval › keyword › search posts keyword quality"; got != want {
		t.Errorf("displayTestName() = %q, want %q", got, want)
	}
	if got, want := reproduceCommand(c), `GOEVALS=1 LLM_PROVIDER=openai go test ./react -run '^TestSearchPostsOutputQualityEval$/^keyword$/^\[openai\]_search_posts_keyword_quality$' -v`; got != want {
		t.Errorf("reproduceCommand() = %s\nwant %s", got, want)
	}
}

func assertCases(t *testing.T, label string, cases []*caseStats, want []string) {
	t.Helper()
	got := make([]string, 0, len(cases))
	for _, c := range cases {
		got = append(got, c.provider+" "+c.test+" "+c.rubric)
	}
	assertStrings(t, label, got, want)
}

func assertStrings(t *testing.T, label string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s = %q, want %q", label, got, want)
	}
}

func TestSummaryIncludesCostAndLatency(t *testing.T) {
	results := buildResultSet([]EvalLogLine{
		graded(catOpenAI, "is a cat", 0, true), usageLine(catOpenAI, 0, 4000),
		graded(catOpenAI, "is a cat", 1, true), usageLine(catOpenAI, 1, 6000),
	})
	baseline := buildResultSet([]EvalLogLine{graded(catOpenAI, "is a cat", 0, true), usageLine(catOpenAI, 0, 4000)})

	summary := buildReport(results, baseline, nil).summaryMarkdown()

	for _, want := range []string{
		"| OpenAI | gpt-6.1-sol | 2 | 10k / 200 | $0.02 | – | 1.5s | +0% |",
		"| TestReactEval › react cat message | OpenAI | 5.0k | 100 | $0.01 | 1.5s | +25% | +0% |",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary missing %q:\n%s", want, summary)
		}
	}
}
