// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useEffect, useState} from 'react';

import {getAgents, getServices} from '@/client';
import type {ServiceInfo, UserAgent} from '@/types/agents';

import {canManageAgent, useAgentPermissions} from './agent_access';

export type AgentEditAccess =
    | {status: 'loading'}
    | {status: 'denied'}
    | {status: 'allowed'; agent: UserAgent; services: ServiceInfo[]};

// Resolves whether the current user may edit the agent behind a bot user, with
// the same rule the All agents list uses (canManageAgent). The agent is matched
// by bot user ID. Fails closed: a missing agent or a failed lookup is 'denied'.
export function useAgentEditAccess(botId: string): {access: AgentEditAccess; reload: () => void} {
    const {currentUserId, hasManageOthersAgent, hasManageSystem, canViewServices} = useAgentPermissions();

    const [result, setResult] = useState<{identity: string; access: AgentEditAccess} | null>(null);
    const [revision, setRevision] = useState(0);

    // Who and what the access was resolved for. A reload (the revision) keeps the previous result until
    // the new one arrives, so an open editor is not unmounted (and its unsaved edits lost) meanwhile.
    const identity = `${botId}:${currentUserId}:${hasManageOthersAgent}:${hasManageSystem}:${canViewServices}`;
    const key = `${identity}:${revision}`;

    useEffect(() => {
        let cancelled = false;
        const requestIdentity = identity;
        const load = async () => {
            let access: AgentEditAccess = {status: 'denied'};
            try {
                const {agents} = await getAgents();
                const agent = (agents ?? []).find((a) => Boolean(a.botUserID) && a.botUserID === botId);
                if (agent && canManageAgent(agent, currentUserId, {hasManageOthersAgent, hasManageSystem})) {
                    let services: ServiceInfo[] = [];
                    if (canViewServices) {
                        try {
                            services = await getServices() ?? [];
                        } catch (e) {
                            // The editor still opens (as from All agents); it has no service list.
                            console.error('Failed to load AI services for agent settings:', e); // eslint-disable-line no-console
                        }
                    }
                    access = {status: 'allowed', agent, services};
                }
            } catch (e) {
                console.error('Failed to resolve agent edit access:', e); // eslint-disable-line no-console
            }
            if (!cancelled) {
                setResult({identity: requestIdentity, access});
            }
        };
        if (botId && currentUserId) {
            load();
        }
        return () => {
            cancelled = true;
        };
    }, [key, identity, botId, currentUserId, hasManageOthersAgent, hasManageSystem, canViewServices]);

    const reload = useCallback(() => setRevision((n) => n + 1), []);

    if (!botId || !currentUserId) {
        return {access: {status: 'denied'}, reload};
    }
    return {access: result?.identity === identity ? result.access : {status: 'loading'}, reload};
}
