// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useRef} from 'react';
import {useIntl} from 'react-intl';
import styled, {keyframes} from 'styled-components';

import {ThreadViewer as UnstyledThreadViewer} from '@/mm_webapp';

import JumpToLatest from './jump_to_latest';
import {useThreadGenerating} from './use_thread_generating';
import {useThreadScroll} from './use_thread_scroll';
import {AGENT_CHAT_SURFACE, AgentChatSurfaceContext} from './agent_chat_surface_context';
import {CHAT_CONTENT_MAX_WIDTH, hostThreadChatStyles} from './agent_chat_host_styles';
import type {AgentNewChatLayout} from './agent_new_chat';

interface AgentChatThreadViewProps {

    // The thread's channel; '' while unknown.
    channelId: string;
    rootId: string;
    agentName: string;

    // 'page': the product view's centered 800px column. 'panel': fills the RHS.
    layout: AgentNewChatLayout;

    // Passed through to the host ThreadViewer.
    isThreadView: boolean;
    testId?: string;
    viewerTestId?: string;
}

// The host renders a post's time as a permalink to the post, which in a chat only reloads the view
// the user is in. Its tooltip (the full date) stays; the click does nothing.
const TIME_PERMALINK_SELECTOR = 'a.post__permalink';
const blockTimePermalink = (e: React.MouseEvent) => {
    if ((e.target as Element).closest?.(TIME_PERMALINK_SELECTOR)) {
        e.preventDefault();
        e.stopPropagation();
    }
};

export const isThreadViewerAvailable = Boolean(UnstyledThreadViewer);

// One agent thread as a chat: the host ThreadViewer renders the posts (as
// bubbles, through hostThreadChatStyles) and its own reply composer under
// them, placed in the chat's column.
const AgentChatThreadView = ({channelId, rootId, agentName, layout, isThreadView, testId, viewerTestId}: AgentChatThreadViewProps) => {
    const intl = useIntl();
    const viewerRef = useRef<HTMLDivElement>(null);
    const {scrolledUp, scrollToBottom, bottomInset} = useThreadScroll(viewerRef);
    const generating = useThreadGenerating(channelId, rootId) !== null;

    if (!ThreadViewer) {
        return null;
    }

    return (
        <AgentChatSurfaceContext.Provider value={AGENT_CHAT_SURFACE}>
            <ThreadPane
                $layout={layout}
                data-testid={testId}
            >
                <ViewerArea
                    ref={viewerRef}
                    onClickCapture={blockTimePermalink}
                >
                    <ThreadViewer
                        data-testid={viewerTestId}
                        rootPostId={rootId}
                        isThreadView={isThreadView}
                        useRelativeTimestamp={false}
                        inputPlaceholder={agentName ? intl.formatMessage({defaultMessage: 'Reply to {name}'}, {name: agentName}) : intl.formatMessage({defaultMessage: 'Reply…'})}
                    />
                    <JumpToLatest
                        visible={scrolledUp}
                        responding={generating}
                        agentName={agentName}
                        bottomInset={bottomInset}
                        onClick={scrollToBottom}
                    />
                </ViewerArea>
            </ThreadPane>
        </AgentChatSurfaceContext.Provider>
    );
};

const fadeIn = keyframes`
    from { opacity: 0; }
    to { opacity: 1; }
`;

const ThreadViewer = UnstyledThreadViewer && styled(UnstyledThreadViewer)`
    height: 100%;
`;

const ThreadPane = styled.div<{$layout: AgentNewChatLayout}>`
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    min-height: 0;
    overflow: hidden;
    animation: ${fadeIn} var(--duration-moderate) var(--ease-entrance);

    @media (prefers-reduced-motion: reduce) {
        animation: none;
    }

    ${({$layout}) => hostThreadChatStyles($layout === 'page' ? {columnMaxWidth: CHAT_CONTENT_MAX_WIDTH} : {})}
`;

// Definite height, no own scroll: the host ThreadViewer brings its own scroller, and its reply
// composer under it.
const ViewerArea = styled.div`
    position: relative;
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    min-height: 0;
`;

export default AgentChatThreadView;
