// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect, useMemo, useCallback} from 'react';

import {useDispatch, useSelector} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';
import {PreferenceType} from '@mattermost/types/preferences';

import {getAIBots, savePreferences} from '@/client';

import manifest from './manifest';
import {BotsHandler} from './redux';
import {ChannelAccessLevel, UserAccessLevel} from './components/system_console/bot';
import {EnabledTool} from './types/agents';

export type EnabledMCPTool = EnabledTool;

export interface LLMBot {
    id: string;
    displayName: string;
    username: string;
    lastIconUpdate: number;
    dmChannelID: string;
    channelAccessLevel: ChannelAccessLevel;

    // Server sends nil Go slices as JSON null (no teamIDs field on /ai_bots).
    channelIDs: string[] | null;
    userAccessLevel: UserAccessLevel;
    userIDs: string[] | null;
    enabledMCPTools: EnabledMCPTool[] | null;
    autoEnableNewMCPTools: boolean;

    // Optional so the UI degrades to the non-SA UX when the server predates the field.
    useServiceAccountAuth?: boolean;

    // isDefault marks the system-wide default agent. Optional: older servers
    // omit it, in which case we fall back to list ordering.
    isDefault?: boolean;
}

// Shared core preference identifying the user's selected agent across every
// client and selector surface. Must stay byte-identical across repos.
export const PREFERENCE_CATEGORY_AGENTS = 'agents';
export const PREFERENCE_NAME_SELECTED_AGENT = 'selected_agent';

const selectedAgentPreferenceKey = `${PREFERENCE_CATEGORY_AGENTS}--${PREFERENCE_NAME_SELECTED_AGENT}`;

// Saved selected-agent id from global preferences, '' when unset.
export const getSelectedAgentId = (state: any): string =>
    state.entities?.preferences?.myPreferences?.[selectedAgentPreferenceKey]?.value ?? '';

// Selection precedence: saved preference (when still available) -> system
// default bot -> first available bot.
export const resolveActiveBot = (bots: LLMBot[] | null, preferredId: string): LLMBot | null => {
    if (!bots || bots.length === 0) {
        return null;
    }
    if (preferredId) {
        const preferred = bots.find((bot) => bot.id === preferredId);
        if (preferred) {
            return preferred;
        }
    }
    return bots.find((bot) => bot.isDefault) ?? bots[0];
};

export const useBotlist = () => {
    const bots = useSelector<GlobalState, LLMBot[] | null>((state: any) => state['plugins-' + manifest.id].bots);
    const currentUserId = useSelector<GlobalState, string>((state) => state.entities.users.currentUserId);
    const selectedAgentId = useSelector(getSelectedAgentId);
    const dispatch = useDispatch();

    // Load bots
    useEffect(() => {
        const fetchBots = async () => {
            const response = await getAIBots();
            if (!response) {
                return;
            }

            dispatch({
                type: BotsHandler,
                bots: response.bots,
            });

            dispatch({
                type: 'SET_SEARCH_ENABLED',
                searchEnabled: response.searchEnabled,
            });

            dispatch({
                type: 'SET_ALLOW_UNSAFE_LINKS',
                allowUnsafeLinks: Boolean(response.allowUnsafeLinks),
            });
        };
        if (!bots) {
            fetchBots();
        }
    }, [currentUserId, bots, dispatch]);

    const activeBot = useMemo(() => resolveActiveBot(bots, selectedAgentId), [bots, selectedAgentId]);

    // Persists an explicit user selection to the shared core preference (never
    // during auto-resolution). The dispatch keeps other surfaces in sync.
    const setActiveBot = useCallback((bot: LLMBot | null) => {
        if (!bot || !currentUserId) {
            return;
        }
        const preference: PreferenceType = {
            user_id: currentUserId,
            category: PREFERENCE_CATEGORY_AGENTS,
            name: PREFERENCE_NAME_SELECTED_AGENT,
            value: bot.id,
        };
        dispatch({type: 'RECEIVED_PREFERENCES', data: [preference]});
        savePreferences(currentUserId, [preference]).catch(() => { /* best effort */ });
    }, [currentUserId, dispatch]);

    return {bots, activeBot, setActiveBot};
};

// useBotlistForChannel only shows bots the user is allowed to use in a specific channel. Also returns if bots were filtered for showing
// a sorry no bots message.
export const useBotlistForChannel = (channelId: string) => {
    const {bots, setActiveBot} = useBotlist();
    const selectedAgentId = useSelector(getSelectedAgentId);

    const filteredBots = useMemo(() => {
        if (!bots) {
            return [];
        }
        return bots.filter((bot: LLMBot) => {
            const channelIDs = bot.channelIDs ?? [];
            return bot.channelAccessLevel === ChannelAccessLevel.All ||
				(bot.channelAccessLevel === ChannelAccessLevel.Allow && channelIDs.includes(channelId)) ||
				(bot.channelAccessLevel === ChannelAccessLevel.Block && !channelIDs.includes(channelId));
        });
    }, [bots, channelId]);

    // Within a channel the preferred/default bot may be disallowed, so resolve
    // against the filtered list. This auto-fallback is not persisted.
    const activeBot = useMemo(() => resolveActiveBot(filteredBots, selectedAgentId), [filteredBots, selectedAgentId]);

    const wasFiltered = Boolean(bots) && (filteredBots.length !== bots?.length);

    return {bots: filteredBots, activeBot, setActiveBot, wasFiltered};
};
