// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Client methods for the attribute-based access control (ABAC) surface:
// policy CRUD per resource type plus the CEL authoring proxies.

import {ClientError} from '@mattermost/client';

import {ABACStatus, AccessControlPolicy, AccessControlPropertyField, AccessControlTestResult, CELExpressionError, PolicyResourceType, VisualExpression} from '@/types/access_control';
import {agentRoute, baseRoute, Client4, readAgentErrorMessage} from '@/client';
import manifest from '@/manifest';

// getABACStatus reports whether the server-side ABAC engine is usable.
export async function getABACStatus(): Promise<ABACStatus> {
    const url = `${baseRoute()}/access_control/status`;
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

// getAccessPolicy fetches a policy from the given route; 404 resolves to null
// because "no policy" is a normal state, not an error.
async function getAccessPolicy(url: string): Promise<AccessControlPolicy | null> {
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        return response.json();
    }
    if (response.status === 404) {
        return null;
    }

    throw new ClientError(Client4.url, {
        message: await readAgentErrorMessage(response),
        status_code: response.status,
        url,
    });
}

async function putAccessPolicy(url: string, policy: AccessControlPolicy): Promise<AccessControlPolicy> {
    const response = await fetch(url, Client4.getOptions({
        method: 'PUT',
        body: JSON.stringify(policy),
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: await readAgentErrorMessage(response),
        status_code: response.status,
        url,
    });
}

async function deleteAccessPolicy(url: string): Promise<void> {
    const response = await fetch(url, Client4.getOptions({
        method: 'DELETE',
    }));

    if (response.ok || response.status === 404) {
        return;
    }

    throw new ClientError(Client4.url, {
        message: await readAgentErrorMessage(response),
        status_code: response.status,
        url,
    });
}

export function getAgentAccessPolicy(agentId: string): Promise<AccessControlPolicy | null> {
    return getAccessPolicy(`${agentRoute(agentId)}/access_policy`);
}

export function putAgentAccessPolicy(agentId: string, policy: AccessControlPolicy): Promise<AccessControlPolicy> {
    return putAccessPolicy(`${agentRoute(agentId)}/access_policy`, policy);
}

export function deleteAgentAccessPolicy(agentId: string): Promise<void> {
    return deleteAccessPolicy(`${agentRoute(agentId)}/access_policy`);
}

export function getServiceAccessPolicy(serviceId: string): Promise<AccessControlPolicy | null> {
    return getAccessPolicy(`${baseRoute()}/admin/services/${serviceId}/access_policy`);
}

export function putServiceAccessPolicy(serviceId: string, policy: AccessControlPolicy): Promise<AccessControlPolicy> {
    return putAccessPolicy(`${baseRoute()}/admin/services/${serviceId}/access_policy`, policy);
}

export function deleteServiceAccessPolicy(serviceId: string): Promise<void> {
    return deleteAccessPolicy(`${baseRoute()}/admin/services/${serviceId}/access_policy`);
}

export function getMCPServerAccessPolicy(serverId: string): Promise<AccessControlPolicy | null> {
    return getAccessPolicy(`${baseRoute()}/admin/mcp/${serverId}/access_policy`);
}

export function putMCPServerAccessPolicy(serverId: string, policy: AccessControlPolicy): Promise<AccessControlPolicy> {
    return putAccessPolicy(`${baseRoute()}/admin/mcp/${serverId}/access_policy`, policy);
}

export function deleteMCPServerAccessPolicy(serverId: string): Promise<void> {
    return deleteAccessPolicy(`${baseRoute()}/admin/mcp/${serverId}/access_policy`);
}

// celRouteURL appends the ?agent_id= authz lane for per-agent admins who hold
// no system permissions.
function celRouteURL(path: string, agentId?: string, extraQuery?: Record<string, string>): string {
    const url = new URL(`${baseRoute()}/access_control/cel/${path}`, window.location.origin);
    if (agentId) {
        url.searchParams.set('agent_id', agentId);
    }
    for (const [key, value] of Object.entries(extraQuery ?? {})) {
        url.searchParams.set(key, value);
    }
    return url.toString();
}

export async function checkAccessControlExpression(resourceType: PolicyResourceType, expression: string, agentId?: string): Promise<CELExpressionError[]> {
    const url = celRouteURL('check', agentId);
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
        body: JSON.stringify({resource_type: policyResourceTypeValue(resourceType), expression}),
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: await readAgentErrorMessage(response),
        status_code: response.status,
        url,
    });
}

export async function testAccessControlExpression(resourceType: PolicyResourceType, expression: string, term: string, after: string, limit: number, agentId?: string): Promise<AccessControlTestResult> {
    const url = celRouteURL('test', agentId);
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
        body: JSON.stringify({resource_type: policyResourceTypeValue(resourceType), expression, term, after, limit}),
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: await readAgentErrorMessage(response),
        status_code: response.status,
        url,
    });
}

export async function getAccessControlFields(after: string, limit: number, agentId?: string): Promise<AccessControlPropertyField[]> {
    const url = celRouteURL('autocomplete/fields', agentId, {after, limit: String(limit)});
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: await readAgentErrorMessage(response),
        status_code: response.status,
        url,
    });
}

export async function getAccessControlVisualAST(resourceType: PolicyResourceType, expression: string, agentId?: string): Promise<VisualExpression> {
    const url = celRouteURL('visual_ast', agentId);
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
        body: JSON.stringify({resource_type: policyResourceTypeValue(resourceType), expression}),
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: await readAgentErrorMessage(response),
        status_code: response.status,
        url,
    });
}

// policyResourceTypeValue maps the UI resource kind onto the plugin-owned ABAC
// policy type the CEL routes validate against, which the platform keys as
// "<pluginID>:<resourceType>".
function policyResourceTypeValue(resourceType: PolicyResourceType): string {
    return `${manifest.id}:${resourceType}`;
}
