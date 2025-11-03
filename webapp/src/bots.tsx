// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useState, useEffect} from 'react';

import {useDispatch, useSelector} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';

import {getAIBots} from '@/client';

import manifest from './manifest';
import {BotsHandler} from './redux';
import {ChannelAccessLevel, UserAccessLevel} from './components/system_console/bot';

export interface LLMBot {
    id: string;
    displayName: string;
    username: string;
    lastIconUpdate: number;
    dmChannelID: string;
    channelAccessLevel: ChannelAccessLevel;
    channelIDs: string[];
    userAccessLevel: UserAccessLevel;
    userIDs: string[];
    teamIDs: string[];
}

const defaultBotLocalStorageKey = 'defaultBot';

export const useBotlist = () => {
    const bots = useSelector<GlobalState, LLMBot[] | null>((state: any) => state['plugins-' + manifest.id].bots);
    const [activeBot, setActiveBot] = useState<LLMBot | null>(null);
    const currentUserId = useSelector<GlobalState, string>((state) => state.entities.users.currentUserId);
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

    useEffect(() => {
        const defaultActiveBotName = localStorage.getItem(defaultBotLocalStorageKey);
        setActiveBot(bots?.find((bot: LLMBot) => bot.username === defaultActiveBotName) || bots?.[0] || null);
    }, [bots]);

    useEffect(() => {
        if (!activeBot) {
            return;
        }
        localStorage.setItem(defaultBotLocalStorageKey, activeBot.username);
    }, [activeBot]);

    return {bots, activeBot, setActiveBot};
};

// useBotlistForChannel only shows bots the user is allowed to use in a specific channel. Also returns if bots were filtered for showing
// a sorry no bots message.
export const useBotlistForChannel = (channelId: string) => {
    const {bots, activeBot, setActiveBot} = useBotlist();
    const [filteredBots, setFilteredBots] = useState<LLMBot[]>([]);

    useEffect(() => {
        if (!bots) {
            return;
        }

        const filtered = bots.filter((bot: LLMBot) => {
            return bot.channelAccessLevel === ChannelAccessLevel.All ||
				(bot.channelAccessLevel === ChannelAccessLevel.Allow && bot.channelIDs.includes(channelId)) ||
				(bot.channelAccessLevel === ChannelAccessLevel.Block && !bot.channelIDs.includes(channelId));
        });

        setFilteredBots(filtered);
        if (!filtered.find((bot) => bot.username === activeBot?.username)) {
            setActiveBot(filtered[0] || null);
        }
    }, [bots, channelId, activeBot, setActiveBot]);

    const wasFiltered = bots && (filteredBots.length !== bots.length);

    return {bots: filteredBots, activeBot, setActiveBot, wasFiltered};
};
