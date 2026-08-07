// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';

import PostText from '../post_text';
import ToolApprovalSet from '../tool_approval_set';
import {ToolApprovalStage} from '../tool_types';

import {Round} from './turn_content_utils';
import {ReasoningDisplay} from './reasoning_display';

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

    /** Takes the round id so the post can pass one callback to every round. */
    onToggleReasoning: (roundId: string, collapsed: boolean) => void;
}

/**
 * One assistant round: reasoning, text, then the tool cards it produced.
 *
 * Memoized because a streaming response re-renders the post on every chunk
 * while only the live round's content actually changes.
 */
export const RoundView = React.memo((props: RoundViewProps) => {
    const {round} = props;
    return (
        <RoundContainer>
            {round.reasoning.summary !== '' && (
                <ReasoningDisplay
                    reasoningSummary={round.reasoning.summary}
                    isReasoningCollapsed={props.reasoningCollapsed}
                    isReasoningLoading={props.reasoningLoading}
                    onToggleCollapse={(collapsed) => props.onToggleReasoning(round.id, collapsed)}
                />
            )}
            {round.text !== '' && (
                <PostText
                    message={round.text}
                    channelID={props.channelID}
                    postID={props.postID}
                    showCursor={props.showCursor}
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

const RoundContainer = styled.div`
    & + & {
        margin-top: 8px;
    }
`;
