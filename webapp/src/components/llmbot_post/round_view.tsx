// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';

import PostText, {TextReveal} from '../post_text';
import ToolApprovalSet from '../tool_approval_set';
import {ToolApprovalStage} from '../tool_types';

import {Round} from './turn_content_utils';
import {ReasoningDisplay} from './reasoning_display';
import ServerToolSet from './server_tool_set';

interface RoundViewProps {
    round: Round;
    postID: string;
    conversationID?: string;
    channelID: string;
    approvalStage: ToolApprovalStage;
    canApprove: boolean;
    canExpand: boolean;
    showCursor: boolean;
    reasoningLoading: boolean;
    reasoningCollapsed: boolean;

    /** Only on the live round in surfaces that smooth streaming text. */
    reveal?: TextReveal;

    /** Takes the round id so the post can pass one callback to every round. */
    onToggleReasoning: (roundId: string, collapsed: boolean) => void;
}

/**
 * One assistant round: reasoning, provider-executed tools, text, then client
 * tool cards. Memoized so settled rounds skip re-rendering on every chunk.
 */
export const RoundView = React.memo((props: RoundViewProps) => {
    const {round} = props;
    const wide = hasWideContent(round) ? true : undefined; // eslint-disable-line no-undefined
    return (
        <RoundContainer data-bubble-wide={wide}>
            {round.reasoning.summary !== '' && (
                <ReasoningDisplay
                    reasoningSummary={round.reasoning.summary}
                    isReasoningCollapsed={props.reasoningCollapsed}
                    isReasoningLoading={props.reasoningLoading}
                    onToggleCollapse={(collapsed) => props.onToggleReasoning(round.id, collapsed)}
                />
            )}
            {round.serverTools.length > 0 && (
                <ServerToolSet serverTools={round.serverTools}/>
            )}
            {round.text !== '' && (
                <PostText
                    message={round.text}
                    channelID={props.channelID}
                    postID={props.postID}
                    showCursor={props.showCursor}
                    reveal={props.reveal}
                    annotations={round.annotations.length > 0 ? round.annotations : undefined} // eslint-disable-line no-undefined
                />
            )}
            {round.toolCalls.length > 0 && (
                <ToolApprovalSet
                    postID={props.postID}
                    conversationID={props.conversationID}
                    toolCalls={round.toolCalls}
                    approvalStage={props.approvalStage}
                    canApprove={props.canApprove}
                    canExpand={props.canExpand}
                />
            )}
        </RoundContainer>
    );
});
RoundView.displayName = 'RoundView';

// Reasoning and tool UI need the full bubble width (use_bubble_width.ts).
function hasWideContent(round: Round): boolean {
    return round.reasoning.summary !== '' || round.serverTools.length > 0 || round.toolCalls.length > 0;
}

const RoundContainer = styled.div`
    & + & {
        margin-top: 8px;
    }
`;
