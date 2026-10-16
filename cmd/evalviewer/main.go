// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	// Usage is set on the one line per run that carries token usage, cost,
	// and latency instead of a graded result.
	Usage *UsageRecord `json:"usage"`
}

// RunUsage matches evals.RunUsage.
type RunUsage struct {
	Model            string  `json:"model"`
	Calls            int     `json:"calls"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CachedReadTokens int64   `json:"cached_read_tokens"`
	ReasoningTokens  int64   `json:"reasoning_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	LatencyMS        int64   `json:"latency_ms"`
	FirstTokenMS     int64   `json:"first_token_ms"`
}

// UsageRecord matches evals.UsageRecord.
type UsageRecord struct {
	Agent  RunUsage `json:"agent"`
	Grader RunUsage `json:"grader"`
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

  - a pull request comment listing only checks that newly fail (empty when
    there is nothing to act on), and
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
downloaded from S3) and render pass rate, tokens, cost, and latency per
provider across runs, newest first.`,
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
	reportCmd.Flags().StringVar(&reportCommentFile, "comment", "comment.md", "Where to write the pull request comment (empty when nothing is actionable)")
	reportCmd.Flags().StringVar(&reportSummaryFile, "summary", "summary.md", "Where to write the workflow run summary")
	reportCmd.Flags().StringVar(&reportJSONFile, "json", "", "Where to write a machine-readable summary for history (optional)")
	reportCmd.Flags().StringVar(&reportCommit, "commit", "", "Commit the results belong to, recorded in the JSON summary")
	reportCmd.Flags().StringVar(&reportRunURL, "run-url", "", "Workflow run URL, recorded in the JSON summary")
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

// runEvals cleans up stale results, then runs `go test` with GOEVALS=1 set,
// streaming output to the terminal. It returns the `go test` error so each
// command can apply its own exit-code policy.
func runEvals(args []string) error {
	if err := cleanupOldResults(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to clean old results: %v\n", err)
	}

	fmt.Println("Running evaluations...")

	cmd := exec.Command("go", append([]string{"test"}, args...)...)
	cmd.Env = os.Environ()
	if os.Getenv("GOEVALS") == "" {
		cmd.Env = append(cmd.Env, "GOEVALS=1")
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runCommand(args []string) {
	if err := runEvals(args); err != nil {
		fmt.Printf("\nTests completed with errors: %v\n", err)
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
	testErr := runEvals(args)
	if testErr != nil {
		fmt.Printf("\nTests completed with errors: %v\n", testErr)
	} else {
		fmt.Println("\nTests completed successfully.")
	}

	// Find and check results
	evalFile, err := findEvalsFile()
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError finding evals.jsonl: %v\n", err)
		fmt.Println("No evaluation results found to check.")
		// If tests failed but no evals file, exit with test error
		if testErr != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Load and check results
	results, err := loadResults(evalFile, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading results: %v\n", err)
		os.Exit(1)
	}

	// Print summary
	printSummary(results)
	if lines, err := loadLines(evalFile); err == nil {
		var usage strings.Builder
		buildReport(buildResultSet(lines), nil, nil).writeUsage(&usage)
		fmt.Print(usage.String())
	}

	// Exit with appropriate status code
	hasFailures := false
	for _, result := range results {
		if !result.Pass {
			hasFailures = true
			break
		}
	}

	if hasFailures || testErr != nil {
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

	comment := r.commentMarkdown()
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
		fmt.Println("\nNothing actionable for the pull request; comment left empty.")
	} else {
		fmt.Printf("\n%d regressions written to %s\n", len(r.regressions), reportCommentFile)
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

// loadResults loads graded and errored results, skipping usage lines.
func loadResults(filename string, showOnlyFailures bool) ([]EvalLogLine, error) {
	lines, err := loadLines(filename)
	if err != nil {
		return nil, err
	}

	var results []EvalLogLine
	for _, result := range lines {
		if result.Usage != nil || (showOnlyFailures && result.Pass) {
			continue
		}
		results = append(results, result)
	}
	return results, nil
}

// loadLines loads every line of an evals.jsonl file, including usage lines.
func loadLines(filename string) ([]EvalLogLine, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("error opening file %s: %v", filename, err)
	}
	defer file.Close()

	var results []EvalLogLine
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
		results = append(results, result)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading file: %v", err)
	}

	return results, nil
}
