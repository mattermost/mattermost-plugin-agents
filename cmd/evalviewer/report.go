// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"cmp"
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
	// usage is keyed by provider-independent test name.
	usage map[string]*usageStats
}

// usageStats sums the per-run usage of one eval for a provider.
type usageStats struct {
	test   string
	runs   int
	agent  RunUsage
	grader RunUsage
}

func (u *usageStats) add(record UsageRecord) {
	u.runs++
	addUsage(&u.agent, record.Agent)
	addUsage(&u.grader, record.Grader)
}

func addUsage(total *RunUsage, run RunUsage) {
	total.Model = cmp.Or(total.Model, run.Model)
	total.Calls += run.Calls
	total.InputTokens += run.InputTokens
	total.OutputTokens += run.OutputTokens
	total.CachedReadTokens += run.CachedReadTokens
	total.ReasoningTokens += run.ReasoningTokens
	total.CostUSD += run.CostUSD
	total.LatencyMS += run.LatencyMS
}

func (u *usageStats) mean(v float64) float64 {
	return v / float64(u.runs)
}

func (p *providerResults) usageTotals() usageStats {
	var total usageStats
	for _, u := range p.usage {
		total.runs += u.runs
		addUsage(&total.agent, u.agent)
		addUsage(&total.grader, u.grader)
	}
	return total
}

func (p *providerResults) sortedUsage() []*usageStats {
	usage := make([]*usageStats, 0, len(p.usage))
	for _, u := range p.usage {
		usage = append(usage, u)
	}
	slices.SortFunc(usage, func(a, b *usageStats) int { return cmp.Compare(a.test, b.test) })
	return usage
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
	p := &providerResults{name: name, cases: map[caseKey]*caseStats{}, usage: map[string]*usageStats{}}
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
		if record.Usage != nil {
			u, ok := p.usage[test]
			if !ok {
				u = &usageStats{test: test}
				p.usage[test] = u
			}
			u.add(*record.Usage)
			continue
		}
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
	// tokenGrowth lists evals whose input tokens grew past the thresholds.
	tokenGrowth []tokenChange
	baselines   map[string]*providerResults
	hasBaseline bool
	labels      reportLabels
}

// reportLabels names what is compared, for example "this PR" against
// "`master`", or "this run" against "the previous night".
type reportLabels struct {
	change   string
	baseline string
}

var pullRequestLabels = reportLabels{change: "this PR", baseline: "`master`"}

// Input token growth is reported on the pull request only when it is both
// relative and absolute: prompt size is close to deterministic, so growth this
// large reflects a change in what the agent is sent rather than run noise.
const (
	tokenGrowthRatio     = 0.20
	tokenGrowthMinTokens = 1000
)

type tokenChange struct {
	provider string
	test     string
	base     float64
	current  float64
}

