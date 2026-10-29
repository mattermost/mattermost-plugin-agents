// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// EvalLogLine matches the structure from evals/record.go
type EvalLogLine struct {
	Name      string  `json:"name"`
	Package   string  `json:"package"`
	Timestamp string  `json:"timestamp"`
	RunNumber int     `json:"run_number"`
	Rubric    string  `json:"rubric"`
	Output    string  `json:"output"`
	Reasoning string  `json:"reasoning"`
	Score     float64 `json:"score"`
	Pass      bool    `json:"pass"`
	Error     string  `json:"error"`
	// Complete is set only on the line `evalviewer check` appends once every
	// eval package finished. loadLines drops that line and sets RunComplete
	// on the file's other lines instead.
	Complete    bool `json:"complete,omitempty"`
	RunComplete bool `json:"-"`
}

var (
	// Flags for view command
	filename         string
	showOnlyFailures bool

	// Flags for report command
	reportResultsDir  string
	reportBaselineDir string
	reportProviders   []string
	reportCommentFile string
	reportSummaryFile string
	reportJSONFile    string
	reportCommit      string
	reportRunURL      string
	reportPrevious    string
	reportChangeLabel string
	reportBaseLabel   string

	// Flags for trend command
	trendHistoryDir string
	trendLimit      int
)

