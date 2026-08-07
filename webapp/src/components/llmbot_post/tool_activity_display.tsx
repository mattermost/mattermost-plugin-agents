// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useLayoutEffect, useRef, useState} from 'react';
import {FormattedMessage} from 'react-intl';
import styled, {css, keyframes} from 'styled-components';

import {ChevronRightIcon} from '@mattermost/compass-icons/components';

import {toolDisplayName} from '@/utils/tool_names';

import ToolStatusIcon from '../tool_status_icon';
import {ToolCallStatus} from '../tool_types';

import {PostActivity} from './activity_items';
import {CollapseChevron, CollapseHeaderRow} from './collapse_header';
import {noMotionWhenReduced} from './motion';
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

    // The effect re-runs on every append. Timing the wait from the last step
    // rather than from the re-run keeps a burst moving; otherwise each new
    // item would reset the clock and the row would sit still until the
    // backlog grew past MAX_CATCHUP and skipped everything.
    const lastStepAtRef = useRef(Date.now());

    useEffect(() => {
        const targetIdx = sequenceRef.current.length - 1;
        const currentIdx = sequenceRef.current.indexOf(displayed);
        if (targetIdx < 0 || currentIdx === targetIdx) {
            return undefined; // eslint-disable-line no-undefined
        }

        if (currentIdx === -1 || targetIdx - currentIdx > MAX_CATCHUP) {
            // Either the list was rebuilt under us (persisted rounds replacing
            // live ones, which changes every key) or updates outran the
            // animation. Jump rather than run a long backlog.
            lastStepAtRef.current = Date.now();
            setDisplayed(sequenceRef.current[targetIdx]);
            return undefined; // eslint-disable-line no-undefined
        }

        const timer = setTimeout(() => {
            // Resolve the next key at fire time: more items may have arrived
            // while this timer was pending.
            const latest = sequenceRef.current;
            const idx = latest.indexOf(displayed);
            lastStepAtRef.current = Date.now();
            setDisplayed(idx === -1 || idx + 1 >= latest.length ? latest[latest.length - 1] : latest[idx + 1]);
        }, Math.max(0, SLOT_STEP_MS - (Date.now() - lastStepAtRef.current)));

        return () => clearTimeout(timer);
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

    /** True while the response is unfinished — still generating, or paused on a decision. */
    inProgress: boolean;

    /** Renders one round of the expanded stack; supplied by the post. */
    renderRound: (round: Round) => React.ReactNode;
}

/**
 * The collapsed "current activity" area of a bot post. Collapsed it is a
 * single row showing the activity item in flight (or, once the response is
 * done, a summary of the tools used); expanded it is the full stack of
 * intermediate rounds.
 *
 * Rounds the viewer owes a decision on never reach here — they render below
 * the activity area through the ordinary round path — so this component knows
 * nothing about approval.
 */
const ToolActivityDisplay: React.FC<ToolActivityDisplayProps> = (props) => {
    const {activity, expanded, inProgress} = props;

    // A tool can still be running after the stream stops, so completion is
    // not simply "not generating".
    const showSummary = !inProgress && !activity.hasRunningTool;

    const sequence = activity.items.map((item) => item.id);
    if (showSummary) {
        sequence.push(SUMMARY_KEY);
    }

    const displayedKey = useSlotSequence(sequence);
    const outgoingKey = useSlotTransition(displayedKey);

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

        const item = activity.items.find((candidate) => candidate.id === key);
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
            <CollapseHeaderRow
                data-testid='llm-bot-tool-activity-header'
                onClick={() => props.onToggleExpanded(!expanded)}
            >
                <CollapseChevron $expanded={expanded}>
                    <ChevronRightIcon/>
                </CollapseChevron>
                <SlotViewport>
                    {outgoingKey !== null && (
                        <SlotRow
                            key={`out-${outgoingKey}`}
                            $phase='out'
                            data-testid='llm-bot-tool-activity-outgoing'
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
            </CollapseHeaderRow>

            {expanded && (
                <ExpandedRounds data-testid='llm-bot-tool-activity-rounds'>
                    {activity.activityRounds.map(props.renderRound)}
                </ExpandedRounds>
            )}
        </ActivityContainer>
    );
};

export default ToolActivityDisplay;

const ActivityContainer = styled.div`
    margin: 4px 0;
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

    ${noMotionWhenReduced}
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
