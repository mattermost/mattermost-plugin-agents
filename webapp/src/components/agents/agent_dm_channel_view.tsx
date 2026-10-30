// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import {useDispatch, useSelector, useStore} from 'react-redux';
import styled from 'styled-components';

import type {Channel} from '@mattermost/types/channels';
import {GlobalState} from '@mattermost/types/store';
import MessageTextOutlineIcon from '@mattermost/compass-icons/components/message-text-outline';
import {Button} from '@mattermost/compass-ui/components/button';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {Scrollbar} from '@mattermost/compass-ui/components/scrollbar';
import {Spinner} from '@mattermost/compass-ui/components/spinner';

import {dmOtherUserId, setAgentDMRawView} from '@/agent_dm_view';
import {useBotlist} from '@/bots';
import {getPost} from '@/client';

import AgentChatPage from './agent_chat_page';
import {AgentThreadsSection} from './product_sidebar';
import {ALL_AGENTS_PATH, buildChatPath, getHistory} from './use_agents_route';

// The props the host passes a registerChannelViewComponent component.
export interface AgentDMChannelViewProps {
    channel: Channel;
    channelId: string;
    teamId: string;

    // A permalinked post in this DM (`/:team/messages/@agent/:postid`). The host keeps it after the
    // id leaves the URL, until the channel changes.
    focusedPostId?: string;
}

// The Channels route of a DM, with an optional post id: `/:team/messages/:identifier(/:postid)`.
// Anything else (the channel popout's `/_popout/channel/...`) is not rewritten.
const DM_PATH = /^(\/[^/]+\/messages\/[^/]+)(?:\/[^/]+)?\/?$/;

// This DM's Channels URL without a post id, or '' when the current location is not one.
export const dmChannelPath = (pathname: string | undefined): string => {
    if (!pathname || pathname.startsWith('/_popout/')) {
        return '';
    }
    return DM_PATH.exec(pathname)?.[1] ?? '';
};

// The DM URL for another agent (its username), on the same team as `dmPath`.
const otherAgentDMPath = (dmPath: string, username: string): string => {
    const team = dmPath.split('/')[1];
    return `/${team}/messages/@${encodeURIComponent(username)}`;
};

// What the chat pane shows: a chat (its root post id), a new chat (undefined), or, while a
// permalinked post is being looked up, nothing yet.
type Selection = {resolving: true} | {resolving: false; rootId?: string};

