// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import {useSelector, useStore} from 'react-redux';
import styled, {createGlobalStyle} from 'styled-components';

import type {Channel} from '@mattermost/types/channels';
import {GlobalState} from '@mattermost/types/store';
import {Spinner} from '@mattermost/compass-ui/components/spinner';

import {dmOtherUserId} from '@/agent_dm_view';
import {useBotlist} from '@/bots';
import {getPost} from '@/client';

import AgentChatPage from './agent_chat_page';
import {dmChannelPath, useDMPostId} from './agent_dm_route';
import {ALL_AGENTS_PATH, buildChatPath, getHistory} from './use_agents_route';

// The props the host passes a registerChannelViewComponent component.
export interface AgentDMChannelViewProps {
    channel: Channel;
    channelId: string;
    teamId: string;

    // A permalinked post in this DM (`/:team/messages/@agent/:postid`). The host keeps it after the
    // id leaves the URL, until the channel changes; the view follows the URL itself where it can.
    focusedPostId?: string;
}

export {dmChannelPath};

// The DM URL for another agent (its username), on the same team as `dmPath`.
const otherAgentDMPath = (dmPath: string, username: string): string => {
    const team = dmPath.split('/')[1];
    return `/${team}/messages/@${encodeURIComponent(username)}`;
};

// What the chat pane shows: a chat (its root post id), a new chat (undefined), or, while a
// permalinked post is being looked up, nothing yet.
type Selection = {resolving: true} | {resolving: false; rootId?: string};

// An agent's DM in the Channels app, shown as the Agents product's chat under the chat's own title bar
// in place of the host's channel header. The host keeps its LHS and RHS around it, and marks the DM
// read when this mounts. The agent's chats are listed under its item in the host's sidebar.
//
// The open chat follows the DM URL (`/:team/messages/@agent/:postId`), which the host's channel route
// accepts without reloading the channel: the sidebar's chats and permalinks navigate to it, picking a
// chat here puts its root in it, and a new chat drops it, so the chat survives a reload and can be
// linked. A permalink may name a reply, whose chat is its thread. Where the location is not a Channels
// DM route (the channel popout) the view uses the host's focusedPostId and keeps the pick locally.
const AgentDMChannelViewForChannel = ({channel, focusedPostId}: AgentDMChannelViewProps) => {
    const intl = useIntl();
    const store = useStore<GlobalState>();
    const currentUserId = useSelector<GlobalState, string>((state) => state.entities.users.currentUserId);
    const {bots} = useBotlist();
    const botId = dmOtherUserId(channel.name, currentUserId);
    const urlPostId = useDMPostId();
    const postId = urlPostId ?? focusedPostId ?? '';

    const [selection, setSelection] = useState<Selection>(postId ? {resolving: true} : {resolving: false});

    // Bumped by every selection, so a permalink lookup that resolves after a newer one is dropped.
    const selectionSeq = useRef(0);

    // Picking a chat puts its root in the URL, which the host hands back as focusedPostId: that is
    // not a permalink to look up.
    const openRootId = useRef('');
    openRootId.current = selection.resolving ? '' : selection.rootId ?? '';

    const invalidate = useCallback(() => {
        selectionSeq.current++;
    }, []);

    useEffect(() => {
        if (!postId) {
            // A DM URL without a post is a new chat, wherever the navigation came from.
            if (urlPostId !== null) {
                setSelection((prev) => (prev.resolving || prev.rootId ? {resolving: false} : prev));
            }
            return invalidate;
        }
        if (postId === openRootId.current) {
            return invalidate;
        }
        const seq = ++selectionSeq.current;
        const select = (rootId: string) => {
            if (seq === selectionSeq.current) {
                setSelection({resolving: false, rootId});
            }
        };

        // The chat is the post's thread. Whether it belongs to this agent's DM is checked by the chat
        // pane, which fails closed.
        const cached = store.getState().entities.posts.posts[postId];
        if (cached) {
            select(cached.root_id || cached.id);
            return invalidate;
        }
        setSelection({resolving: true});
        getPost(postId).then((post) => select(post.root_id || post.id)).catch((error: unknown) => {
            console.error('Failed to load the linked agent chat post:', error); // eslint-disable-line no-console
            select(postId);
        });

        return invalidate;
    }, [postId, urlPostId, store, invalidate]);

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

    return (
        <Layout data-testid='agent-dm-channel-view'>
            <HideHostChannelHeader/>
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

// The host's channel header would sit above the chat's own title bar; the chat view replaces it
// while it is mounted (the host renders it for every channel view).
const HideHostChannelHeader = createGlobalStyle`
    #app-content > #channel-header {
        display: none;
    }
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

// Keyed by channel so that the open chat and any pending permalink lookup never carry over to another DM.
const AgentDMChannelView = (props: AgentDMChannelViewProps) => (
    <AgentDMChannelViewForChannel
        key={props.channel.id}
        {...props}
    />
);

export default AgentDMChannelView;
