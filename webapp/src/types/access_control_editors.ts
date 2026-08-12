// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Prop contracts for the access-control editors the host webapp exports on
// window.Components, narrowed to exactly the props this plugin passes (see
// policy_editor.tsx). Source of truth in the mattermost repo (webapp/):
//   - channels/src/components/admin_console/access_control/editors/table_editor/table_editor.tsx (TableEditorProps)
//   - channels/src/components/admin_console/access_control/editors/cel_editor/editor.tsx (CELEditorProps, CELEditorActions)
//   - channels/src/packages/mattermost-redux/src/types/actions.ts (ActionResult)
// Drift tripwire: `npm run check-editor-contract` type-checks these mirrors
// against the host's exported prop types.

import type {ComponentType} from 'react';

import type {AccessControlPropertyField, AccessControlTestResult, CELExpressionError, VisualExpression} from './access_control';

// Mirrors mattermost-redux's ActionResult. The host editors receive plugin
// promises adapted onto this {data}/{error} shape.
export type ActionResult<Data = unknown, Err = unknown> = {
    data?: Data;
    error?: Err;
};

// Mirrors the subset of TableEditorProps['actions'] this plugin supplies.
export type TableEditorActions = {
    getVisualAST: (expr: string) => Promise<ActionResult<VisualExpression>>;

    // Overrides the redux thunk backing the built-in TestResultsModal so the
    // request routes through the plugin's proxy.
    searchUsers?: (expression: string, term: string, after: string, limit: number) => Promise<ActionResult<AccessControlTestResult>>;
};

// Mirrors the subset of TableEditorProps this plugin passes. The host types
// userAttributes as UserPropertyField[]; AccessControlPropertyField is this
// plugin's deliberately-looser mirror of the same model.PropertyField wire
// shape (the values flow from the server through the plugin untouched).
export type TableEditorProps = {
    value: string;
    onChange: (value: string) => void;
    onValidate?: (isValid: boolean) => void;
    userAttributes: AccessControlPropertyField[];
    enableUserManagedAttributes: boolean;
    onParseError: (error: string) => void;
    actions: TableEditorActions;
};

// Mirrors CELEditorProps['userAttributes'] entries.
export type CELEditorAttribute = {
    attribute: string;
    values: string[];
    isNative?: boolean;
    objectType?: string;
};

// Mirrors the subset of CELEditorActions this plugin supplies.
export type CELEditorActions = {

    // Overrides Client4.checkAccessControlExpression; receives only the
    // expression — resource scoping is the supplier's concern.
    checkExpression?: (expression: string) => Promise<CELExpressionError[]>;
    searchUsers?: (expression: string, term: string, after: string, limit: number) => Promise<ActionResult<AccessControlTestResult>>;
};

// Mirrors the subset of CELEditorProps this plugin passes.
export type CELEditorProps = {
    value: string;
    onChange: (value: string) => void;
    onValidate?: (isValid: boolean) => void;
    userAttributes: CELEditorAttribute[];
    actions?: CELEditorActions;
};

export type AccessControlTableEditorComponent = ComponentType<TableEditorProps>;
export type AccessControlCELEditorComponent = ComponentType<CELEditorProps>;
