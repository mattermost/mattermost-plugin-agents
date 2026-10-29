// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// commentMarker identifies the eval results comment on a pull request. The
// heading is kept from earlier versions so their comments are updated in place.
const commentMarker = "## 🤖 LLM Evaluation Results"

var providerDisplayNames = map[string]string{
	"openai":           "OpenAI",
	"anthropic":        "Anthropic",
	"azure":            "Azure OpenAI",
	"mistral":          "Mistral",
	"bedrock":          "AWS Bedrock",
	"cohere":           "Cohere",
	"openaicompatible": "OpenAI-compatible",
}

func providerDisplayName(provider string) string {
	if name, ok := providerDisplayNames[provider]; ok {
		return name
	}
	return provider
}

// caseKey identifies one graded check: a rubric of an eval for a provider.
// An empty rubric holds runs that errored before grading.
type caseKey struct {
	provider string
	test     string
	rubric   string
}

type caseStats struct {
	caseKey
	pkg      string
	segments []string
	passes   int
	failures int
	// sample is the most recent failed or errored record, shown in reports.
	sample EvalLogLine
}

func (c *caseStats) runs() int {
	return c.passes + c.failures
}

// failing is a majority vote across runs, so a single flaky run among several
// does not count as a failure.
func (c *caseStats) failing() bool {
	return c.failures > c.passes
}

func (c *caseStats) flaky() bool {
	return c.failures > 0 && !c.failing()
}

func (c *caseStats) errored() bool {
	return c.rubric == ""
}

type providerResults struct {
	name    string
	records int
	errors  int
	cases   map[caseKey]*caseStats
	// complete is set when every eval package of the provider's run finished.
	complete bool
}

// unavailable reports a provider whose every run errored or that produced no
// results at all: a credentials or configuration problem, not a PR regression.
func (p *providerResults) unavailable() bool {
	return p.records == 0 || p.records == p.errors
}

func (p *providerResults) firstError() string {
	for _, c := range p.sortedCases() {
		if c.sample.Error != "" {
			return c.sample.Error
		}
	}
	return "no results were recorded; check that the provider credentials are configured"
}

func (p *providerResults) ranEval(test string) bool {
	for key := range p.cases {
		if key.test == test {
			return true
		}
	}
	return false
}

func (p *providerResults) sortedCases() []*caseStats {
	cases := make([]*caseStats, 0, len(p.cases))
	for _, c := range p.cases {
		cases = append(cases, c)
	}
	slices.SortFunc(cases, func(a, b *caseStats) int {
		return cmp.Or(cmp.Compare(a.test, b.test), cmp.Compare(a.rubric, b.rubric))
	})
	return cases
}

// resultSet holds results grouped by provider. A nil resultSet means no
// results were available at all (for example, a missing baseline).
type resultSet map[string]*providerResults

func (r resultSet) provider(name string) *providerResults {
	if p, ok := r[name]; ok {
		return p
	}
	p := &providerResults{name: name, cases: map[caseKey]*caseStats{}}
	r[name] = p
	return p
}

// splitEvalName splits "TestReactEval/[openai]_react_cat_message" into the
// provider, a provider-independent test name, and the name's segments. The
// provider tag can be on any segment, since evals.Run may be called inside a
// subtest.
func splitEvalName(name string) (provider, test string, segments []string) {
	segments = strings.Split(name, "/")
	provider = "unknown"
	parts := slices.Clone(segments)
	for i, segment := range parts {
		if after, ok := strings.CutPrefix(segment, "["); ok {
			if p, rest, found := strings.Cut(after, "]"); found {
				provider = p
				parts[i] = strings.TrimPrefix(rest, "_")
				break
			}
		}
	}
	return provider, strings.Join(parts, "/"), segments
}

