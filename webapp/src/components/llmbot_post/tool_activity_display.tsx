// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useLayoutEffect, useRef, useState} from 'react';
import {FormattedMessage} from 'react-intl';
import styled, {css, keyframes} from 'styled-components';

import {ChevronRightIcon} from '@mattermost/compass-icons/components';

import {toolDisplayName} from '@/utils/tool_names';

import ToolApprovalSet, {needsViewerDecision} from '../tool_approval_set';
import ToolStatusIcon from '../tool_status_icon';
import {ToolApprovalStage, ToolCallStatus} from '../tool_types';

import {ActivityItem, PostActivity} from './activity_items';
import {Round} from './turn_content_utils';

// Length of the roll-in/roll-out transition, and the slightly longer window
// after which the outgoing row is unmounted.
const SLOT_ANIM_MS = 240;
const SLOT_CLEAR_MS = 300;

// How long each item stays on screen while the row catches up to a burst of
// new items, and how far behind it may fall before jumping to the end.
const SLOT_STEP_MS = 260;
const MAX_CATCHUP = 3;

// Pseudo-item shown once the response has finished: "Used 5 tools".
const SUMMARY_KEY = '__summary__';

/**
 * Walks `sequence` one entry at a time so a burst of new activity items still
 * reads as a sequence rather than a jump. Returns the key that should be on
 * screen right now.
 */
function useSlotSequence(sequence: string[]): string {
    const [displayed, setDisplayed] = useState<string>(() => sequence[sequence.length - 1] ?? '');

    // Read through a ref so streaming text (which rebuilds the array without
    // changing any key) does not restart the step timer.
    const sequenceRef = useRef(sequence);
    sequenceRef.current = sequence;
    const sequenceKey = sequence.join('\u0000');

    useEffect(() => {
        let timer: ReturnType<typeof setTimeout> | null = null;
        const seq = sequenceRef.current;
        const targetIdx = seq.length - 1;
        const currentIdx = seq.indexOf(displayed);

        if (targetIdx >= 0) {
            if (currentIdx === -1 || targetIdx - currentIdx > MAX_CATCHUP) {
                // Either the list was rebuilt under us (persisted rounds
                // replacing live ones, which changes every key) or updates
                // outran the animation. Jump rather than run a long backlog.
                setDisplayed(seq[targetIdx]);
            } else if (currentIdx < targetIdx) {
                timer = setTimeout(() => setDisplayed(seq[currentIdx + 1]), SLOT_STEP_MS);
            }
        }

        return () => {
            if (timer) {
                clearTimeout(timer);
            }
        };
    }, [sequenceKey, displayed]);

    return displayed;
}

/**
 * Tracks the key that is rolling out of view, or null when nothing is. Runs
 * as a layout effect so the incoming row is never painted in its final
 * position before the animation starts.
 */
function useSlotTransition(displayedKey: string): string | null {
    const [outgoingKey, setOutgoingKey] = useState<string | null>(null);
    const shownKeyRef = useRef(displayedKey);

    useLayoutEffect(() => {
        let timer: ReturnType<typeof setTimeout> | null = null;

        if (shownKeyRef.current !== displayedKey) {
            // A change mid-animation replaces the outgoing row instead of
            // queueing another one, so rapid updates jump-cut cleanly.
            setOutgoingKey(shownKeyRef.current);
            shownKeyRef.current = displayedKey;
            timer = setTimeout(() => setOutgoingKey(null), SLOT_CLEAR_MS);
        }

        return () => {
            if (timer) {
                clearTimeout(timer);
            }
        };
    }, [displayedKey]);

    return outgoingKey;
}

function summaryStatus(activity: PostActivity): ToolCallStatus {
    if (activity.hasError) {
        return ToolCallStatus.Error;
    }
    if (activity.hasRejected) {
        return ToolCallStatus.Rejected;
    }
    return ToolCallStatus.Success;
}

interface ToolActivityDisplayProps {
    activity: PostActivity;
    expanded: boolean;
    onToggleExpanded: (expanded: boolean) => void;

    /** True while the response is still being generated. */
    inProgress: boolean;

    /** Renders one round of the expanded stack; supplied by the post. */
    renderRound: (round: Round, index: number) => React.ReactNode;

    postID: string;
    conversationID?: string;
    canApprove: boolean;
    canExpand: boolean;

    /** Approval stage of the last activity round. */
    approvalStage: ToolApprovalStage;
}

/**
 * The collapsed "current activity" area of a bot post. Collapsed it is a
 * single row showing the activity item in flight (or, once the response is
 * done, a summary of the tools used); expanded it is the full stack of
 * intermediate rounds. Approval controls stay visible either way.
 *
 * The row is only rendered when it stands for something the viewer cannot
 * already see, so a post whose sole activity is the tool call awaiting a
 * decision shows just the approval card.
 */
