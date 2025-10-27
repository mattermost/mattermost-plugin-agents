// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useEffect, useCallback} from 'react';
import {useIntl} from 'react-intl';
import {useDispatch, useSelector} from 'react-redux';
import styled from 'styled-components';

import {GlobalState} from '@mattermost/types/store';

import manifest from '@/manifest';

import {getAIThreads, updateRead} from '@/client';

import {useBotlist} from '@/bots';

import {ThreadViewer as UnstyledThreadViewer} from '@/mm_webapp';

import ThreadItem from './thread_item';
import RHSHeader from './rhs_header';
import RHSNewTab from './rhs_new_tab';

const ThreadViewer = UnstyledThreadViewer && styled(UnstyledThreadViewer)`
    height: 100%;
`;

const ThreadsList = styled.div`
    overflow-y: scroll;
`;

const RhsContainer = styled.div`
    height: 100%;
    display: flex;
    flex-direction: column;
`;

export interface AIThread {
    id: string;
    message: string;
    channel_id: string;
    title: string;
    reply_count: number;
    update_at: number;
}

const twentyFourHoursInMS = 24 * 60 * 60 * 1000;

export default function RHS() {
    const dispatch = useDispatch();
    const intl = useIntl();
    const [currentTab, setCurrentTab] = useState('new');
    const selectedPostId = useSelector((state: any) => state['plugins-' + manifest.id].selectedPostId);
    const currentUserId = useSelector<GlobalState, string>((state) => state.entities.users.currentUserId);
    const currentTeamId = useSelector<GlobalState, string>((state) => state.entities.teams.currentTeamId);

    const [threads, setThreads] = useState<AIThread[] | null>(null);

    useEffect(() => {
        const fetchThreads = async () => {
            setThreads(await getAIThreads());
        };
        if (currentTab === 'threads') {
            fetchThreads();
        } else if (currentTab === 'thread' && Boolean(selectedPostId)) {
            // Update read for the thread to tomorrow. We don't really want the unreads thing to show up.
            updateRead(currentUserId, currentTeamId, selectedPostId, Date.now() + twentyFourHoursInMS);
        }
        return () => {
            // Sometimes we are too fast for the server, so try again on unmount/switch.
            if (selectedPostId) {
                updateRead(currentUserId, currentTeamId, selectedPostId, Date.now() + twentyFourHoursInMS);
            }
        };
    }, [currentTab, selectedPostId]);

    const selectPost = useCallback((postId: string) => {
        dispatch({type: 'SELECT_AI_POST', postId});
    }, [dispatch]);

    const {bots, activeBot, setActiveBot} = useBotlist();

    // No bots available - hide the RHS entirely
    if (bots && bots.length === 0) {
        return null;
    }

    let content = null;
    if (selectedPostId) {
        if (currentTab !== 'thread') {
            setCurrentTab('thread');
        }
        content = (
            <ThreadViewer
                data-testid='rhs-thread-viewer'
                inputPlaceholder={intl.formatMessage({defaultMessage: 'Reply...'})}
                rootPostId={selectedPostId}
                useRelativeTimestamp={false}
                isThreadView={false}
            />
        );
    } else if (currentTab === 'threads') {
        if (threads && bots) {
            content = (
                <ThreadsList
                    data-testid='rhs-threads-list'
                >
                    {threads.map((p) => (
                        <ThreadItem
                            key={p.id}
                            postTitle={p.title}
                            postMessage={p.message}
                            repliesCount={p.reply_count}
                            lastActivityDate={p.update_at}
                            label={bots.find((bot) => bot.dmChannelID === p.channel_id)?.displayName ?? ''}
                            onClick={() => {
                                setCurrentTab('thread');
                                selectPost(p.id);
                            }}
                        />))}
                </ThreadsList>
            );
        } else {
            content = null;
        }
    } else if (currentTab === 'new') {
        content = (
            <RHSNewTab
                data-testid='rhs-new-tab'
                setCurrentTab={setCurrentTab}
                selectPost={selectPost}
                activeBot={activeBot}
            />
        );
    }
    return (
        <RhsContainer
            data-testid='mattermost-ai-rhs'
        >
            <RHSHeader
                currentTab={currentTab}
                setCurrentTab={setCurrentTab}
                selectPost={selectPost}
                bots={bots}
                activeBot={activeBot}
                setActiveBot={setActiveBot}
            />
            {content}
        </RhsContainer>
    );
}