func buildResultSet(records []EvalLogLine) resultSet {
	set := resultSet{}
	gradedRuns := map[[2]string]map[int]bool{}
	for _, record := range records {
		provider, test, segments := splitEvalName(record.Name)
		p := set.provider(provider)
		p.complete = p.complete || record.RunComplete
		p.records++

		key := caseKey{provider: provider, test: test, rubric: record.Rubric}
		if record.Error != "" {
			p.errors++
			key.rubric = ""
		}
		c, ok := p.cases[key]
		if !ok {
			c = &caseStats{caseKey: key, pkg: record.Package, segments: segments}
			p.cases[key] = c
		}
		if record.Error == "" {
			runs := gradedRuns[[2]string{provider, test}]
			if runs == nil {
				runs = map[int]bool{}
				gradedRuns[[2]string{provider, test}] = runs
			}
			runs[record.RunNumber] = true
		}
		if record.Error == "" && record.Pass {
			c.passes++
		} else {
			c.failures++
			c.sample = record
		}
	}

	// Runs of the same eval that were graded count against its errored runs,
	// so an eval only counts as errored when most of its runs errored.
	for _, p := range set {
		for key, c := range p.cases {
			if key.rubric == "" {
				c.passes = len(gradedRuns[[2]string{key.provider, key.test}])
			}
		}
	}
	return set
}

// loadResultDir reads every evals.jsonl in dir or its immediate
// subdirectories (the layout download-artifact produces). It returns nil if
// the directory does not exist or contains no results.
func loadResultDir(dir string) (resultSet, error) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil
	}

	var files []string
	for _, pattern := range []string{"*.jsonl", "*/*.jsonl"} {
		matches, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil {
			return nil, err
		}
		files = append(files, matches...)
	}
	if len(files) == 0 {
		return nil, nil
	}

	var records []EvalLogLine
	for _, file := range files {
		loaded, err := loadLines(file)
		if err != nil {
			return nil, err
		}
		records = append(records, loaded...)
	}
	return buildResultSet(records), nil
}

type report struct {
	providers []*providerResults
	// regressions fail on this change and did not fail on the baseline.
	regressions []*caseStats
	preExisting []*caseStats
	// unbaselined fail for a provider that has no usable baseline while
	// others do, so whether they are new is unknown.
	unbaselined []*caseStats
	flaky       []*caseStats
	baselines   map[string]*providerResults
	hasBaseline bool
	labels      reportLabels
	// commit and runURL identify the run the comment reports on.
	commit string
	runURL string
}

// reportLabels names what is compared, for example "this PR" against
// "`master`", or "this run" against "the previous night".
type reportLabels struct {
	change   string
	baseline string
}

var pullRequestLabels = reportLabels{change: "this PR", baseline: "`master`"}

func buildReport(results, baseline resultSet, expectedProviders []string) report {
	names := slices.Clone(expectedProviders)
	for name := range results {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)

	r := report{baselines: map[string]*providerResults{}, labels: pullRequestLabels}
	for _, b := range baseline {
		if !b.unavailable() {
			r.hasBaseline = true
		}
	}
	for _, name := range names {
		p := results.provider(name)
		r.providers = append(r.providers, p)
		if p.unavailable() {
			continue
		}

		var base *providerResults
		if baseline != nil {
			if b, ok := baseline[name]; ok && !b.unavailable() {
				base = b
				r.baselines[name] = b
			}
		}

		for _, c := range p.sortedCases() {
			switch {
			case c.failing() && base != nil && failedOnBaseline(base, c.caseKey):
				r.preExisting = append(r.preExisting, c)
			case c.failing() && base == nil && r.hasBaseline:
				r.unbaselined = append(r.unbaselined, c)
			case c.failing():
				r.regressions = append(r.regressions, c)
			case c.flaky():
				r.flaky = append(r.flaky, c)
			}
		}
	}
	return r
}

func failedOnBaseline(base *providerResults, key caseKey) bool {
	c, ok := base.cases[key]
	return ok && c.failing()
}

// commentResolved starts the body of a comment whose failures are gone.
const commentResolved = "✅ No eval regressions"

// commentChecksPattern matches the hidden line that lists the failing checks
// a comment reports, base64-encoded JSON so no check text can end the HTML
// comment early.
var commentChecksPattern = regexp.MustCompile(`<!-- eval-checks: ([A-Za-z0-9+/=]*) -->`)