func main() {
	var rootCmd = &cobra.Command{
		Use:   "evalviewer",
		Short: "Display evaluation results from evals.jsonl",
		Long: `evalviewer is a CLI tool to run evaluations and display results in a nice table format.

It can either run tests and display results, or view existing evaluation results.`,
	}

	var runCmd = &cobra.Command{
		Use:   "run [go test flags and args]",
		Short: "Run eval tests and display results",
		Long: `Run go test with GOEVALS=1 environment variable set, then automatically
find and display the evaluation results in a TUI.

All arguments after 'run' are passed directly to 'go test'.`,
		Example: `  evalviewer run -v ./conversations         # Run evals for conversations package
  evalviewer run -v ./...                   # Run all evals
  evalviewer run -v -cover ./conversations  # Run with test coverage`,
		DisableFlagParsing: true,
		Run: func(cmd *cobra.Command, args []string) {
			runCommand(args)
		},
	}

	var viewCmd = &cobra.Command{
		Use:   "view",
		Short: "Display existing evaluation results",
		Long:  `Display evaluation results from an existing evals.jsonl file in a TUI.`,
		Example: `  evalviewer view -file evals.jsonl         # View existing results
  evalviewer view -failures-only            # Show only failures`,
		Run: func(cmd *cobra.Command, args []string) {
			viewCommandWithFlags()
		},
	}

	var checkCmd = &cobra.Command{
		Use:   "check [go test flags and args]",
		Short: "Run eval tests and check results (CI-friendly, no TUI)",
		Long: `Run go test with GOEVALS=1 environment variable set, then check the results
and exit with status code 1 if any evaluations failed. This command is designed
for CI/CD pipelines and does not use the interactive TUI.

A package that did not run all of its evals (it failed to build, panicked, timed
out, or a test failed without recording a result) always fails the command. With
EVALS_REPORT_ONLY set, failed evals do not, so 'evalviewer report' can present
them.

All arguments after 'check' are passed directly to 'go test'.`,
		Example: `  evalviewer check ./conversations           # Run and check evals for conversations
  evalviewer check ./...                     # Run and check all evals
  evalviewer check -v -cover ./...           # Run with verbose output and coverage`,
		DisableFlagParsing: true,
		Run: func(cmd *cobra.Command, args []string) {
			checkCommand(args)
		},
	}

	var reportCmd = &cobra.Command{
		Use:   "report",
		Short: "Summarize eval results for a pull request (CI-friendly)",
		Long: `Aggregate evals.jsonl files from one or more providers, compare them with a
baseline from the target branch, and write:

  - a pull request comment listing only checks that newly fail, or resolving
    the previous comment once they are fixed (empty when the previous comment
    should be left as it is), and
  - a full summary for the workflow run, including pre-existing failures,
    flaky checks, and providers that could not run.

Repeated runs (GOEVALS=N) are combined by majority vote. Results are read from
evals.jsonl files in the directory or its immediate subdirectories.`,
		Example: `  evalviewer report --results eval-results --baseline eval-baseline --providers openai,anthropic
  evalviewer report --results . --comment comment.md`,
		Run: func(cmd *cobra.Command, args []string) {
			reportCommand()
		},
	}

	var trendCmd = &cobra.Command{
		Use:   "trend",
		Short: "Render eval trends from past report --json summaries",
		Long: `Read every summary.json under a directory (for example, nightly summaries
downloaded from S3) and render the pass rate per provider across runs, newest
first.`,
		Example: `  evalviewer trend --history eval-history --limit 14`,
		Run: func(cmd *cobra.Command, args []string) {
			trendCommand()
		},
	}

	// Add flags to view command
	viewCmd.Flags().StringVarP(&filename, "file", "f", "evals.jsonl", "Path to the evals.jsonl file")
	viewCmd.Flags().BoolVar(&showOnlyFailures, "failures-only", false, "Show only failed evaluations")

	reportCmd.Flags().StringVar(&reportResultsDir, "results", ".", "Directory containing evals.jsonl results")
	reportCmd.Flags().StringVar(&reportBaselineDir, "baseline", "", "Directory containing baseline evals.jsonl results from the target branch")
	reportCmd.Flags().StringSliceVar(&reportProviders, "providers", nil, "Providers expected to have results; missing ones are reported as unavailable")
	reportCmd.Flags().StringVar(&reportCommentFile, "comment", "comment.md", "Where to write the pull request comment (empty when the existing comment should be left as it is)")
	reportCmd.Flags().StringVar(&reportPrevious, "previous", "", "File holding the eval comment already on the pull request, so the new comment keeps its checks that were not rechecked or resolves it")
	reportCmd.Flags().StringVar(&reportSummaryFile, "summary", "summary.md", "Where to write the workflow run summary")
	reportCmd.Flags().StringVar(&reportJSONFile, "json", "", "Where to write a machine-readable summary for history (optional)")
	reportCmd.Flags().StringVar(&reportCommit, "commit", "", "Commit the results belong to, shown in the comment and recorded in the JSON summary")
	reportCmd.Flags().StringVar(&reportRunURL, "run-url", "", "Workflow run URL, linked from the comment and recorded in the JSON summary")
	reportCmd.Flags().StringVar(&reportChangeLabel, "change-label", pullRequestLabels.change, "How the report refers to the results being checked")
	reportCmd.Flags().StringVar(&reportBaseLabel, "baseline-label", pullRequestLabels.baseline, "How the report refers to the baseline")

	trendCmd.Flags().StringVar(&trendHistoryDir, "history", ".", "Directory containing summary.json files")
	trendCmd.Flags().IntVar(&trendLimit, "limit", 14, "Number of most recent runs to show (0 for all)")

	// Add commands to root
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(viewCmd)
	rootCmd.AddCommand(checkCmd)
	rootCmd.AddCommand(reportCmd)
	rootCmd.AddCommand(trendCmd)

	// Execute
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// evalRun is the outcome of `go test` over the eval packages.
type evalRun struct {
	// err is the error from `go test`, which also fails when evals do.
	err        error
	incomplete []incompletePackage
}

// broken reports that evals did not all run, as opposed to some failing:
// packages did not finish, or `go test` could not run at all or was killed.
// Exit status 1 is the only one `go test` uses for failing tests.
func (r evalRun) broken() bool {
	var exitErr *exec.ExitError
	return len(r.incomplete) > 0 || (r.err != nil && !(errors.As(r.err, &exitErr) && exitErr.ExitCode() == 1))
}

// reportOnlyEnv makes check exit non-zero only when evals did not all run,
// leaving failed evals to `evalviewer report`.
const reportOnlyEnv = "EVALS_REPORT_ONLY"

// runEvals cleans up stale results, then runs `go test` with GOEVALS=1 set,
// streaming output to the terminal, so each command can apply its own
// exit-code policy.
func runEvals(args []string) evalRun {
	if err := cleanupOldResults(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to clean old results: %v\n", err)
	}

	fmt.Println("Running evaluations...")

	// Unfinished tests are detected from the `=== RUN` lines, which only -v prints.
	if !slices.ContainsFunc(args, func(a string) bool { return a == "-v" || strings.HasPrefix(a, "-v=") || a == "-test.v" }) {
		args = append([]string{"-v"}, args...)
	}
	cmd := exec.Command("go", append([]string{"test"}, args...)...)
	cmd.Env = os.Environ()
	if os.Getenv("GOEVALS") == "" {
		cmd.Env = append(cmd.Env, "GOEVALS=1")
	}
	var watcher packageWatcher
	cmd.Stdout = io.MultiWriter(os.Stdout, &watcher)
	cmd.Stderr = os.Stderr
	run := evalRun{err: cmd.Run(), incomplete: watcher.incompletePackages()}

	var records []EvalLogLine
	evalFile, findErr := findEvalsFile()
	if findErr == nil {
		records, _ = loadLines(evalFile)
	}
	run.incomplete = append(run.incomplete, watcher.unrecordedFailures(records, goModulePath())...)
	reportIncomplete(run.incomplete)
	if findErr == nil && !run.broken() {
		if err := markComplete(evalFile); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to mark results complete: %v\n", err)
		}
	}
	return run
}