// An agent's DM in the Channels app, shown as the Agents product's chat: the agent's chats in a
// column, the open chat (or a new one) beside it. The host keeps its channel header, LHS and RHS
// around it, and marks the DM read when this mounts. "View as messages" under the chats switches this
// DM back to the host's post list for the session (the channel header then offers the way back).
//
// The open chat is this component's state. A permalink (focusedPostId) opens that post's chat; a
// later pick by the user wins over it. Picking a chat also puts its root in the URL
// (`/:team/messages/@agent/:rootId`), which the host's channel route accepts without reloading the
// channel, so the chat survives a reload and can be linked; a new chat drops it.
const AgentDMChannelView = ({channel, focusedPostId}: AgentDMChannelViewProps) => {
    const intl = useIntl();
    const dispatch = useDispatch();
    const store = useStore<GlobalState>();
    const currentUserId = useSelector<GlobalState, string>((state) => state.entities.users.currentUserId);
    const {bots} = useBotlist();
    const botId = dmOtherUserId(channel.name, currentUserId);
    const bot = useMemo(() => bots?.find((b) => b.id === botId) ?? null, [bots, botId]);
    const listBots = useMemo(() => (bot ? [bot] : null), [bot]);

    const [selection, setSelection] = useState<Selection>(focusedPostId ? {resolving: true} : {resolving: false});

    // Bumped by every selection, so a permalink lookup that resolves after a newer one is dropped.
    const selectionSeq = useRef(0);

    useEffect(() => {
        if (!focusedPostId) {
            return;
        }
        const seq = ++selectionSeq.current;
        const select = (rootId: string) => {
            if (seq === selectionSeq.current) {
                setSelection({resolving: false, rootId});
            }
        };

        // The chat is the post's thread. Whether it belongs to this agent's DM is checked by the chat
        // pane, which fails closed.
        const cached = store.getState().entities.posts.posts[focusedPostId];
        if (cached) {
            select(cached.root_id || cached.id);
            return;
        }
        setSelection({resolving: true});
        getPost(focusedPostId).then((post) => select(post.root_id || post.id)).catch((error: unknown) => {
            console.error('Failed to load the linked agent chat post:', error); // eslint-disable-line no-console
            select(focusedPostId);
        });
    }, [focusedPostId, store]);

    const toChat = useCallback((targetBotId: string, rootId?: string) => {
        const history = getHistory();
        const dmPath = dmChannelPath(history?.location?.pathname);

        // Another agent's chat: that agent's DM (or the Agents product outside the Channels route).
        if (targetBotId !== botId) {
            const target = bots?.find((b) => b.id === targetBotId);
            history?.push(dmPath && target ? otherAgentDMPath(dmPath, target.username) : buildChatPath(targetBotId, rootId));
            return;
        }

        selectionSeq.current++;
        setSelection({resolving: false, rootId});
        const path = rootId ? `${dmPath}/${encodeURIComponent(rootId)}` : dmPath;
        if (dmPath && path !== history?.location?.pathname) {
            history?.push(path);
        }
    }, [botId, bots]);

    const toAll = useCallback(() => getHistory()?.push(ALL_AGENTS_PATH), []);

    const rootId = selection.resolving ? '' : selection.rootId ?? '';
    const listLabel = intl.formatMessage({defaultMessage: 'Chats'});

    return (
        <Layout data-testid='agent-dm-channel-view'>
            <ListColumn aria-label={intl.formatMessage({defaultMessage: 'Your chats with {name}'}, {name: bot?.displayName ?? ''})}>
                <ListScroll color='--sidebar-text-rgb'>
                    <AgentThreadsSection
                        bots={listBots}
                        activeBotId={botId}
                        activeRootId={rootId}
                        onSelectThread={toChat}
                        onNewChat={toChat}
                        title={listLabel}
                    />
                </ListScroll>
                <ListFooter>
                    <Button
                        emphasis='tertiary'
                        appearance='inverted'
                        size='x-small'
                        leadingIcon={<Icon glyph={<MessageTextOutlineIcon/>}/>}
                        data-testid='agent-dm-view-as-messages'
                        onClick={() => dispatch(setAgentDMRawView(channel.id, true))}
                    >
                        {intl.formatMessage({defaultMessage: 'View as messages'})}
                    </Button>
                </ListFooter>
            </ListColumn>
            <ChatColumn>
                {selection.resolving ? (
                    <Centered>
                        <Spinner
                            size='20'
                            aria-label={intl.formatMessage({defaultMessage: 'Opening chat…'})}
                        />
                    </Centered>
                ) : (
                    <AgentChatPage
                        botId={botId}
                        rootId={rootId}
                        toChat={toChat}
                        toAll={toAll}
                        compactHeader={true}
                    />
                )}
            </ChatColumn>
        </Layout>
    );
};

const Layout = styled.div`
    display: flex;
    flex: 1 1 auto;
    flex-direction: row;
    min-width: 0;
    min-height: 0;
    overflow: hidden;
`;

// The product sidebar's width (264px: 48px * 5.5) and colors, which the chat list is drawn for.
const ListColumn = styled.nav`
    display: flex;
    flex: 0 0 calc(var(--spacing-xxxxxl) * 5.5);
    flex-direction: column;
    width: calc(var(--spacing-xxxxxl) * 5.5);
    min-height: 0;
    padding-top: var(--spacing-s);
    border-right: var(--border-light);
    background: var(--sidebar-bg);
    color: var(--sidebar-text);
`;

const ListScroll = styled(Scrollbar)`
    flex: 1 1 auto;
    min-height: 0;
`;

const ListFooter = styled.div`
    display: flex;
    flex: 0 0 auto;
    padding: var(--spacing-s) var(--spacing-l);
`;

const ChatColumn = styled.div`
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    min-width: 0;
    min-height: 0;
`;

const Centered = styled.div`
    display: flex;
    flex: 1 1 auto;
    align-items: center;
    justify-content: center;
`;

export default AgentDMChannelView;