// commentMarkdown renders the pull request comment, given the eval comment
// already on the pull request ("" if none). An empty result leaves that
// comment as it is.
//
// A previous comment's check is dropped only on evidence that it is not this
// change's problem: its eval ran and the check passes or also fails on the
// baseline, or the eval no longer exists. Every other check stays listed, and
// the comment is resolved only when nothing is left.
func (r report) commentMarkdown(previous string) string {
	unchecked, unbaselined := r.trackedChecks(previousChecks(previous))

	if len(r.regressions) == 0 && len(unchecked) == 0 && len(unbaselined) == 0 {
		if previous == "" || strings.Contains(previous, commentResolved) {
			return ""
		}
		resolved := commentMarker + "\n\n" + commentResolved
		if r.commit != "" {
			resolved += " as of " + r.commit[:min(7, len(r.commit))]
		}
		return resolved + ".\n"
	}

	var sb strings.Builder
	sb.WriteString(commentMarker + "\n\n")
	checks := make([]caseKey, 0, len(r.regressions)+len(unchecked))
	for _, c := range r.regressions {
		checks = append(checks, c.caseKey)
	}
	sb.WriteString(checksLine(slices.Concat(checks, unchecked, unbaselined)) + "\n")
	r.writeRunLink(&sb)
	if len(r.regressions) > 0 {
		r.writeRegressions(&sb)
	}
	writeTrackedChecks(&sb, "Not rechecked in this run", "These checks failed in an earlier run and their eval has no result in this one (the provider was unavailable or the eval did not run), so they still count as failing.", unchecked)
	writeTrackedChecks(&sb, "Still failing, with nothing to compare against", "These checks failed in an earlier run and still fail. The baseline has no results for their provider, so they cannot be compared with it.", unbaselined)
	sb.WriteString("<sub>Only checks that newly fail are listed. Pre-existing failures, flaky checks, and unavailable providers are in the workflow run summary.</sub>\n")
	return sb.String()
}

func checksLine(checks []caseKey) string {
	encoded := make([][3]string, len(checks))
	for i, c := range checks {
		encoded[i] = [3]string{c.provider, c.test, c.rubric}
	}
	data, _ := json.Marshal(encoded)
	return "<!-- eval-checks: " + base64.StdEncoding.EncodeToString(data) + " -->"
}