// markComplete appends the line that says every eval package finished. The
// report treats a check whose eval has no result as removed only in a
// complete run; in any other run the eval may simply not have run.
func markComplete(evalFile string) error {
	f, err := os.OpenFile(evalFile, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(f).Encode(EvalLogLine{Complete: true}); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func runCommand(args []string) {
	if run := runEvals(args); run.err != nil {
		fmt.Printf("\nTests completed with errors: %v\n", run.err)
	} else {
		fmt.Println("\nTests completed successfully.")
	}

	// Find and display results
	evalFile, err := findEvalsFile()
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError finding evals.jsonl: %v\n", err)
		fmt.Println("You can view results manually with: evalviewer view -file /path/to/evals.jsonl")
		os.Exit(1)
	}

	// Display results with default settings
	results, err := loadResults(evalFile, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading results: %v\n", err)
		os.Exit(1)
	}

	// Launch TUI
	if err := runTUI(results); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
}

func viewCommandWithFlags() {
	results, err := loadResults(filename, false) // Don't pre-filter, let TUI handle it
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading results: %v\n", err)
		os.Exit(1)
	}

	// Launch TUI
	if err := runTUI(results); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
}

func checkCommand(args []string) {
	run := runEvals(args)
	if run.err != nil {
		fmt.Printf("\nTests completed with errors: %v\n", run.err)
	} else {
		fmt.Println("\nTests completed successfully.")
	}

	hasFailures := false
	evalFile, err := findEvalsFile()
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError finding evals.jsonl: %v\n", err)
		fmt.Println("No evaluation results found to check.")
	} else {
		results, err := loadResults(evalFile, false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading results: %v\n", err)
			os.Exit(1)
		}
		printSummary(results)
		hasFailures = slices.ContainsFunc(results, func(result EvalLogLine) bool { return !result.Pass })
	}

	switch {
	case run.broken():
		os.Exit(1)
	case os.Getenv(reportOnlyEnv) != "":
		return
	case hasFailures || run.err != nil:
		os.Exit(1)
	}
}

func reportCommand() {
	results, err := loadResultDir(reportResultsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading results: %v\n", err)
		os.Exit(1)
	}
	if results == nil {
		results = resultSet{}
	}

	var baseline resultSet
	if reportBaselineDir != "" {
		baseline, err = loadResultDir(reportBaselineDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading baseline: %v\n", err)
			os.Exit(1)
		}
	}

	r := buildReport(results, baseline, reportProviders)
	r.labels = reportLabels{change: reportChangeLabel, baseline: reportBaseLabel}
	r.commit = reportCommit
	r.runURL = reportRunURL

	var previous string
	if reportPrevious != "" {
		data, err := os.ReadFile(reportPrevious)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", reportPrevious, err)
			os.Exit(1)
		}
		previous = string(data)
	}
	comment := r.commentMarkdown(previous)
	if err := os.WriteFile(reportCommentFile, []byte(comment), 0600); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", reportCommentFile, err)
		os.Exit(1)
	}
	summary := r.summaryMarkdown()
	if err := os.WriteFile(reportSummaryFile, []byte(summary), 0600); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", reportSummaryFile, err)
		os.Exit(1)
	}

	if reportJSONFile != "" {
		data, err := json.MarshalIndent(r.runSummary(time.Now(), reportCommit, reportRunURL), "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding summary: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(reportJSONFile, data, 0600); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", reportJSONFile, err)
			os.Exit(1)
		}
	}

	if os.Getenv("GITHUB_ACTIONS") == "true" {
		for _, line := range r.annotations() {
			fmt.Println(line)
		}
	}
	fmt.Print(summary)
	if comment == "" {
		fmt.Println("\nNothing to change in the pull request comment; comment file left empty.")
	} else {
		fmt.Printf("\nPull request comment written to %s (%d regressions)\n", reportCommentFile, len(r.regressions))
	}
}

