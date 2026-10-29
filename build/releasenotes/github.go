// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

const defaultGitHubAPI = "https://api.github.com"

// pullRequest is the subset of a GitHub pull request the notes need.
type pullRequest struct {
	Number int
	Title  string
	// Body is the description with comments, images and empty template
	// sections removed and the release-note block extracted.
	Body   string
	Labels []string
	// ReleaseNote is the content of the PR template's release-note block.
	ReleaseNote string
	// NoReleaseNote is set when that block says NONE.
	NoReleaseNote bool
	// Missing is set when GitHub has no such pull request (the number may
	// belong to an issue or another repository); only the commit subject is known.
	Missing bool
	// Untrusted is set when the author has no write access. A PR's title and
	// description stay editable after merge without review, so only the
	// commit subject, frozen at merge, is used for these.
	Untrusted bool
}

// trustedAssociations are the authors who could change the code anyway.
var trustedAssociations = map[string]bool{"OWNER": true, "MEMBER": true, "COLLABORATOR": true}

type githubClient struct {
	BaseURL string
	Token   string
	Repo    string
	HTTP    *http.Client
	// Backoff is the base delay between retries of transient failures.
	Backoff time.Duration
}

func newGitHubClient(repo, token string) *githubClient {
	return &githubClient{
		// GITHUB_API_URL is set by Actions (and points at GHES when applicable).
		BaseURL: envOr("GITHUB_API_URL", defaultGitHubAPI),
		Token:   token,
		Repo:    repo,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
		Backoff: 2 * time.Second,
	}
}

type pullRequestResponse struct {
	Title             string `json:"title"`
	Body              string `json:"body"`
	AuthorAssociation string `json:"author_association"`
	Labels            []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

const maxAttempts = 3

// sleepCtx waits for d or until ctx is done.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// pullRequest fetches one pull request. A 404 yields a pullRequest with
// Missing set; other failures are errors after bounded retries.
func (g *githubClient) pullRequest(ctx context.Context, number int) (pullRequest, error) {
	url := fmt.Sprintf("%s/repos/%s/pulls/%d", strings.TrimRight(g.BaseURL, "/"), g.Repo, number)
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			if err := sleepCtx(ctx, g.Backoff*time.Duration(attempt-1)); err != nil {
				return pullRequest{}, err
			}
		}
		pr, retry, err := g.fetchOnce(ctx, url, number)
		if err == nil {
			return pr, nil
		}
		lastErr = err
		if !retry {
			break
		}
	}
	return pullRequest{}, lastErr
}

func (g *githubClient) fetchOnce(ctx context.Context, url string, number int) (pr pullRequest, retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return pullRequest{}, false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return pullRequest{}, true, fmt.Errorf("fetching PR #%d: %w", number, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return pullRequest{}, true, fmt.Errorf("reading PR #%d: %w", number, err)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return pullRequest{Number: number, Missing: true}, false, nil
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden || resp.StatusCode >= 500:
		// 403 is how GitHub reports exhausted rate limits.
		return pullRequest{}, true, fmt.Errorf("fetching PR #%d: GitHub returned %s", number, resp.Status)
	default:
		return pullRequest{}, false, fmt.Errorf("fetching PR #%d: GitHub returned %s", number, resp.Status)
	}

	var parsed pullRequestResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return pullRequest{}, false, fmt.Errorf("decoding PR #%d: %w", number, err)
	}
	if !trustedAssociations[parsed.AuthorAssociation] {
		return pullRequest{Number: number, Untrusted: true}, false, nil
	}
	pr = pullRequest{Number: number, Title: strings.TrimSpace(parsed.Title)}
	for _, l := range parsed.Labels {
		pr.Labels = append(pr.Labels, l.Name)
	}
	pr.Body, pr.ReleaseNote, pr.NoReleaseNote = cleanBody(parsed.Body)
	return pr, false, nil
}

// fetchPullRequests loads the given pull requests with bounded concurrency.
// It fails on the first non-recoverable error.
func fetchPullRequests(ctx context.Context, g *githubClient, numbers []int) (map[int]pullRequest, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	const workers = 8
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		result   = make(map[int]pullRequest, len(numbers))
		firstErr error
		jobs     = make(chan int)
	)
	for range workers {
		wg.Go(func() {
			for n := range jobs {
				pr, err := g.pullRequest(ctx, n)
				mu.Lock()
				if err != nil {
					if firstErr == nil {
						firstErr = err
						cancel()
					}
				} else {
					result[n] = pr
				}
				mu.Unlock()
			}
		})
	}
feed:
	for _, n := range numbers {
		select {
		case jobs <- n:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

var (
	htmlCommentRe  = regexp.MustCompile(`(?s)<!--.*?(?:-->|$)`)
	releaseNoteRe  = regexp.MustCompile("(?s)```release-note[ \t]*\n(.*?)```")
	markdownImgRe  = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	htmlImgRe      = regexp.MustCompile(`(?i)<img\b[^>]*>`)
	blankRunRe     = regexp.MustCompile(`\n{3,}`)
	noneReleaseRe  = regexp.MustCompile(`(?i)^none\.?$`)
	markdownHeadRe = regexp.MustCompile(`^#{1,6}\s`)
)

// stripHTMLComments removes HTML comments, including an unterminated one.
func stripHTMLComments(s string) string {
	return htmlCommentRe.ReplaceAllString(s, "")
}

// cleanBody prepares a pull request description for the prompt. It removes
// HTML comments (the PR template's instructions), images, and template
// headings with no content, and splits out the release-note block.
func cleanBody(body string) (text, releaseNote string, none bool) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = stripHTMLComments(body)

	if m := releaseNoteRe.FindStringSubmatch(body); m != nil {
		releaseNote = strings.TrimSpace(m[1])
		body = releaseNoteRe.ReplaceAllString(body, "")
	}
	if noneReleaseRe.MatchString(releaseNote) {
		none = true
		releaseNote = ""
	}

	body = markdownImgRe.ReplaceAllString(body, "")
	body = htmlImgRe.ReplaceAllString(body, "")
	body = dropEmptyHeadings(body)
	body = blankRunRe.ReplaceAllString(body, "\n\n")
	return strings.TrimSpace(body), releaseNote, none
}

// dropEmptyHeadings removes markdown headings that have no content before the
// next heading or the end of the text, such as unfilled template sections.
func dropEmptyHeadings(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		if markdownHeadRe.MatchString(strings.TrimSpace(line)) {
			empty := true
			for _, next := range lines[i+1:] {
				t := strings.TrimSpace(next)
				if t == "" {
					continue
				}
				empty = markdownHeadRe.MatchString(t)
				break
			}
			if empty {
				continue
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// truncateRunes shortens s to at most limit runes, marking the cut.
func truncateRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return strings.TrimSpace(string(r[:limit])) + "\n[truncated]"
}
