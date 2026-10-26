// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';
import styled, {css, keyframes} from 'styled-components';

import {ThreadViewer as UnstyledThreadViewer} from '@/mm_webapp';

import AgentChatComposer from './agent_chat_composer';
import {CHAT_CONTENT_MAX_WIDTH, hostThreadChatStyles} from './agent_chat_host_styles';
import type {AgentNewChatLayout} from './agent_new_chat';

interface AgentChatThreadViewProps {

    // The thread's DM channel; '' while unknown (the composer waits for it).
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

export const isThreadViewerAvailable = Boolean(UnstyledThreadViewer);

// One agent thread as a chat: the host ThreadViewer renders the posts (as
// bubbles, through hostThreadChatStyles) and the plugin's pill composer,
// pinned below it, replaces the host reply box.
const AgentChatThreadView = ({channelId, rootId, agentName, layout, isThreadView, testId, viewerTestId}: AgentChatThreadViewProps) => {
    const intl = useIntl();

    if (!ThreadViewer) {
        return null;
    }

    return (
        <ThreadPane
            $layout={layout}
            data-testid={testId}
        >
            <ViewerArea>
                <ThreadViewer
                    data-testid={viewerTestId}
                    rootPostId={rootId}
                    isThreadView={isThreadView}
                    useRelativeTimestamp={false}
                    inputPlaceholder={intl.formatMessage({defaultMessage: 'Reply…'})}
                />
            </ViewerArea>
            {channelId && (
                <ComposerArea $layout={layout}>
                    <AgentChatComposer
                        key={rootId}
                        channelId={channelId}
                        rootId={rootId}
                        agentName={agentName}
                    />
                </ComposerArea>
            )}
        </ThreadPane>
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
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    min-height: 0;
`;

// The RHS uses the same 24px sides as its new-chat state, so the two line up.
const ComposerArea = styled.div<{$layout: AgentNewChatLayout}>`
    flex: 0 0 auto;
    width: 100%;
    padding: var(--spacing-xs) var(--spacing-xxl) var(--spacing-xxl);

    ${({$layout}) => $layout === 'page' && css`
        max-width: ${CHAT_CONTENT_MAX_WIDTH};
        margin-inline: auto;
    `}
`;

export default AgentChatThreadView;