func (c tokenChange) ratio() float64 {
	return (c.current - c.base) / c.base
}

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

		if base != nil {
			for _, u := range p.sortedUsage() {
				b, ok := base.usage[u.test]
				if !ok || b.agent.InputTokens == 0 {
					continue
				}
				change := tokenChange{
					provider: name,
					test:     u.test,
					base:     b.mean(float64(b.agent.InputTokens)),
					current:  u.mean(float64(u.agent.InputTokens)),
				}
				if change.current-change.base >= tokenGrowthMinTokens && change.ratio() >= tokenGrowthRatio {
					r.tokenGrowth = append(r.tokenGrowth, change)
				}
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

// commentMarkdown renders the pull request comment. It is empty when there is
// nothing for the author to act on.
func (r report) commentMarkdown() string {
	if len(r.regressions) == 0 && len(r.tokenGrowth) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(commentMarker + "\n\n")
	if len(r.regressions) > 0 {
		r.writeRegressions(&sb)
	}
	if len(r.tokenGrowth) > 0 {
		r.writeTokenGrowth(&sb)
	}
	sb.WriteString("<sub>Only checks that newly fail and input token growth over the thresholds are listed. Pre-existing failures, flaky checks, cost, latency, and unavailable providers are in the workflow run summary.</sub>\n")
	return sb.String()
}

func (r report) writeTokenGrowth(sb *strings.Builder) {
	fmt.Fprintf(sb, "📈 **Input tokens grew by at least %.0f%% (and %d tokens) for %d %s compared to %s.** This usually means more context or a longer prompt is being sent to the model.\n\n",
		tokenGrowthRatio*100, tokenGrowthMinTokens, len(r.tokenGrowth), plural(len(r.tokenGrowth), "eval", "evals"), r.labels.baseline)
	fmt.Fprintf(sb, "| Eval | Provider | Input tokens per run (%s → %s) | Change |\n", r.labels.baseline, r.labels.change)
	sb.WriteString("|------|----------|------------------------------------|--------|\n")
	for _, c := range r.tokenGrowth {
		fmt.Fprintf(sb, "| %s | %s | %s → %s | +%.0f%% |\n",
			tableCell(displayTestName(c.test)), providerDisplayName(c.provider), formatTokens(c.base), formatTokens(c.current), c.ratio()*100)
	}
	sb.WriteString("\n")
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

	r.writeUsage(&sb)

	writeCaseList(&sb, "Regressions", r.regressions)
	writeCaseList(&sb, "Pre-existing failures (also failing on "+r.labels.baseline+")", r.preExisting)
	writeCaseList(&sb, "Failures without a baseline for their provider on "+r.labels.baseline, r.unbaselined)
	writeCaseList(&sb, "Flaky (failed some runs, passed the majority)", r.flaky)
	if !r.hasBaseline {
		fmt.Fprintf(&sb, "\nNo baseline from %s was available for comparison.\n", r.labels.baseline)
	}
	return sb.String()
}

// writeUsage renders token usage, cost, and latency per provider, and per eval
// in a collapsed table.
func (r report) writeUsage(sb *strings.Builder) {
	var providers []*providerResults
	for _, p := range r.providers {
		if len(p.usage) > 0 {
			providers = append(providers, p)
		}
	}
	if len(providers) == 0 {
		return
	}

	sb.WriteString("\n### Cost and latency\n\n")
	fmt.Fprintf(sb, "| Provider | Model | Runs | Input / output tokens | Cost | Grader cost | Latency per run | Cost vs %s |\n", r.labels.baseline)
	sb.WriteString("|----------|-------|------|-----------------------|------|-------------|-----------------|----------------|\n")
	for _, p := range providers {
		total := p.usageTotals()
		vsBase := "–"
		if b, ok := r.baselines[p.name]; ok {
			if baseCost := b.usageTotals(); baseCost.agent.CostUSD > 0 && total.agent.CostUSD > 0 {
				vsBase = formatChange(baseCost.mean(baseCost.agent.CostUSD), total.mean(total.agent.CostUSD))
			}
		}
		fmt.Fprintf(sb, "| %s | %s | %d | %s / %s | %s | %s | %s | %s |\n",
			providerDisplayName(p.name), cmp.Or(total.agent.Model, "–"), total.runs,
			formatTokens(float64(total.agent.InputTokens)), formatTokens(float64(total.agent.OutputTokens)),
			formatCost(total.agent.CostUSD), formatCost(total.grader.CostUSD),
			formatDuration(total.mean(float64(total.agent.LatencyMS))), vsBase)
	}
	sb.WriteString("\nCost is estimated from list prices when the provider does not report it; \"–\" means the model has no known price. Latency is the summed wall time of the agent's LLM calls per run.\n")

	sb.WriteString("\n<details>\n<summary>Per eval</summary>\n\n")
	fmt.Fprintf(sb, "| Eval | Provider | Input tokens | Output tokens | Cost | Latency | Input vs %s | Latency vs %s |\n", r.labels.baseline, r.labels.baseline)
	sb.WriteString("|------|----------|--------------|---------------|------|---------|-----------------|-------------------|\n")
	for _, p := range providers {
		base := r.baselines[p.name]
		for _, u := range p.sortedUsage() {
			inputChange, latencyChange := "–", "–"
			if base != nil {
				if b, ok := base.usage[u.test]; ok {
					inputChange = formatChange(b.mean(float64(b.agent.InputTokens)), u.mean(float64(u.agent.InputTokens)))
					latencyChange = formatChange(b.mean(float64(b.agent.LatencyMS)), u.mean(float64(u.agent.LatencyMS)))
				}
			}
			fmt.Fprintf(sb, "| %s | %s | %s | %s | %s | %s | %s | %s |\n",
				tableCell(displayTestName(u.test)), providerDisplayName(p.name),
				formatTokens(u.mean(float64(u.agent.InputTokens))), formatTokens(u.mean(float64(u.agent.OutputTokens))),
				formatCost(u.mean(u.agent.CostUSD)), formatDuration(u.mean(float64(u.agent.LatencyMS))),
				inputChange, latencyChange)
		}
	}
	sb.WriteString("\nValues are means per run.\n\n</details>\n")
}

func formatTokens(n float64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", n/1_000_000)
	case n >= 10_000:
		return fmt.Sprintf("%.0fk", n/1000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", n/1000)
	default:
		return fmt.Sprintf("%.0f", n)
	}
}

func formatCost(usd float64) string {
	switch {
	case usd == 0:
		return "–"
	case usd < 0.01:
		return fmt.Sprintf("$%.4f", usd)
	default:
		return fmt.Sprintf("$%.2f", usd)
	}
}

func formatDuration(ms float64) string {
	if ms < 1000 {
		return fmt.Sprintf("%.0fms", ms)
	}
	return fmt.Sprintf("%.1fs", ms/1000)
}

func formatChange(base, current float64) string {
	if base == 0 {
		return "–"
	}
	return fmt.Sprintf("%+.0f%%", (current-base)/base*100)
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
