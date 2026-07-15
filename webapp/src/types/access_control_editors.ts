// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Prop contracts for the access-control editors the host webapp exports on
// window.Components (contract §6.1). These mirror the props implemented in
// the mattermost webapp — the source of truth is
// components/admin_console/access_control/editors/ over there; keep the two
// in sync when the host editors gain or change props.

import type {ComponentType, ReactNode} from 'react';

import type {AccessControlPropertyField, AccessControlTestResult, CELExpressionError, VisualExpression} from './access_control';

// Mirrors mattermost-redux's ActionResult. The host editors receive plugin
// promises adapted onto this {data}/{error} shape.
export type ActionResult<Data = unknown, Err = unknown> = {
    data?: Data;
    error?: Err;
};

// Mirrors TableEditorProps['actions'] in the host webapp's table_editor.tsx.
export type TableEditorActions = {
    getVisualAST: (expr: string) => Promise<ActionResult<VisualExpression>>;

    // Overrides the redux thunk backing the built-in TestResultsModal so the
    // request routes through the plugin's proxy.
    searchUsers?: (expression: string, term: string, after: string, limit: number) => Promise<ActionResult<AccessControlTestResult>>;
};

// Mirrors TableEditorProps in the host webapp's table_editor.tsx. The host
// types userAttributes as UserPropertyField[]; AccessControlPropertyField is
// this plugin's mirror of the same model.PropertyField wire shape.
export type TableEditorProps = {
    value: string;
    onChange: (value: string) => void;
    onValidate?: (isValid: boolean) => void;
    disabled?: boolean;
    userAttributes: AccessControlPropertyField[];
    enableUserManagedAttributes: boolean;
    onParseError: (error: string) => void;
    channelId?: string;
    teamId?: string;
    actions: TableEditorActions;
    isSystemAdmin?: boolean;
    validateExpressionAgainstRequester?: (expression: string) => Promise<ActionResult<{requester_matches: boolean}>>;
    onTestClick?: () => void;
    testButtonDisabled?: boolean;
    testButtonTooltip?: string;
    testButtonLabel?: ReactNode;
    onMaskedStateChange?: (hasMasked: boolean) => void;
};

// Mirrors CELEditorProps['userAttributes'] entries (contract §6.2).
export type CELEditorAttribute = {
    attribute: string;
    values: string[];
    isNative?: boolean;
};

// Mirrors CELEditorActions in the host webapp's cel_editor/editor.tsx.
export type CELEditorActions = {

    // Overrides Client4.checkAccessControlExpression; receives only the
    // expression — resource scoping is the supplier's concern.
    checkExpression?: (expression: string) => Promise<CELExpressionError[]>;
    searchUsers?: (expression: string, term: string, after: string, limit: number) => Promise<ActionResult<AccessControlTestResult>>;
};

// Mirrors CELEditorProps in the host webapp's cel_editor/editor.tsx.
export type CELEditorProps = {
    value: string;
    onChange: (value: string) => void;
    onValidate?: (isValid: boolean) => void;
    placeholder?: string;
    className?: string;
    channelId?: string;
    teamId?: string;
    disabled?: boolean;
    userAttributes: CELEditorAttribute[];
    onTestClick?: () => void;
    testButtonLabel?: ReactNode;
    hasMaskedRows?: boolean;
    actions?: CELEditorActions;
};

export type AccessControlTableEditorComponent = ComponentType<TableEditorProps>;
export type AccessControlCELEditorComponent = ComponentType<CELEditorProps>;
