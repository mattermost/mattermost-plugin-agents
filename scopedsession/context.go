// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package scopedsession

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/mattermost/mattermost/server/public/model"
)

// ErrRunEnded is returned for a read under a scoped session whose run ended.
var ErrRunEnded = errors.New("scoped session's run has ended")

// Binding ties a scoped session's scope to the reads made under it. A run
// owns one binding and ends it when the run ends.
type Binding struct {
	scope *model.SessionScope
	ended atomic.Bool
}

// NewBinding returns a live binding for scope.
func NewBinding(scope *model.SessionScope) *Binding {
	return &Binding{scope: scope}
}

// End makes every later read under the binding fail, including reads already
// in flight on a connection that carries it.
func (b *Binding) End() {
	if b != nil {
		b.ended.Store(true)
	}
}

type bindingKey struct{}

// WithBinding marks ctx as running under b's scoped session. Connections
// whose reads must honor the scope carry it from the moment they are made,
// so plugin services read the scope from the request rather than looking the
// session up.
func WithBinding(ctx context.Context, b *Binding) context.Context {
	return context.WithValue(ctx, bindingKey{}, b)
}

// ScopeFromContext returns the scope ctx runs under, or nil when ctx carries
// no binding and the read is the user's own. A binding without a scope, or
// one whose run has ended, is an error, never unscoped.
func ScopeFromContext(ctx context.Context) (*model.SessionScope, error) {
	b, marked := ctx.Value(bindingKey{}).(*Binding)
	if !marked {
		return nil, nil
	}
	if b == nil || b.scope == nil {
		return nil, errors.New("scoped read has no scope")
	}
	if b.ended.Load() {
		return nil, ErrRunEnded
	}
	return b.scope, nil
}
