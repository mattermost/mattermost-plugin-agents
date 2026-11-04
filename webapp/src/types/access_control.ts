// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Server JSON shapes (model.AccessControlPolicy et al.) used by the ABAC
// policy editor. Field names are snake_case as serialized by Go. Everything
// @mattermost/types already publishes is re-exported from there; only shapes
// the package gets wrong or doesn't cover are defined locally.

import type {
    AccessControlPolicyRule,
    AccessControlTestResult,
    AccessControlVisualAST,
    AccessControlVisualASTNode,
    CELExpressionError,
} from '@mattermost/types/access_control';

export type {
    AccessControlPolicyRule,
    AccessControlTestResult,
    AccessControlVisualAST,
    AccessControlVisualASTNode,
    CELExpressionError,
};

// Not imported from @mattermost/types: the published AccessControlPolicy
// declares `created_at`, but the server serializes model.AccessControlPolicy
// as `create_at` (and marks revision/active/version optional, which the
// editor relies on being present). Deriving via Omit would replace most of
// the fields anyway, so the wire shape is spelled out here.
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
