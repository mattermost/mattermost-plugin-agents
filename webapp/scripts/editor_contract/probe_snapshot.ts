// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// CI-facing half of the editor-contract drift tripwire: the same
// assignability assertions as probe.ts, but against the committed snapshot of
// the host types (src/types/host_editor_contract.snapshot.d.ts) instead of a
// live mattermost webapp checkout, so scripts/check_editor_contract.mjs can
// enforce the contract unconditionally — no host checkout required. When a
// checkout IS present the script additionally runs probe.ts against it and
// verifies the snapshot itself is still fresh.
//
// See probe.ts for the rationale behind the two deliberate wire-shape
// exceptions (userAttributes and searchUsers results).

/* eslint-disable @typescript-eslint/no-unused-vars, no-undef */

import type {AccessControlPropertyField, AccessControlTestResult as MirrorAccessControlTestResult, CELExpressionError as MirrorCELExpressionError} from '../../src/types/access_control';
import type {CELEditorActions as MirrorCELEditorActions, CELEditorProps as MirrorCELEditorProps, TableEditorActions as MirrorTableEditorActions, TableEditorProps as MirrorTableEditorProps} from '../../src/types/access_control_editors';
import type {HostAccessControlTestResult, HostCELEditorActions, HostCELEditorProps, HostCELExpressionError, HostTableEditorProps, HostUserPropertyField} from '../../src/types/host_editor_contract.snapshot';

declare function expectAssignable<T>(value: T): void;

// --- TableEditor ---

// Everything except the wire-payload exceptions must be assignable to the
// host contract (the plugin renders the host component with these props).
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

// Result envelope matches modulo the users element type (wire exception).
expectAssignable<Omit<HostAccessControlTestResult, 'users'>>(
    {} as Omit<MirrorAccessControlTestResult, 'users'>,
);
expectAssignable<Array<Record<string, unknown>>>({} as MirrorAccessControlTestResult['users']);
expectAssignable<Array<object>>({} as HostAccessControlTestResult['users']);

export {};
