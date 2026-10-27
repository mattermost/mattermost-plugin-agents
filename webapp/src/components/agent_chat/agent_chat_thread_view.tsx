// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useRef} from 'react';
import {useIntl} from 'react-intl';
import styled, {css, keyframes} from 'styled-components';

import type {LLMBot} from '@/bots';
import {ThreadViewer as UnstyledThreadViewer} from '@/mm_webapp';

import AgentChatComposer from './agent_chat_composer';
import JumpToLatest from './jump_to_latest';
import {useThreadGenerating} from './use_thread_generating';
import {useThreadScroll} from './use_thread_scroll';
import {AGENT_CHAT_SURFACE, AgentChatSurfaceContext} from './agent_chat_surface_context';
import {CHAT_COLUMN_PADDING, CHAT_CONTENT_MAX_WIDTH, CHAT_PANEL_PADDING, hostThreadChatStyles} from './agent_chat_host_styles';
import type {AgentNewChatLayout} from './agent_new_chat';

interface AgentChatThreadViewProps {

    // The thread's DM channel; '' while unknown (the composer waits for it).
    channelId: string;
    rootId: string;
    agentName: string;

    // The thread's agent, for the composer's "+" menu; null when none is known.
    bot?: LLMBot | null;

    // 'page': the product view's centered 800px column. 'panel': fills the RHS.
    layout: AgentNewChatLayout;

    // Passed through to the host ThreadViewer.
    isThreadView: boolean;
    testId?: string;
    viewerTestId?: string;
}

export const isThreadViewerAvailable = Boolean(UnstyledThreadViewer);

// One agent thread as a chat: the host ThreadViewer renders the posts (as
// bubbles, through hostThreadChatStyles) and the plugin's pill composer,
// pinned below it, replaces the host reply box.
const AgentChatThreadView = ({channelId, rootId, agentName, bot, layout, isThreadView, testId, viewerTestId}: AgentChatThreadViewProps) => {
    const intl = useIntl();
    const viewerRef = useRef<HTMLDivElement>(null);
    const {scrolledUp, scrollToBottom} = useThreadScroll(viewerRef);
    const generating = useThreadGenerating(channelId, rootId) !== null;

    if (!ThreadViewer) {
        return null;
    }

    // Posts in this view leave Stop to the composer below them.
    return (
        <AgentChatSurfaceContext.Provider value={AGENT_CHAT_SURFACE}>
            <ThreadPane
                $layout={layout}
                data-testid={testId}
            >
                <ViewerArea ref={viewerRef}>
                    <ThreadViewer
                        data-testid={viewerTestId}
                        rootPostId={rootId}
                        isThreadView={isThreadView}
                        useRelativeTimestamp={false}
                        inputPlaceholder={intl.formatMessage({defaultMessage: 'Reply…'})}
                    />
                    <JumpToLatest
                        visible={scrolledUp}
                        responding={generating}
                        agentName={agentName}
                        onClick={scrollToBottom}
                    />
                </ViewerArea>
                {channelId && (
                    <ComposerArea $layout={layout}>
                        <AgentChatComposer
                            key={rootId}
                            channelId={channelId}
                            rootId={rootId}
                            agentName={agentName}
                            bot={bot}
                        />
                    </ComposerArea>
                )}
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

// Definite height, no own scroll: the host ThreadViewer brings its own scroller.
const ViewerArea = styled.div`
    position: relative;
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    min-height: 0;
`;

// The RHS uses the same 24px sides as its new-chat state, so the two line up.
const ComposerArea = styled.div<{$layout: AgentNewChatLayout}>`
    flex: 0 0 auto;
    width: 100%;
    padding: 0 ${CHAT_PANEL_PADDING} ${CHAT_PANEL_PADDING} ${CHAT_PANEL_PADDING};

    ${({$layout}) => $layout === 'page' && css`
        box-sizing: border-box;

        /* The messages' column: the same inset, cap and side padding, so the edges line up. */
        width: calc(100% - 2 * var(--spacing-m));
        max-width: ${CHAT_CONTENT_MAX_WIDTH};
        margin-inline: auto;
        padding-inline: ${CHAT_COLUMN_PADDING};
        padding-bottom: var(--spacing-xxl);
    `}
`;

export default AgentChatThreadView;
