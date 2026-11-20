// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package agentconfig

import (
	"errors"
)

// Kind classifies why an agent update failed, so each caller can translate it
// (an HTTP status, a tool result) without matching error text.
type Kind int

const (
	// KindInternal is a store, configuration, or Mattermost API failure.
	KindInternal Kind = iota
	// KindNotFound means the agent does not exist or is deleted.
	KindNotFound
	// KindForbidden means the actor may not make this change.
	KindForbidden
	// KindInvalid means the proposed configuration or username is invalid.
	KindInvalid
	// KindConflict means the requested username is held by another account.
	KindConflict
	// KindNotLicensed wraps an *enterprise.LicenseError.
	KindNotLicensed
	// KindAccess wraps an accesscontrol error from validating the write.
	KindAccess
	// KindPolicy wraps a failure to delete the agent's access policy.
	KindPolicy
)

// Error is the error type every Service method returns.
type Error struct {
	Kind Kind
	Err  error
}

func (e *Error) Error() string { return e.Err.Error() }

func (e *Error) Unwrap() error { return e.Err }

func newError(kind Kind, err error) *Error {
	return &Error{Kind: kind, Err: err}
}

// KindOf returns the kind of err, or KindInternal when err is not an *Error.
func KindOf(err error) Kind {
	if e, ok := errors.AsType[*Error](err); ok {
		return e.Kind
	}
	return KindInternal
}

var (
	// ErrNotFound is wrapped by KindNotFound errors.
	ErrNotFound = errors.New("agent not found")

	// ErrForbidden is wrapped by KindForbidden errors when the actor may not
	// manage the agent at all.
	ErrForbidden = errors.New("not authorized to modify this agent")

	// ErrServiceAccountAuthRequiresAdmin is wrapped by KindForbidden errors
	// when an actor who may manage the agent, but not service accounts, tries
	// to turn service account authentication on or change its grants.
	ErrServiceAccountAuthRequiresAdmin = errors.New("only system administrators and people with read and write access to the Agents settings can save an agent with service account authentication enabled; turn the setting off to make other changes")

	// ErrSelfSetupFieldNotAllowed is wrapped by KindForbidden errors when a
	// self_setup write changes a field outside SelfSetupFields, or widens one.
	ErrSelfSetupFieldNotAllowed = errors.New("an agent can only change its own display name and instructions, or turn its own tools off")

	// ErrInvalidUsername is wrapped by KindInvalid errors.
	ErrInvalidUsername = errors.New("invalid username: must start with a lowercase letter and contain only lowercase letters, numbers, dots, hyphens, or underscores")
)
