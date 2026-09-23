// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef, useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import styled, {css, keyframes} from 'styled-components';

import {ChevronRightIcon} from '@mattermost/compass-icons/components';

import {toolDisplayName} from '@/utils/tool_identity';

import ToolStatusIcon from '../tool_status_icon';
import {ToolCallStatus} from '../tool_types';

import {ActivityItem, PostActivity} from './activity_items';
import {CollapseChevron, CollapseHeaderRow} from './collapse_header';
import {noMotionWhenReduced, prefersReducedMotion} from './motion';
import {serverToolTitle} from './server_tool_set';
import {Round} from './turn_content_utils';

const ROW_ANIM_MS = 240;
const EXPAND_MS = 200;

const SUMMARY_KEY = 'summary';

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

    renderRound: (round: Round) => React.ReactNode;
}

/**
 * The collapsible activity area of a bot post. Collapsed, a single row shows
 * the latest tool invocation, or a "Used N tools" summary once the response is
 * done; expanded, it shows the full stack of intermediate rounds.
 */
const ToolActivityDisplay: React.FC<ToolActivityDisplayProps> = (props) => {
    const {activity, expanded, inProgress} = props;
    const intl = useIntl();

    // A tool can still be running after the stream stops.
    const showSummary = !inProgress && !activity.hasRunningTool;
    const current: ActivityItem | null = showSummary ? null : activity.items[activity.items.length - 1] ?? null;
    const currentKey = current?.id ?? SUMMARY_KEY;

    // Only rows that change after mount roll in, so settled posts render statically.
    const mountKeyRef = useRef(currentKey);
    const animateRow = currentKey !== mountKeyRef.current;

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

    let rowContent: React.ReactNode;
    if (current === null) {
        rowContent = (
            <>
                <ToolStatusIcon status={summaryStatus(activity)}/>
                <ActivityLabel>
                    <FormattedMessage
                        id='ai.activity.tools_used'
                        defaultMessage='Used {count, plural, one {# tool} other {# tools}}'
                        values={{count: activity.items.length}}
                    />
                </ActivityLabel>
            </>
        );
    } else {
        const label = current.kind === 'tool' ? toolDisplayName(current.toolCall) : serverToolTitle(current.serverTool, intl);
        rowContent = (
            <>
                <ToolStatusIcon status={current.status}/>
                <ActivityLabel>{label}</ActivityLabel>
            </>
        );
    }

    return (
        <ActivityContainer data-testid='llm-bot-tool-activity'>
            <CollapseHeaderRow
                as='button'
                type='button'
                data-testid='llm-bot-tool-activity-header'
                aria-expanded={expanded}
                onClick={toggle}
            >
                <CollapseChevron $expanded={expanded}>
                    <ChevronRightIcon/>
                </CollapseChevron>
                <RowViewport>
                    <Row
                        key={currentKey}
                        $animate={animateRow}
                        data-testid='llm-bot-tool-activity-current'
                    >
                        {rowContent}
                    </Row>
                </RowViewport>
            </CollapseHeaderRow>

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

const ActivityContainer = styled.div`
    margin-top: 4px;
`;

const RowViewport = styled.div`
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

const Row = styled.div<{$animate: boolean}>`
    display: flex;
    align-items: center;
    gap: 8px;
    height: 20px;
    line-height: 20px;

    ${(props) => props.$animate && css`
        animation: ${rollIn} ${ROW_ANIM_MS}ms ease-out;
    `}

    ${noMotionWhenReduced}
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
    padding-top: 8px;
`;
