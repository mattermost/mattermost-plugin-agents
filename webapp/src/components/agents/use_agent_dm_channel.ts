// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect, useState} from 'react';
import {useDispatch, useSelector, useStore} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';

import {Client4, getBotDirectChannel} from '@/client';
import type {LLMBot} from '@/bots';
import {BotsHandler} from '@/redux';
import manifest from '@/manifest';

export type AgentDMChannelState =
    | {status: 'loading'; channelId: string}
    | {status: 'ready'; channelId: string}
    | {status: 'error'; channelId: string; error: Error};

interface Outcome {
    key: string;
    error?: Error;
}

const toError = (e: unknown) => (e instanceof Error ? e : new Error(String(e)));

// Ensures the agent's DM channel exists and that the channel, the current
// user's membership and that membership's roles are in Redux. The product view
// does not mount the host's TeamController, so on a cold load none of these are
// guaranteed: the host ThreadViewer renders nothing without the channel, and
// AdvancedTextEditor is disabled without the channel and a CREATE_POST role.
export function useAgentDMChannel(bot: LLMBot | null): AgentDMChannelState {
    const dispatch = useDispatch();
    const store = useStore<GlobalState>();
    const currentUserId = useSelector<GlobalState, string>((state) => state.entities.users.currentUserId);

    const botId = bot?.id ?? '';
    const dmChannelId = bot?.dmChannelID ?? '';
    const key = `${botId}:${dmChannelId}`;

    const [outcome, setOutcome] = useState<Outcome | null>(null);

    // Create the DM channel when the bots list has none for this agent.
    useEffect(() => {
        let cancelled = false;
        const requestKey = key;
        const create = async () => {
            try {
                const newChannelId = await getBotDirectChannel(currentUserId, botId);
                if (cancelled) {
                    return;
                }

                // Read the latest list at dispatch time so a concurrent bots
                // refresh is not overwritten with a stale copy.
                const bots = (store.getState() as any)[`plugins-${manifest.id}`]?.bots as LLMBot[] | null | undefined;
                if (!bots) {
                    return;
                }
                dispatch({
                    type: BotsHandler,
                    bots: bots.map((b) => (b.id === botId ? {...b, dmChannelID: newChannelId} : b)),
                });
            } catch (e) {
                if (!cancelled) {
                    setOutcome({key: requestKey, error: toError(e)});
                }
            }
        };
        if (botId && !dmChannelId && currentUserId) {
            create();
        }
        return () => {
            cancelled = true;
        };
    }, [botId, dmChannelId, currentUserId, key, dispatch, store]);

    // Load the channel, membership and roles once the channel id is known.
    useEffect(() => {
        let cancelled = false;
        const requestKey = key;
        const load = async () => {
            try {
                if (!store.getState().entities.channels.channels[dmChannelId]) {
                    const channel = await Client4.getChannel(dmChannelId);
                    if (cancelled) {
                        return;
                    }
                    dispatch({type: 'RECEIVED_CHANNEL', data: channel});
                }

                let member = store.getState().entities.channels.myMembers[dmChannelId];
                if (!member) {
                    member = await Client4.getChannelMember(dmChannelId, currentUserId);
                    if (cancelled) {
                        return;
                    }
                    dispatch({type: 'RECEIVED_MY_CHANNEL_MEMBER', data: member});
                }

                const loadedRoles = store.getState().entities.roles?.roles ?? {};
                const missingRoles = (member.roles || '').split(' ').filter((name) => name && !loadedRoles[name]);
                if (missingRoles.length > 0) {
                    const roles = await Client4.getRolesByNames(missingRoles);
                    if (cancelled) {
                        return;
                    }
                    dispatch({type: 'RECEIVED_ROLES', data: roles});
                }

                if (!cancelled) {
                    setOutcome({key: requestKey});
                }
            } catch (e) {
                if (!cancelled) {
                    setOutcome({key: requestKey, error: toError(e)});
                }
            }
        };
        if (dmChannelId && currentUserId) {
            load();
        }
        return () => {
            cancelled = true;
        };
    }, [dmChannelId, currentUserId, key, dispatch, store]);

    if (outcome?.key === key) {
        if (outcome.error) {
            return {status: 'error', channelId: dmChannelId, error: outcome.error};
        }
        return {status: 'ready', channelId: dmChannelId};
    }
    return {status: 'loading', channelId: dmChannelId};
}
