// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Compile-time drift tripwire between this plugin's mirrored editor prop
// contracts and the mattermost webapp's exported editor prop types. Never
// executed or bundled: scripts/check_editor_contract.mjs type-checks it
// against a mattermost webapp checkout when one is available.
//
// The mirrors must stay assignable to the host types. Two payloads are
// deliberate exceptions, typed as the server wire shape the plugin proxies
// through untouched: TableEditor's userAttributes and searchUsers results.
// For those the probe checks the inverse direction — the host's strict type
// must keep satisfying the plugin's wire mirror — so host-side
// renames/removals still fail this check.

/* eslint-disable @typescript-eslint/no-unused-vars, no-undef */

import type {AccessControlTestResult as HostAccessControlTestResult, CELExpressionError as HostCELExpressionError} from '@mattermost/types/access_control';
import type {UserPropertyField} from '@mattermost/types/properties_user';
import type {CELEditorActions as HostCELEditorActions, CELEditorProps as HostCELEditorProps} from 'components/admin_console/access_control/editors/cel_editor/editor';
import type {TableEditorProps as HostTableEditorProps} from 'components/admin_console/access_control/editors/table_editor/table_editor';

import type {AccessControlPropertyField, AccessControlTestResult as MirrorAccessControlTestResult, CELExpressionError as MirrorCELExpressionError} from '../../src/types/access_control';
import type {CELEditorActions as MirrorCELEditorActions, CELEditorProps as MirrorCELEditorProps, TableEditorActions as MirrorTableEditorActions, TableEditorProps as MirrorTableEditorProps} from '../../src/types/access_control_editors';

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
expectAssignable<AccessControlPropertyField>({} as UserPropertyField);
expectAssignable<AccessControlPropertyField[]>({} as MirrorTableEditorProps['userAttributes']);
expectAssignable<UserPropertyField[]>({} as HostTableEditorProps['userAttributes']);

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