const ToolActivityDisplay: React.FC<ToolActivityDisplayProps> = (props) => {
    const {activity, expanded, inProgress} = props;

    const showSummary = !inProgress && !activity.hasRunningTool;

    // Only the last activity round can still be awaiting a decision; earlier
    // rounds were resolved before the response moved on.
    const approvalRound = activity.activityRounds.length > 0 ?
        activity.activityRounds[activity.activityRounds.length - 1] :
        null;
    const showCollapsedApproval = !expanded && approvalRound !== null &&
        needsViewerDecision(approvalRound.toolCalls, props.approvalStage, props.canApprove);

    // The approval card renders that round in full right below the row, so
    // leaving its items in the row would just repeat the tool name.
    const rowItems = showCollapsedApproval ?
        activity.items.filter((item) => item.roundId !== approvalRound.id) :
        activity.items;

    const sequence = rowItems.map((item) => item.id);
    if (showSummary) {
        sequence.push(SUMMARY_KEY);
    }

    const itemsByKey = new Map<string, ActivityItem>(rowItems.map((item) => [item.id, item]));

    const displayedKey = useSlotSequence(sequence);
    const outgoingKey = useSlotTransition(displayedKey);

    if (activity.items.length === 0 || approvalRound === null) {
        return null;
    }

    const renderRowContent = (key: string): React.ReactNode => {
        if (key === SUMMARY_KEY) {
            return (
                <>
                    <ToolStatusIcon status={summaryStatus(activity)}/>
                    <ActivityLabel>
                        <FormattedMessage
                            id='ai.activity.tools_used'
                            defaultMessage='Used {count, plural, one {# tool} other {# tools}}'
                            values={{count: activity.toolCount}}
                        />
                    </ActivityLabel>
                </>
            );
        }

        const item = itemsByKey.get(key);
        if (!item) {
            return null;
        }

        switch (item.kind) {
        case 'text':
            return <ActivityLabel>{item.text}</ActivityLabel>;
        case 'tool':
            return (
                <>
                    <ToolStatusIcon status={item.toolCall.status}/>
                    <ActivityLabel>{toolDisplayName(item.toolCall.name)}</ActivityLabel>
                </>
            );
        default: {
            const exhaustive: never = item;
            return exhaustive;
        }
        }
    };

    return (
        <ActivityContainer data-testid='llm-bot-tool-activity'>
            {sequence.length > 0 && (
                <ActivityHeader
                    data-testid='llm-bot-tool-activity-header'
                    onClick={() => props.onToggleExpanded(!expanded)}
                >
                    <ActivityChevron $expanded={expanded}>
                        <ChevronRightIcon/>
                    </ActivityChevron>
                    <SlotViewport>
                        {outgoingKey !== null && (
                            <SlotRow
                                key={`out-${outgoingKey}`}
                                $phase='out'
                                aria-hidden={true}
                            >
                                {renderRowContent(outgoingKey)}
                            </SlotRow>
                        )}
                        <SlotRow
                            key={displayedKey}
                            $phase={outgoingKey === null ? 'static' : 'in'}
                            data-testid='llm-bot-tool-activity-current'
                        >
                            {renderRowContent(displayedKey)}
                        </SlotRow>
                    </SlotViewport>
                </ActivityHeader>
            )}

            {expanded && (
                <ExpandedRounds data-testid='llm-bot-tool-activity-rounds'>
                    {activity.activityRounds.map(props.renderRound)}
                </ExpandedRounds>
            )}

            {showCollapsedApproval && (
                <ToolApprovalSet
                    postID={props.postID}
                    conversationID={props.conversationID}
                    toolCalls={approvalRound.toolCalls}
                    approvalStage={props.approvalStage}
                    canApprove={props.canApprove}
                    canExpand={props.canExpand}
                    showArguments={approvalRound.toolCalls.some((tc) => tc.arguments != null)}
                    showResults={approvalRound.toolCalls.some((tc) => tc.result != null)}
                />
            )}
        </ActivityContainer>
    );
};

export default ToolActivityDisplay;

const ActivityContainer = styled.div`
    margin: 4px 0;
`;

const ActivityHeader = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.75);
    cursor: pointer;
    user-select: none;

    &:hover {
        color: rgba(var(--center-channel-color-rgb), 0.8);
    }
`;

const ActivityChevron = styled.div<{$expanded: boolean}>`
    display: flex;
    align-items: center;
    justify-content: center;
    width: 16px;
    height: 16px;
    flex-shrink: 0;
    transition: transform 0.2s ease;
    transform: ${(props) => (props.$expanded ? 'rotate(90deg)' : 'rotate(0)')};

    svg {
        width: 14px;
        height: 14px;
    }
`;

const SlotViewport = styled.div`
    position: relative;
    flex: 1;
    min-width: 0;
    height: 20px;
    overflow: hidden;
`;

const rollIn = keyframes`
    from {
        transform: translateY(100%);
        opacity: 0;
    }
    to {
        transform: translateY(0);
        opacity: 1;
    }
`;

const rollOut = keyframes`
    from {
        transform: translateY(0);
        opacity: 1;
    }
    to {
        transform: translateY(-100%);
        opacity: 0;
    }
`;

const SlotRow = styled.div<{$phase: 'in' | 'out' | 'static'}>`
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    gap: 8px;
    line-height: 20px;

    ${(props) => props.$phase === 'in' && css`
        animation: ${rollIn} ${SLOT_ANIM_MS}ms ease-out;
    `}

    ${(props) => props.$phase === 'out' && css`
        animation: ${rollOut} ${SLOT_ANIM_MS}ms ease-in forwards;
    `}

    @media (prefers-reduced-motion: reduce) {
        animation: none;
    }
`;

const ActivityLabel = styled.span`
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
`;

const ExpandedRounds = styled.div`
    margin-top: 8px;
`;
