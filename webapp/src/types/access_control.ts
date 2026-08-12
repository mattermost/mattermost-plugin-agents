// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Mirrors of the server JSON shapes (model.AccessControlPolicy et al.) used by
// the ABAC policy editor. Field names are snake_case as serialized by Go.

export type AccessControlPolicyRule = {
    actions: string[];
    expression: string;
    name?: string;
    role?: string;
};

export type AccessControlPolicy = {
    id: string;
    name: string;
    type: string;
    active: boolean;
    create_at: number;
    revision: number;
    version: string;
    roles: string[] | null;
    imports: string[] | null;
    rules: AccessControlPolicyRule[] | null;
    scope?: string;
    scope_id?: string;
    props: Record<string, unknown> | null;
};

export type CELExpressionError = {
    line: number;
    column: number;
    message: string;
};

// model.AccessControlPolicyTestResponse; users are full user profiles but the
// editors only read a handful of fields.
export type AccessControlTestResult = {
    users: Array<Record<string, unknown>>;
    total: number;
};

export type VisualExpressionCondition = {
    attribute: string;
    operator: string;
    value: unknown;
    value_type: number;
    attribute_type: string;
    has_masked_values?: boolean;
};

export type VisualExpression = {
    conditions: VisualExpressionCondition[];
};

// model.PropertyField, as returned by the fields-autocomplete proxy. Passed
// through to the host webapp's editors (which type it as UserPropertyField).
export type AccessControlPropertyField = {
    id: string;
    group_id: string;
    name: string;
    type: string;
    attrs: Record<string, unknown> | null;
    target_id?: string;
    target_type?: string;

    // 'user' | 'session' — buckets CEL autocomplete (user.attributes.* vs user.session.*).
    object_type?: string;
    create_at: number;
    update_at: number;
    delete_at: number;
};

export type ABACStatus = {
    available: boolean;
};

export type PolicyResourceType = 'agent' | 'service' | 'mcp';
