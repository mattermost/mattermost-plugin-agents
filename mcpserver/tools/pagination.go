// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

type paginationOptions struct {
	DefaultPerPage  int
	FallbackPerPage int
	MaxPerPage      int
}

type pagination struct {
	Page    int
	PerPage int
}

func newPagination(page, perPage int, opts paginationOptions) pagination {
	if page < 0 {
		page = 0
	}
	if perPage <= 0 {
		perPage = opts.FallbackPerPage
	}
	if perPage <= 0 {
		perPage = opts.DefaultPerPage
	}
	if opts.MaxPerPage > 0 && perPage > opts.MaxPerPage {
		perPage = opts.MaxPerPage
	}
	if perPage < 0 {
		perPage = 0
	}

	return pagination{
		Page:    page,
		PerPage: perPage,
	}
}

func (p pagination) SliceBounds(total int) (start int, end int, ok bool) {
	if total <= 0 {
		return 0, 0, false
	}
	if p.PerPage <= 0 {
		return 0, total, true
	}

	totalPages := total / p.PerPage
	if total%p.PerPage != 0 {
		totalPages++
	}
	if p.Page >= totalPages {
		return 0, 0, false
	}

	start = p.Page * p.PerPage
	end = start + p.PerPage
	if end > total {
		end = total
	}

	return start, end, true
}
