// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSelector} from 'react-redux';

import {userHasSystemPermission} from '@/utils/permissions';

export interface AgentPermissions {
    currentUserId: string;

    // The All agents "Create agent" button and the sidebar's "New agent" item.
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
        canCreate: hasManageOwnAgent || hasManageSystem,
        canViewServices: hasManageOwnAgent || hasManageOthersAgent || hasManageSystem,
    };
}
