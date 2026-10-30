// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSelector} from 'react-redux';

import type {UserAgent} from '@/types/agents';
import {userHasSystemPermission} from '@/utils/permissions';

export interface AgentManagePermissions {
    hasManageOthersAgent: boolean;
    hasManageSystem: boolean;
}

// Whether the current user may edit (configure, export, delete) an agent.
// Shared by the All agents list and the chat header so both gate identically.
export const canManageAgent = (agent: UserAgent, currentUserId: string, {hasManageOthersAgent, hasManageSystem}: AgentManagePermissions): boolean => {
    const isOwner = agent.creatorID === currentUserId || (agent.adminUserIDs?.includes(currentUserId) ?? false);
    if (isOwner || hasManageOthersAgent) {
        return true;
    }

    // Migrated legacy bots have no creator; system admins had full control via System Console.
    return Boolean(!agent.creatorID && hasManageSystem);
};

// Whether the current user may create agents (the All agents "Create agent" button
// and the sidebar's "New agent" item share this rule).
export const canCreateAgent = ({hasManageOwnAgent, hasManageSystem}: {hasManageOwnAgent: boolean; hasManageSystem: boolean}): boolean => (
    hasManageOwnAgent || hasManageSystem
);

export interface AgentPermissions extends AgentManagePermissions {
    currentUserId: string;
    hasManageOwnAgent: boolean;

    // canCreateAgent.
    canCreate: boolean;

    // Mirrors api.canConfigureAgentServices: without one of these, /services 403s, so users browse
    // read-only and the editor gets no service list.
    canViewServices: boolean;
}

// The current user's agent permissions, for the All agents list, the sidebar's add menu and the
// chat's Agent settings. Fails closed: without a current user (or their roles) everything is false.
export function useAgentPermissions(): AgentPermissions {
    const currentUserId = useSelector((state: any) => state.entities?.users?.currentUserId ?? '') as string;
    const has = (permission: string) => (state: any) => Boolean(currentUserId) && userHasSystemPermission(state, currentUserId, permission);
    const hasManageOwnAgent = useSelector(has('manage_own_agent'));
    const hasManageOthersAgent = useSelector(has('manage_others_agent'));
    const hasManageSystem = useSelector(has('manage_system'));
    return {
        currentUserId,
        hasManageOwnAgent,
        hasManageOthersAgent,
        hasManageSystem,
        canCreate: canCreateAgent({hasManageOwnAgent, hasManageSystem}),
        canViewServices: hasManageOwnAgent || hasManageOthersAgent || hasManageSystem,
    };
}
