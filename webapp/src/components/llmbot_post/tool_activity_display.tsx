// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef, useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import styled, {css, keyframes} from 'styled-components';

import {ChevronRightIcon} from '@mattermost/compass-icons/components';

import {toolDisplayName} from '@/utils/tool_identity';

import ToolStatusIcon from '../tool_status_icon';
import {ToolCallStatus} from '../tool_types';

import {ActivityItem, PostActivity, isTerminalToolStatus} from './activity_items';
import {CollapseChevron, CollapseHeaderRow} from './collapse_header';
import {noMotionWhenReduced, prefersReducedMotion} from './motion';
import {LoadingSpinner} from './reasoning_display';
import {RollingLine} from './rolling_line';
import {serverToolTitle} from './server_tool_set';
import {Round} from './turn_content_utils';

const EXPAND_MS = 200;

const SUMMARY_KEY = 'summary';

const LineSpinner = () => <LoadingSpinner data-testid='llm-bot-activity-spinner'/>;

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

    /** True while the response is unfinished: still generating, or paused on a decision. */
    inProgress: boolean;

    /** True while the server is actively producing the response. */
    working: boolean;

    /** A setup phase such as "Connecting to provider...", shown in place of the latest item. */
    statusMessage?: string;

    renderRound: (round: Round) => React.ReactNode;
}

/**
 * The status line of a bot post, which doubles as its collapsible activity
 * area. It shows setup progress until the first activity arrives, then the
 * latest tool invocation or reasoning block, and a "Used N tools" summary once
 * the response is done. Expanded, it frames the full stack of intermediate rounds.
 */
const ToolActivityDisplay: React.FC<ToolActivityDisplayProps> = (props) => {
    const {activity, inProgress, working, statusMessage} = props;
    const intl = useIntl();
    const expandable = activity.items.length > 0;
    const expanded = props.expanded && expandable;

    // Settled posts render statically; only a line that has been live rolls between states.
    const liveRef = useRef(false);
    if (working || inProgress) {
        liveRef.current = true;
    }

    const [closing, setClosing] = useState(false);
    useEffect(() => {
        if (!closing) {
            return undefined; // eslint-disable-line no-undefined
        }
        const timer = setTimeout(() => setClosing(false), EXPAND_MS);
        return () => clearTimeout(timer);
    }, [closing]);

    const toggle = () => {
        setClosing(expanded && !prefersReducedMotion());
        props.onToggleExpanded(!expanded);
    };

    // A tool can still be running after the stream stops.
    const showSummary = !inProgress && !activity.hasRunningTool;
    const current: ActivityItem | null = showSummary ? null : activity.items[activity.items.length - 1] ?? null;

    let lineKey: string;
    let indicator: React.ReactNode;
    let label: React.ReactNode;
    if (statusMessage) {
        lineKey = `status:${statusMessage}`;
        indicator = <LineSpinner/>;
        label = statusMessage;
    } else if (current === null) {
        lineKey = SUMMARY_KEY;
        indicator = <ToolStatusIcon status={summaryStatus(activity)}/>;
        label = (
            <FormattedMessage
                id='ai.activity.tools_used'
                defaultMessage='Used {count, plural, one {# tool} other {# tools}}'
                values={{count: activity.toolCount}}
            />
        );
    } else {
        lineKey = current.id;
        const running = working || !isTerminalToolStatus(current.status);
        indicator = running ? <LineSpinner/> : <ToolStatusIcon status={current.status}/>;
        switch (current.kind) {
        case 'reasoning':
            label = (
                <FormattedMessage
                    id='ai.activity.thinking'
                    defaultMessage='Thinking'
                />
            );
            break;
        case 'tool':
            label = toolDisplayName(current.toolCall);
            break;
        case 'server_tool':
            label = serverToolTitle(current.serverTool, intl);
            break;
        }
    }

    return (
        <ActivityContainer
            $expanded={expanded}
            data-testid={expandable ? 'llm-bot-tool-activity' : 'llm-bot-status-line'}
            data-expanded={expanded}
        >
            <ActivityHeader
                as='button'
                type='button'
                data-testid='llm-bot-tool-activity-header'
                aria-expanded={expandable ? expanded : undefined} // eslint-disable-line no-undefined
                disabled={!expandable}
                onClick={toggle}
            >
                <HeaderChevron
                    $expanded={expanded}
                    $visible={expandable}
                >
                    <ChevronRightIcon/>
                </HeaderChevron>
                <RollingLine
                    lineKey={lineKey}
                    animate={liveRef.current}
                >
                    <Indicator>{indicator}</Indicator>
                    <ActivityLabel>{label}</ActivityLabel>
                </RollingLine>
            </ActivityHeader>

            {(expanded || closing) && (
                <ExpandedRounds
                    $closing={!expanded}
                    data-testid='llm-bot-tool-activity-rounds'
                >
                    <ExpandedClip>
                        <ExpandedContent>
                            {activity.activityRounds.map(props.renderRound)}
                        </ExpandedContent>
                    </ExpandedClip>
                </ExpandedRounds>
            )}
        </ActivityContainer>
    );
};

export default ToolActivityDisplay;

// Expanded, the area becomes a framed panel so it reads as a separate section
// from the answer. The frame is a shadow so collapsed rows stay flush with text.
const ActivityContainer = styled.div<{$expanded: boolean}>`
    margin-top: 4px;
    border-radius: 8px;
    transition: padding ${EXPAND_MS}ms ease, box-shadow ${EXPAND_MS}ms ease, background-color ${EXPAND_MS}ms ease;
    box-shadow: inset 0 0 0 1px transparent;

    ${(props) => props.$expanded && css`
        padding: 8px 12px 12px;
        background-color: rgba(var(--center-channel-color-rgb), 0.02);
        box-shadow: inset 0 0 0 1px rgba(var(--center-channel-color-rgb), 0.12);
    `}

    ${noMotionWhenReduced}
`;

const ActivityHeader = styled(CollapseHeaderRow)`
    &:disabled {
        cursor: default;
        color: rgba(var(--center-channel-color-rgb), 0.75);
    }
`;

const HeaderChevron = styled(CollapseChevron)<{$visible: boolean}>`
    opacity: ${(props) => (props.$visible ? 1 : 0)};
    transition: transform 0.2s ease, opacity 0.2s ease;
`;

const Indicator = styled.div`
    display: flex;
    align-items: center;
    justify-content: center;
    width: 16px;
    height: 16px;
    flex-shrink: 0;
`;

const ActivityLabel = styled.span`
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
`;

const expandIn = keyframes`
    from {
        grid-template-rows: 0fr;
        opacity: 0;
    }
    to {
        grid-template-rows: 1fr;
        opacity: 1;
    }
`;

const collapseOut = keyframes`
    from {
        grid-template-rows: 1fr;
        opacity: 1;
    }
    to {
        grid-template-rows: 0fr;
        opacity: 0;
    }
`;

const ExpandedRounds = styled.div<{$closing: boolean}>`
    display: grid;
    ${(props) => css`
        animation: ${props.$closing ? collapseOut : expandIn} ${EXPAND_MS}ms ease forwards;
    `}

    ${noMotionWhenReduced}
`;

// Clip with a margin so tool card shadows and focus rings are not cut off.
const ExpandedClip = styled.div`
    min-height: 0;
    overflow: clip;
    overflow-clip-margin: 4px;
`;

const ExpandedContent = styled.div`
    margin-top: 8px;
    padding-top: 8px;
    border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
`;
