// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import "fmt"

// usageTotals sums the token usage and cost of one role across trials.
type usageTotals struct {
	InputTokens      int64   `json:"input_tokens"`
	CachedReadTokens int64   `json:"cached_read_tokens,omitempty"`
	OutputTokens     int64   `json:"output_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	// Unpriced is set when some usage was for a model without a known price,
	// so CostUSD is a lower bound.
	Unpriced bool `json:"unpriced,omitempty"`
}

func (u *usageTotals) add(r RoleUsage) {
	u.InputTokens += r.InputTokens
	u.CachedReadTokens += r.CachedReadTokens
	u.OutputTokens += r.OutputTokens
	u.CostUSD += r.CostUSD
	if r.CostUSD == 0 && r.InputTokens+r.OutputTokens > 0 {
		u.Unpriced = true
	}
}

func (u *usageTotals) merge(o usageTotals) {
	u.InputTokens += o.InputTokens
	u.CachedReadTokens += o.CachedReadTokens
	u.OutputTokens += o.OutputTokens
	u.CostUSD += o.CostUSD
	u.Unpriced = u.Unpriced || o.Unpriced
}

func (u usageTotals) empty() bool {
	return u.InputTokens == 0 && u.OutputTokens == 0
}

// providerUsage is a provider's usage for the model under test and the grader.
type providerUsage struct {
	Agent  usageTotals `json:"agent"`
	Grader usageTotals `json:"grader"`
}

func (u *providerUsage) add(t TrialUsage) {
	u.Agent.add(t.Agent)
	u.Grader.add(t.Grader)
}

func (u *providerUsage) merge(o providerUsage) {
	u.Agent.merge(o.Agent)
	u.Grader.merge(o.Grader)
}

func (u providerUsage) empty() bool {
	return u.Agent.empty() && u.Grader.empty()
}

// costCell renders the run's cost, split between the model under test and
// the grader.
func (u providerUsage) costCell() string {
	if u.empty() {
		return "–"
	}
	return fmt.Sprintf("%s (%s + %s grading)", formatCost(u.Agent.CostUSD+u.Grader.CostUSD, u.Agent.Unpriced || u.Grader.Unpriced), formatCost(u.Agent.CostUSD, u.Agent.Unpriced), formatCost(u.Grader.CostUSD, u.Grader.Unpriced))
}

// tokensCell renders the model under test's token usage.
func (u providerUsage) tokensCell() string {
	if u.Agent.empty() {
		return "–"
	}
	cell := fmt.Sprintf("%s in / %s out", formatTokens(u.Agent.InputTokens), formatTokens(u.Agent.OutputTokens))
	if u.Agent.CachedReadTokens > 0 && u.Agent.InputTokens > 0 {
		cell += fmt.Sprintf(" (%d%% cached)", u.Agent.CachedReadTokens*100/u.Agent.InputTokens)
	}
	return cell
}

func formatCost(usd float64, unpriced bool) string {
	s := fmt.Sprintf("$%.2f", usd)
	if unpriced {
		s += "+"
	}
	return s
}

func formatTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1e3)
	default:
		return fmt.Sprintf("%d", n)
	}
}
