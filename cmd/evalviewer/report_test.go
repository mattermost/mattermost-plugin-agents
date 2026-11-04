// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"slices"
	"strings"
	"testing"
)

func graded(name, rubric string, run int, pass bool) EvalLogLine {
	return EvalLogLine{Name: name, Package: "react", RunNumber: run, Rubric: rubric, Pass: pass, Reasoning: "grader says " + rubric, Output: "agent output"}
}

func errored(name string, run int, msg string) EvalLogLine {
	return EvalLogLine{Name: name, Package: "react", RunNumber: run, Error: msg}
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

			comment := r.commentMarkdown("")
			if (comment != "") != (len(tc.wantRegressions) > 0) {
				t.Errorf("comment should be written only for regressions; got %q", comment)
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
	r.commit = "d1da9079abcdef"
	r.runURL = "https://github.com/o/r/actions/runs/1"
	comment := r.commentMarkdown("")

	for _, want := range []string{
		commentMarker,
		"Results for `d1da907` · [full run summary](https://github.com/o/r/actions/runs/1)",
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

func TestCommentLifecycle(t *testing.T) {
	const sha = "d1da9079abcdef"
	catCheck := caseKey{provider: "openai", test: "TestReactEval/react_cat_message", rubric: "is a cat"}
	unresolved := func(checks ...caseKey) string {
		return commentMarker + "\n\n" + checksLine(checks) + "\n⚠️ **1 eval check fails on this PR.**\n"
	}
	resolved := commentMarker + "\n\n" + commentResolved + " as of earlier.\n"
	notRechecked := "| TestReactEval › react cat message | is a cat | OpenAI |\n"

	allPass := []EvalLogLine{graded(catOpenAI, "is a cat", 0, true), graded(catAnthropic, "is a cat", 0, true)}
	openaiFails := []EvalLogLine{graded(catOpenAI, "is a cat", 0, false), graded(catAnthropic, "is a cat", 0, true)}
	openaiDown := []EvalLogLine{errored(catOpenAI, 0, "401 unauthorized"), graded(catAnthropic, "is a cat", 0, true)}
	openaiPartialAnthropicFails := []EvalLogLine{graded(sadOpenAI, "is sad", 0, true), graded(catAnthropic, "is a cat", 0, false)}
	catGone := []EvalLogLine{graded(sadOpenAI, "is sad", 0, true), graded(catAnthropic, "is a cat", 0, true)}
	completed := func(lines []EvalLogLine) []EvalLogLine {
		out := slices.Clone(lines)
		for i := range out {
			out[i].RunComplete = true
		}
		return out
	}

	tests := []struct {
		name        string
		previous    string
		results     []EvalLogLine
		baseline    []EvalLogLine
		want        []string
		wantNone    []string
		wantNoop    bool
		wantTracked []caseKey
	}{
		{name: "nothing to report creates no comment", results: allPass, wantNoop: true},
		{name: "an already resolved comment is left alone", previous: resolved, results: allPass, wantNoop: true},
		{
			name: "a rechecked check that passes resolves the comment", previous: unresolved(catCheck), results: allPass,
			want: []string{commentMarker, commentResolved + " as of d1da907."}, wantNone: []string{"Not rechecked"},
		},
		{
			name: "an old-format comment resolves when nothing fails", previous: commentMarker + "\n\n<!-- eval-providers: openai -->\n⚠️ failing\n", results: openaiDown,
			want: []string{commentResolved},
		},
		{
			name: "a rechecked check that still fails is a regression again", previous: unresolved(catCheck), results: openaiFails,
			want: []string{"| TestReactEval › react cat message | is a cat | OpenAI | 0/1 |"}, wantNone: []string{"Not rechecked"},
			wantTracked: []caseKey{catCheck},
		},
		{
			name: "an unavailable provider keeps its earlier checks", previous: unresolved(catCheck), results: openaiDown,
			want: []string{"### Not rechecked in this run", notRechecked}, wantNone: []string{commentResolved, "Passed runs"},
			wantTracked: []caseKey{catCheck},
		},
		{
			name: "a check that did not run stays listed while another provider regresses", previous: unresolved(catCheck), results: openaiPartialAnthropicFails,
			want:        []string{"| TestReactEval › react cat message | is a cat | Anthropic | 0/1 |", "### Not rechecked in this run", notRechecked},
			wantNone:    []string{commentResolved},
			wantTracked: []caseKey{{provider: "anthropic", test: "TestReactEval/react_cat_message", rubric: "is a cat"}, catCheck},
		},
		{
			name: "an errored check resolves once its eval is graded and passes", previous: unresolved(caseKey{provider: "openai", test: "TestReactEval/react_cat_message"}), results: allPass,
			want: []string{commentResolved}, wantNone: []string{"Not rechecked"},
		},
		{
			name: "a check whose rubric was edited resolves once the eval passes", previous: unresolved(caseKey{provider: "openai", test: "TestReactEval/react_cat_message", rubric: "old rubric text"}), results: allPass,
			want: []string{commentResolved}, wantNone: []string{"Not rechecked"},
		},
		{
			name: "a check that still fails with no baseline for its provider stays listed", previous: unresolved(catCheck), results: openaiFails,
			baseline: []EvalLogLine{graded(catAnthropic, "is a cat", 0, true)},
			want:     []string{"### Still failing, with nothing to compare against", notRechecked}, wantNone: []string{commentResolved, "Not rechecked"},
			wantTracked: []caseKey{catCheck},
		},
		{
			name: "a fixed check resolves even when another check of its eval fails with no baseline", previous: unresolved(catCheck),
			results:  []EvalLogLine{graded(catOpenAI, "is a cat", 0, true), graded(catOpenAI, "is cute", 0, false), graded(catAnthropic, "is a cat", 0, true)},
			baseline: []EvalLogLine{graded(catAnthropic, "is a cat", 0, true)},
			want:     []string{commentResolved}, wantNone: []string{"Still failing"},
		},
		{
			name: "an errored check stays listed through its eval's failures with no baseline", previous: unresolved(caseKey{provider: "openai", test: "TestReactEval/react_cat_message"}), results: openaiFails,
			baseline: []EvalLogLine{graded(catAnthropic, "is a cat", 0, true)},
			want:     []string{"### Still failing, with nothing to compare against", notRechecked}, wantNone: []string{commentResolved},
			wantTracked: []caseKey{catCheck},
		},
		{
			name: "a check that also fails on the baseline resolves the comment", previous: unresolved(catCheck), results: openaiFails,
			baseline: []EvalLogLine{graded(catOpenAI, "is a cat", 0, false), graded(catAnthropic, "is a cat", 0, true)},
			want:     []string{commentResolved},
		},
		{
			name: "a check whose eval was removed resolves the comment in a complete run", previous: unresolved(catCheck), results: completed(catGone),
			want: []string{commentResolved}, wantNone: []string{"Not rechecked"},
		},
		{
			name: "a check whose eval has no result stays listed in an incomplete run", previous: unresolved(catCheck), results: catGone,
			want: []string{"### Not rechecked in this run", notRechecked}, wantNone: []string{commentResolved},
			wantTracked: []caseKey{catCheck},
		},
		{
			name: "a provider that is gone keeps its earlier checks", previous: unresolved(caseKey{provider: "cohere", test: "TestX", rubric: "r"}), results: allPass,
			want: []string{"| TestX | r | Cohere |"}, wantTracked: []caseKey{{provider: "cohere", test: "TestX", rubric: "r"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var baseline resultSet
			if tc.baseline != nil {
				baseline = buildResultSet(tc.baseline)
			}
			r := buildReport(buildResultSet(tc.results), baseline, []string{"anthropic", "openai"})
			r.commit = sha
			comment := r.commentMarkdown(tc.previous)

			if tc.wantNoop {
				if comment != "" {
					t.Errorf("comment should be left alone; got %q", comment)
				}
				return
			}
			for _, want := range tc.want {
				if !strings.Contains(comment, want) {
					t.Errorf("comment missing %q:\n%s", want, comment)
				}
			}
			for _, none := range tc.wantNone {
				if strings.Contains(comment, none) {
					t.Errorf("comment should not contain %q:\n%s", none, comment)
				}
			}
			if got := previousChecks(comment); !slices.Equal(got, tc.wantTracked) {
				t.Errorf("tracked checks = %v, want %v", got, tc.wantTracked)
			}
		})
	}
}

func TestNotRecheckedCommentResolvesOnceRechecked(t *testing.T) {
	outage := buildReport(buildResultSet([]EvalLogLine{errored(catOpenAI, 0, "401 unauthorized"), graded(catAnthropic, "is a cat", 0, false)}), nil, []string{"anthropic", "openai"})
	first := outage.commentMarkdown(checksLine([]caseKey{{provider: "openai", test: "TestReactEval/react_cat_message", rubric: "is a cat"}}))

	stillDown := buildReport(buildResultSet([]EvalLogLine{errored(catOpenAI, 0, "401 unauthorized"), graded(catAnthropic, "is a cat", 0, true)}), nil, []string{"anthropic", "openai"})
	second := stillDown.commentMarkdown(first)
	if !strings.Contains(second, "Not rechecked") || strings.Contains(second, commentResolved) {
		t.Fatalf("openai check should still be listed; got %q", second)
	}

	recovered := buildReport(buildResultSet([]EvalLogLine{graded(catOpenAI, "is a cat", 0, true), graded(catAnthropic, "is a cat", 0, true)}), nil, []string{"anthropic", "openai"})
	if got := recovered.commentMarkdown(second); !strings.Contains(got, commentResolved) {
		t.Errorf("comment should resolve once openai is rechecked and nothing fails; got %q", got)
	}
}

func TestChecksLineRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		check caseKey
	}{
		{name: "comment terminator", check: caseKey{provider: "openai", test: "TestA/b", rubric: "says --> done"}},
		{name: "table and quotes", check: caseKey{provider: "openai", test: "TestA", rubric: `a | "b" 'c'`}},
		{name: "newlines", check: caseKey{provider: "anthropic", test: "TestA", rubric: "line one\nline two\r\n"}},
		{name: "errored", check: caseKey{provider: "openai", test: "TestA"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			line := checksLine([]caseKey{tc.check})
			if strings.Count(line, "-->") != 1 || strings.Contains(line, "\n") {
				t.Fatalf("line must be a single HTML comment: %q", line)
			}
			got := previousChecks("text before\n" + line + "\ntext after")
			if !slices.Equal(got, []caseKey{tc.check}) {
				t.Errorf("round trip = %v, want %v", got, tc.check)
			}
		})
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
			var c *caseStats
			for _, p := range buildResultSet([]EvalLogLine{tc.line}) {
				for _, pc := range p.cases {
					c = pc
				}
			}
			if got := reproduceCommand(c); got != tc.want {
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
