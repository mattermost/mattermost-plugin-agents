// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// releasenotes writes the release notes, release announcement and draft blog
// post for a Mattermost Agents release. See README.md in this directory.
//
//	go run ./build/releasenotes notes    [--base TAG] [--head REF] [--output FILE]
//	go run ./build/releasenotes announce --tag TAG --notes FILE
//	go run ./build/releasenotes blog     --tag TAG --notes FILE [--output FILE]
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

const defaultRepo = "mattermost/mattermost-plugin-agents"

func main() {
	os.Exit(run())
}

// run returns the process exit code.
func run() int {
	log.SetFlags(0)
	log.SetPrefix("releasenotes: ")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(os.Args) < 2 {
		usage()
		return 2
	}
	var err error
	switch os.Args[1] {
	case "notes":
		err = runNotes(ctx, os.Args[2:])
	case "announce":
		err = runAnnounce(ctx, os.Args[2:])
	case "blog":
		err = runBlog(ctx, os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return 0
	default:
		usage()
		return 2
	}
	if err != nil {
		log.Printf("error: %v", err)
		return 1
	}
	return 0
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: releasenotes <command> [flags]

commands:
  notes      generate release notes for a commit range with an LLM
  announce   format (and post) a Mattermost announcement from release notes
  blog       draft a marketing blog post from release notes and the docs

run "releasenotes <command> -h" for flags.
`)
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func defaultDocsURL(repo string) string {
	return envOr("RELEASE_DOCS_URL", "https://github.com/"+repo+"/tree/master/docs")
}

func modelFromEnv() string {
	return envOr("RELEASE_NOTES_MODEL", defaultModel)
}

func runNotes(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("notes", flag.ExitOnError)
	repo := fs.String("repo", envOr("GITHUB_REPOSITORY", defaultRepo), "GitHub repository (owner/name)")
	base := fs.String("base", "", "previous release tag; default: the previous release among the tags merged into --head")
	head := fs.String("head", "HEAD", "ref to release")
	tag := fs.String("tag", "", "tag being released; default: GITHUB_REF_NAME on tag builds, else a tag at --head")
	output := fs.String("output", "", "file to write, replaced atomically; default: stdout")
	printPrompt := fs.Bool("print-prompt", false, "print the prompt and exit without calling the model")
	if err := fs.Parse(args); err != nil {
		return err
	}

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" && !*printPrompt {
		return errors.New("ANTHROPIC_API_KEY is not set")
	}

	currentTag := *tag
	if currentTag == "" {
		currentTag = detectTag(ctx, *head)
	}
	baseRef := *base
	if baseRef == "" {
		var err error
		if baseRef, err = resolveBase(ctx, *head, currentTag); err != nil {
			return err
		}
	}
	log.Printf("range %s..%s (release %q)", baseRef, *head, currentTag)

	commits, err := collectCommits(ctx, baseRef, *head)
	if err != nil {
		return err
	}
	if len(commits) == 0 {
		return fmt.Errorf("no commits in %s..%s", baseRef, *head)
	}

	prNumbers := uniquePRNumbers(commits)
	log.Printf("%d commits, %d pull requests", len(commits), len(prNumbers))
	gh := newGitHubClient(*repo, os.Getenv("GITHUB_TOKEN"))
	prs, err := fetchPullRequests(ctx, gh, prNumbers)
	if err != nil {
		return err
	}
	entries := buildEntries(*repo, commits, prs)
	prompt := buildPrompt(promptOptions{Repo: *repo, Tag: currentTag, Base: baseRef}, entries)

	if *printPrompt {
		fmt.Printf("=== SYSTEM ===\n%s\n\n=== USER ===\n%s\n", notesSystemPrompt, prompt)
		return nil
	}

	minServer, err := minServerVersion(ctx, *head)
	if err != nil {
		return err
	}

	client := newLLMClient(apiKey, modelFromEnv())
	log.Printf("generating notes with %s (%d changes, %d prompt characters)", client.Model, len(entries), len(prompt))
	notes, err := generateNotes(ctx, client, prompt, allowedLinks(entries))
	if err != nil {
		return err
	}
	return writeOutput(*output, composeNotes(minServer, notes))
}

// minServerVersion reads min_server_version from plugin.json at head, falling
// back to the working tree copy.
func minServerVersion(ctx context.Context, head string) (string, error) {
	data, err := runGit(ctx, "show", head+":plugin.json")
	if err != nil {
		b, readErr := os.ReadFile("plugin.json")
		if readErr != nil {
			return "", fmt.Errorf("reading plugin.json: %w", readErr)
		}
		data = string(b)
	}
	var manifest struct {
		MinServerVersion string `json:"min_server_version"`
	}
	if err := json.Unmarshal([]byte(data), &manifest); err != nil {
		return "", fmt.Errorf("parsing plugin.json: %w", err)
	}
	if manifest.MinServerVersion == "" {
		return "", errors.New("plugin.json has no min_server_version")
	}
	return manifest.MinServerVersion, nil
}

func runAnnounce(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("announce", flag.ExitOnError)
	repo := fs.String("repo", envOr("GITHUB_REPOSITORY", defaultRepo), "GitHub repository (owner/name)")
	tag := fs.String("tag", "", "released tag (required)")
	notesPath := fs.String("notes", "", "release notes file, or - for stdin (required)")
	docsURL := fs.String("docs-url", "", "documentation link; default: $RELEASE_DOCS_URL or the repository docs folder")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *tag == "" || *notesPath == "" {
		return errors.New("--tag and --notes are required")
	}
	notes, err := readInput(*notesPath)
	if err != nil {
		return err
	}
	docs := *docsURL
	if docs == "" {
		docs = defaultDocsURL(*repo)
	}
	text := formatAnnouncement(announceOptions{Repo: *repo, Tag: *tag, DocsURL: docs, Notes: notes})

	webhook := os.Getenv("MATTERMOST_RELEASE_WEBHOOK_URL")
	if webhook == "" {
		log.Printf("MATTERMOST_RELEASE_WEBHOOK_URL is not set; not posting. The post would be:")
		fmt.Println(text)
		return nil
	}
	if err := postWebhook(ctx, newWebhookClient(), webhook, text); err != nil {
		return err
	}
	log.Printf("posted announcement for %s", *tag)
	return nil
}

func runBlog(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("blog", flag.ExitOnError)
	repo := fs.String("repo", envOr("GITHUB_REPOSITORY", defaultRepo), "GitHub repository (owner/name)")
	tag := fs.String("tag", "", "released tag (required)")
	notesPath := fs.String("notes", "", "release notes file, or - for stdin (required)")
	docsDir := fs.String("docs-dir", "docs", "directory with the documentation files")
	docNames := fs.String("docs", strings.Join(defaultBlogDocs, ","), "comma-separated documentation files in --docs-dir; missing files are skipped")
	docsURL := fs.String("docs-url", "", "documentation link; default: $RELEASE_DOCS_URL or the repository docs folder")
	output := fs.String("output", "", "file to write, replaced atomically; default: stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *tag == "" || *notesPath == "" {
		return errors.New("--tag and --notes are required")
	}
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return errors.New("ANTHROPIC_API_KEY is not set")
	}
	notes, err := readInput(*notesPath)
	if err != nil {
		return err
	}
	var names []string
	for n := range strings.SplitSeq(*docNames, ",") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	docs, err := loadDocs(*docsDir, names)
	if err != nil {
		return err
	}
	docURL := *docsURL
	if docURL == "" {
		docURL = defaultDocsURL(*repo)
	}
	client := newLLMClient(apiKey, modelFromEnv())
	log.Printf("drafting blog post with %s (%d documentation files)", client.Model, len(docs))
	post, err := generateBlog(ctx, client, blogOptions{Repo: *repo, Tag: *tag, DocsURL: docURL, Notes: notes, Docs: docs})
	if err != nil {
		return err
	}
	return writeOutput(*output, post)
}

func readInput(path string) (string, error) {
	var b []byte
	var err error
	if path == "-" {
		b, err = io.ReadAll(os.Stdin)
	} else {
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// writeOutput writes content to path by renaming a temporary file over it, so
// a failure never leaves a partial file. An empty path writes to stdout.
func writeOutput(path, content string) error {
	if path == "" {
		_, err := io.WriteString(os.Stdout, content)
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".releasenotes-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil { //nolint:gosec // release notes are meant to be world-readable
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}
