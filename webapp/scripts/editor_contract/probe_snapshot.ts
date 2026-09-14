// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// CI-facing half of the editor-contract drift tripwire: the same
// assignability assertions as probe.ts, but against the committed snapshot
// (src/types/host_editor_contract.snapshot.d.ts) so the contract is enforced
// without a host checkout. See probe.ts for the rationale behind the
// userAttributes wire-shape exception and the split searchUsers checks.

/* eslint-disable @typescript-eslint/no-unused-vars, no-undef */

import type {AccessControlPropertyField, AccessControlTestResult as MirrorAccessControlTestResult, CELExpressionError as MirrorCELExpressionError} from '../../src/types/access_control';
import type {CELEditorActions as MirrorCELEditorActions, CELEditorProps as MirrorCELEditorProps, TableEditorActions as MirrorTableEditorActions, TableEditorProps as MirrorTableEditorProps} from '../../src/types/access_control_editors';
import type {HostAccessControlTestResult, HostCELEditorActions, HostCELEditorProps, HostCELExpressionError, HostTableEditorProps, HostUserPropertyField} from '../../src/types/host_editor_contract.snapshot';

declare function expectAssignable<T>(value: T): void;

// --- TableEditor ---

// Everything except the userAttributes exception must be assignable to the
// host contract (the plugin renders the host component with these props).
// searchUsers is carved out only so its parameters and result can be
// checked separately (and in both directions) below.
expectAssignable<Omit<HostTableEditorProps, 'userAttributes' | 'actions'>>(
    {} as Omit<MirrorTableEditorProps, 'userAttributes' | 'actions'>,
);
expectAssignable<Omit<HostTableEditorProps['actions'], 'searchUsers'>>(
    {} as Omit<MirrorTableEditorActions, 'searchUsers'>,
);

// userAttributes exception: the host's strictly-typed field must keep
// satisfying the plugin's wire mirror of model.PropertyField.
expectAssignable<AccessControlPropertyField>({} as HostUserPropertyField);
expectAssignable<AccessControlPropertyField[]>({} as MirrorTableEditorProps['userAttributes']);
expectAssignable<HostUserPropertyField[]>({} as HostTableEditorProps['userAttributes']);

// --- CELEditor ---

expectAssignable<Omit<HostCELEditorProps, 'actions'>>(
    {} as Omit<MirrorCELEditorProps, 'actions'>,
);
expectAssignable<Omit<HostCELEditorActions, 'searchUsers'>>(
    {} as Omit<MirrorCELEditorActions, 'searchUsers'>,
);

// CEL lint errors round-trip both ways.
expectAssignable<HostCELExpressionError>({} as MirrorCELExpressionError);
expectAssignable<MirrorCELExpressionError>({} as HostCELExpressionError);

// --- searchUsers (shared by both editors) ---

type MirrorSearchUsers = NonNullable<MirrorTableEditorActions['searchUsers']>;
type HostSearchUsers = NonNullable<HostTableEditorProps['actions']['searchUsers']>;
type CELMirrorSearchUsers = NonNullable<MirrorCELEditorActions['searchUsers']>;
type CELHostSearchUsers = NonNullable<HostCELEditorActions['searchUsers']>;

// Parameter lists must match exactly, in both directions.
expectAssignable<Parameters<HostSearchUsers>>({} as Parameters<MirrorSearchUsers>);
expectAssignable<Parameters<MirrorSearchUsers>>({} as Parameters<HostSearchUsers>);
expectAssignable<Parameters<CELHostSearchUsers>>({} as Parameters<CELMirrorSearchUsers>);
expectAssignable<Parameters<CELMirrorSearchUsers>>({} as Parameters<CELHostSearchUsers>);

// Actual callback return envelopes (not just the standalone result aliases):
// Promise/ActionResult wrapper drift trips here. The users payload is
// checked separately below so a failure names the moving part.
type MirrorSearchUsersResult = Awaited<ReturnType<MirrorSearchUsers>>;
type HostSearchUsersResult = Awaited<ReturnType<HostSearchUsers>>;
type CELMirrorSearchUsersResult = Awaited<ReturnType<CELMirrorSearchUsers>>;
type CELHostSearchUsersResult = Awaited<ReturnType<CELHostSearchUsers>>;

expectAssignable<Omit<HostSearchUsersResult, 'data'>>({} as Omit<MirrorSearchUsersResult, 'data'>);
expectAssignable<Omit<NonNullable<HostSearchUsersResult['data']>, 'users'>>(
    {} as Omit<NonNullable<MirrorSearchUsersResult['data']>, 'users'>,
);
expectAssignable<Array<object>>({} as NonNullable<HostSearchUsersResult['data']>['users']);
expectAssignable<Omit<CELHostSearchUsersResult, 'data'>>({} as Omit<CELMirrorSearchUsersResult, 'data'>);
expectAssignable<Omit<NonNullable<CELHostSearchUsersResult['data']>, 'users'>>(
    {} as Omit<NonNullable<CELMirrorSearchUsersResult['data']>, 'users'>,
);
expectAssignable<Array<object>>({} as NonNullable<CELHostSearchUsersResult['data']>['users']);

// Result envelope and users element type, checked apart from each other.
expectAssignable<Omit<HostAccessControlTestResult, 'users'>>(
    {} as Omit<MirrorAccessControlTestResult, 'users'>,
);
expectAssignable<Array<Record<string, unknown>>>({} as MirrorAccessControlTestResult['users']);
expectAssignable<Array<object>>({} as HostAccessControlTestResult['users']);

export {};