func trendCommand() {
	history, err := loadHistory(trendHistoryDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading history: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(trendMarkdown(history, trendLimit))
}

// providerStat accumulates per-provider pass/fail counts and failed results.
type providerStat struct {
	passed   int
	failed   int
	failures []EvalLogLine
}

// groupByProvider buckets results by the "[provider]" tag in the test name
// (format: "ParentTest/[provider] test name") and returns per-provider stats
// along with the overall passed and failed totals.
func groupByProvider(results []EvalLogLine) (map[string]providerStat, int, int) {
	providerStats := make(map[string]providerStat)
	totalPassed := 0
	totalFailed := 0

	for _, result := range results {
		provider := "unknown"
		if _, after, ok := strings.Cut(result.Name, "["); ok {
			if p, _, found := strings.Cut(after, "]"); found {
				provider = p
			}
		}

		stats := providerStats[provider]
		if result.Pass {
			stats.passed++
			totalPassed++
		} else {
			stats.failed++
			totalFailed++
			stats.failures = append(stats.failures, result)
		}
		providerStats[provider] = stats
	}

	return providerStats, totalPassed, totalFailed
}

func printSummary(results []EvalLogLine) {
	if len(results) == 0 {
		fmt.Println("\nNo evaluation results found.")
		return
	}

	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("EVALUATION RESULTS SUMMARY")
	fmt.Println(strings.Repeat("=", 80))

	providerStats, totalPassed, totalFailed := groupByProvider(results)

	// Print per-provider statistics
	fmt.Println("\nResults by Provider:")
	fmt.Println(strings.Repeat("-", 80))
	for provider, stats := range providerStats {
		total := stats.passed + stats.failed
		fmt.Printf("  %s: %d tests (%d passed, %d failed)\n",
			strings.ToUpper(provider), total, stats.passed, stats.failed)
	}

	// Print overall statistics
	fmt.Println("\nOverall Statistics:")
	fmt.Println(strings.Repeat("-", 80))
	fmt.Printf("Total Tests: %d\n", len(results))
	fmt.Printf("Passed:      %d\n", totalPassed)
	fmt.Printf("Failed:      %d\n", totalFailed)

	// Print failures grouped by provider
	if totalFailed > 0 {
		fmt.Println("\n" + strings.Repeat("-", 80))
		fmt.Println("FAILED EVALUATIONS:")
		fmt.Println(strings.Repeat("-", 80))

		failureNum := 1
		for provider, stats := range providerStats {
			if len(stats.failures) > 0 {
				fmt.Printf("\n%s Failures:\n", strings.ToUpper(provider))
				for _, failure := range stats.failures {
					fmt.Printf("\n%d. %s\n", failureNum, failure.Name)
					if failure.Error != "" {
						fmt.Printf("   Error:  %s\n", truncateString(failure.Error, 250))
						failureNum++
						continue
					}
					fmt.Printf("   Rubric: %s\n", truncateString(failure.Rubric, 70))
					fmt.Printf("   Score:  %.2f\n", failure.Score)
					if failure.Reasoning != "" {
						fmt.Printf("   Reason: %s\n", truncateString(failure.Reasoning, 250))
					}
					failureNum++
				}
			}
		}
	}

	fmt.Println("\n" + strings.Repeat("=", 80))
}

func truncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-3]) + "..."
}

var errEvalsFileNotFound = errors.New("evals.jsonl not found in current directory or parent directories")

func cleanupOldResults() error {
	evalFile, err := findEvalsFile()
	if errors.Is(err, errEvalsFileNotFound) {
		// No file found, nothing to clean up
		return nil
	}
	if err != nil {
		return err
	}

	fmt.Printf("Cleaning up old results: %s\n", evalFile)
	return os.Remove(evalFile)
}

// findEvalsFile looks for evals.jsonl in the current directory and parent
// directories, returning errEvalsFileNotFound if the walk reaches the root.
func findEvalsFile() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		evalFile := filepath.Join(dir, "evals.jsonl")
		if _, err := os.Stat(evalFile); err == nil {
			return evalFile, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root
			return "", errEvalsFileNotFound
		}
		dir = parent
	}
}

// loadResults loads graded and errored results.
func loadResults(filename string, showOnlyFailures bool) ([]EvalLogLine, error) {
	lines, err := loadLines(filename)
	if err != nil {
		return nil, err
	}

	var results []EvalLogLine
	for _, result := range lines {
		if showOnlyFailures && result.Pass {
			continue
		}
		results = append(results, result)
	}
	return results, nil
}

// loadLines loads every result line of an evals.jsonl file.
func loadLines(filename string) ([]EvalLogLine, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("error opening file %s: %v", filename, err)
	}
	defer file.Close()

	var results []EvalLogLine
	complete := false
	scanner := bufio.NewScanner(file)
	// Lines carry full agent outputs, which can exceed the default 64KB limit.
	scanner.Buffer(make([]byte, 0, 1024*1024), 64*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var result EvalLogLine
		if err := json.Unmarshal([]byte(line), &result); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing line: %v\n", err)
			continue
		}
		if result.Complete {
			complete = true
			continue
		}
		results = append(results, result)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading file: %v", err)
	}

	for i := range results {
		results[i].RunComplete = complete
	}
	return results, nil
}