// previousChecks returns the checks a previous comment reports. Comments
// from before checks were tracked report none.
func previousChecks(comment string) []caseKey {
	m := commentChecksPattern.FindStringSubmatch(comment)
	if m == nil {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(m[1])
	if err != nil {
		return nil
	}
	var decoded [][3]string
	if json.Unmarshal(data, &decoded) != nil {
		return nil
	}
	checks := make([]caseKey, len(decoded))
	for i, c := range decoded {
		checks[i] = caseKey{provider: c[0], test: c[1], rubric: c[2]}
	}
	return checks
}

// trackedChecks sorts a previous comment's checks that stay open. unchecked
// are those whose eval has no result in a run that may not have run it (the
// provider was unavailable, or a package did not finish); in a complete run
// the eval was removed, which closes them. unbaselined are this run's failures
// of an eval that ran, for a provider the baseline has no results for. Any
// result for the eval counts as rechecking it, since a check's key changes
// when it errored before or its rubric was edited.
func (r report) trackedChecks(checks []caseKey) (unchecked, unbaselined []caseKey) {
	for _, key := range checks {
		i := slices.IndexFunc(r.providers, func(p *providerResults) bool { return p.name == key.provider })
		var p *providerResults
		if i >= 0 && !r.providers[i].unavailable() {
			p = r.providers[i]
		}
		switch {
		case p != nil && p.ranEval(key.test):
			// The check itself if it has a result; otherwise (it errored before,
			// or its rubric was edited) the eval's current failures stand in for it.
			_, exact := p.cases[key]
			for _, c := range r.unbaselined {
				matches := c.caseKey == key || (!exact && c.provider == key.provider && c.test == key.test)
				if matches && !slices.Contains(unbaselined, c.caseKey) {
					unbaselined = append(unbaselined, c.caseKey)
				}
			}
		case p != nil && p.complete:
		case !slices.Contains(unchecked, key):
			unchecked = append(unchecked, key)
		}
	}
	return unchecked, unbaselined
}

func writeTrackedChecks(sb *strings.Builder, title, explanation string, checks []caseKey) {
	if len(checks) == 0 {
		return
	}
	fmt.Fprintf(sb, "### %s\n\n%s\n\n", title, explanation)
	sb.WriteString("| Eval | Check | Provider |\n")
	sb.WriteString("|------|-------|----------|\n")
	for _, c := range checks {
		check := c.rubric
		if check == "" {
			check = "errored before grading"
		}
		fmt.Fprintf(sb, "| %s | %s | %s |\n", tableCell(displayTestName(c.test)), tableCell(check), providerDisplayName(c.provider))
	}
	sb.WriteString("\n")
}

// writeRunLink says which commit the comment reports on and links the full
// run summary, since the comment is edited in place across pushes.
func (r report) writeRunLink(sb *strings.Builder) {
	var parts []string
	if r.commit != "" {
		parts = append(parts, fmt.Sprintf("Results for `%s`", r.commit[:min(7, len(r.commit))]))
	}
	if r.runURL != "" {
		parts = append(parts, fmt.Sprintf("[full run summary](%s)", r.runURL))
	}
	if len(parts) > 0 {
		sb.WriteString(strings.Join(parts, " · ") + "\n\n")
	}
}

func (r report) writeRegressions(sb *strings.Builder) {
	if r.hasBaseline {
		fmt.Fprintf(sb, "⚠️ **%d eval %s on %s but not on %s.**\n\n", len(r.regressions), plural(len(r.regressions), "check fails", "checks fail"), r.labels.change, r.labels.baseline)
	} else {
		fmt.Fprintf(sb, "⚠️ **%d eval %s on %s.** No baseline from %s was available, so these may be pre-existing.\n\n", len(r.regressions), plural(len(r.regressions), "check fails", "checks fail"), r.labels.change, r.labels.baseline)
	}

	sb.WriteString("| Eval | Check | Provider | Passed runs |\n")
	sb.WriteString("|------|-------|----------|-------------|\n")
	for _, c := range r.regressions {
		fmt.Fprintf(sb, "| %s | %s | %s | %d/%d |\n",
			tableCell(displayTestName(c.test)), tableCell(checkLabel(c)), providerDisplayName(c.provider), c.passes, c.runs())
	}

	sb.WriteString("\n<details>\n<summary>Grader reasoning, agent output, and how to reproduce</summary>\n")
	for _, group := range groupByEval(r.regressions) {
		first := group[0]
		fmt.Fprintf(sb, "\n#### %s (%s)\n\n", displayTestName(first.test), providerDisplayName(first.provider))
		for _, c := range group {
			if c.errored() {
				fmt.Fprintf(sb, "- **Errored before grading:** %s\n", truncateString(oneLine(c.sample.Error), 500))
				continue
			}
			fmt.Fprintf(sb, "- **%s** — %s\n", oneLine(c.rubric), truncateString(oneLine(c.sample.Reasoning), 500))
		}
		if output := latestOutput(group); output != "" {
			sb.WriteString("\n<details>\n<summary>Agent output</summary>\n\n````text\n")
			sb.WriteString(truncateString(output, 3000))
			sb.WriteString("\n````\n\n</details>\n")
		}
		fmt.Fprintf(sb, "\nReproduce: `%s`\n", reproduceCommand(first))
	}
	sb.WriteString("\n</details>\n\n")
}

// summaryMarkdown renders the full results for the workflow run summary.
func (r report) summaryMarkdown() string {
	var sb strings.Builder
	sb.WriteString("## LLM evaluation results\n\n")
	sb.WriteString("| Provider | Status | Checks passed |\n")
	sb.WriteString("|----------|--------|---------------|\n")
	for _, p := range r.providers {
		if p.unavailable() {
			fmt.Fprintf(&sb, "| %s | ⚪ Unavailable: %s | – |\n", providerDisplayName(p.name), tableCell(truncateString(oneLine(p.firstError()), 200)))
			continue
		}
		passing, total := 0, 0
		for _, c := range p.cases {
			total++
			if !c.failing() {
				passing++
			}
		}
		status := "✅ Passing"
		if passing < total {
			status = "⚠️ Failing"
		}
		fmt.Fprintf(&sb, "| %s | %s | %d/%d |\n", providerDisplayName(p.name), status, passing, total)
	}

	writeCaseList(&sb, "Regressions", r.regressions)
	writeCaseList(&sb, "Pre-existing failures (also failing on "+r.labels.baseline+")", r.preExisting)
	writeCaseList(&sb, "Failures without a baseline for their provider on "+r.labels.baseline, r.unbaselined)
	writeCaseList(&sb, "Flaky (failed some runs, passed the majority)", r.flaky)
	if !r.hasBaseline {
		fmt.Fprintf(&sb, "\nNo baseline from %s was available for comparison.\n", r.labels.baseline)
	}
	return sb.String()
}

// annotations returns GitHub workflow commands that flag unavailable
// providers on the run without adding noise to the pull request.
func (r report) annotations() []string {
	var lines []string
	for _, p := range r.providers {
		if p.unavailable() {
			lines = append(lines, fmt.Sprintf("::warning title=Evals unavailable for %s::%s",
				providerDisplayName(p.name), escapeWorkflowCommand(truncateString(oneLine(p.firstError()), 500))))
		}
	}
	return lines
}

func writeCaseList(sb *strings.Builder, title string, cases []*caseStats) {
	if len(cases) == 0 {
		return
	}
	fmt.Fprintf(sb, "\n### %s\n\n", title)
	for _, c := range cases {
		fmt.Fprintf(sb, "- %s — %s (%s, %d/%d passed)\n", displayTestName(c.test), oneLine(checkLabel(c)), providerDisplayName(c.provider), c.passes, c.runs())
	}
}

// groupByEval groups cases of the same eval and provider, preserving order.
func groupByEval(cases []*caseStats) [][]*caseStats {
	var groups [][]*caseStats
	index := map[[2]string]int{}
	for _, c := range cases {
		key := [2]string{c.provider, c.test}
		if i, ok := index[key]; ok {
			groups[i] = append(groups[i], c)
			continue
		}
		index[key] = len(groups)
		groups = append(groups, []*caseStats{c})
	}
	return groups
}

func latestOutput(group []*caseStats) string {
	for _, c := range group {
		if c.sample.Output != "" {
			return c.sample.Output
		}
	}
	return ""
}

func checkLabel(c *caseStats) string {
	if c.errored() {
		return "errored before grading"
	}
	return c.rubric
}

// displayTestName turns "TestReactEval/react_cat_message" into
// "TestReactEval › react cat message".
func displayTestName(test string) string {
	segments := strings.Split(test, "/")
	for i := 1; i < len(segments); i++ {
		segments[i] = strings.ReplaceAll(segments[i], "_", " ")
	}
	return strings.Join(segments, " › ")
}

func reproduceCommand(c *caseStats) string {
	anchored := make([]string, len(c.segments))
	for i, segment := range c.segments {
		anchored[i] = "^" + regexp.QuoteMeta(segment) + "$"
	}
	pattern := strings.Join(anchored, "/")
	pkg := "./..."
	if c.pkg != "" {
		pkg = "./" + c.pkg
	}
	return fmt.Sprintf("GOEVALS=1 LLM_PROVIDER=%s go test %s -run %s -v", shellQuote(c.provider), shellQuote(pkg), shellQuote(pattern))
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)

// shellQuote makes s a single POSIX shell word. Values come from evals.jsonl,
// which the change under test writes, so none may be pasted unquoted.
func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func tableCell(s string) string {
	return strings.ReplaceAll(oneLine(s), "|", `\|`)
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func escapeWorkflowCommand(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(s)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
