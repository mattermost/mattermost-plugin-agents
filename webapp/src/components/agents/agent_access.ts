// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {UserAgent} from '@/types/agents';

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
