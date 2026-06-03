// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {AlertCircleOutlineIcon} from '@mattermost/compass-icons/components';
import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import styled from 'styled-components';

import {useConversationContext} from '@/hooks/use_conversation_context';
import type {CompositionSource} from '@/types/conversation';

import DotMenu, {DotMenuButton, DropdownMenu} from '../dot_menu';

// HideBelow: utilization under this fraction hides the indicator entirely.
// A fresh conversation with two lines of chat shouldn't display a meter.
const HideBelow = 0.05;
const WarnAt = 0.7;
const CritAt = 0.9;

const RingSize = 18;
const RingStroke = 2.5;
const RingRadius = (RingSize - RingStroke) / 2;
const RingCircumference = 2 * Math.PI * RingRadius;

type ContextUsageIndicatorProps = {
    conversationId: string | undefined;
};

const ContextUsageIndicator = ({conversationId}: ContextUsageIndicatorProps) => {
    const intl = useIntl();
    const {composition} = useConversationContext(conversationId);

    if (!composition || composition.total <= 0) {
        return null;
    }

    const hasLimit = Boolean(composition.input_token_limit && composition.input_token_limit > 0);
    const utilization = hasLimit ? composition.total / (composition.input_token_limit as number) : 0;
    if (hasLimit && utilization < HideBelow) {
        return null;
    }

    const color = hasLimit ? ringColor(utilization) : 'rgba(var(--center-channel-color-rgb), 0.72)';
    const pct = hasLimit ? Math.round(utilization * 100) : 0;
    const isOver = hasLimit && utilization > 1;
    const cappedUtil = Math.min(utilization, 1);
    const arcOffset = RingCircumference * (1 - cappedUtil);

    const tooltip = hasLimit ? intl.formatMessage(
        {id: 'context.tooltip', defaultMessage: '{used} / {limit} tokens ({pct}%)'},
        {
            used: formatTokens(composition.total),
            limit: formatTokens(composition.input_token_limit as number),
            pct,
        },
    ) : intl.formatMessage(
        {id: 'context.tooltip.no_limit', defaultMessage: '{used} tokens used (provider does not publish a context limit)'},
        {used: formatTokens(composition.total)},
    );

    return (
        <DotMenu
            icon={
                <IndicatorContent>
                    {hasLimit && (
                        <RingSvg
                            width={RingSize}
                            height={RingSize}
                            viewBox={`0 0 ${RingSize} ${RingSize}`}
                            aria-hidden='true'
                        >
                            <circle
                                cx={RingSize / 2}
                                cy={RingSize / 2}
                                r={RingRadius}
                                fill='none'
                                stroke='rgba(var(--center-channel-color-rgb), 0.16)'
                                strokeWidth={RingStroke}
                            />
                            <circle
                                cx={RingSize / 2}
                                cy={RingSize / 2}
                                r={RingRadius}
                                fill='none'
                                stroke={color}
                                strokeWidth={RingStroke}
                                strokeDasharray={RingCircumference}
                                strokeDashoffset={arcOffset}
                                strokeLinecap='round'
                                transform={`rotate(-90 ${RingSize / 2} ${RingSize / 2})`}
                            />
                        </RingSvg>
                    )}
                    <Percent $color={color}>{hasLimit ? `${pct}%` : formatTokens(composition.total)}</Percent>
                    {isOver && (
                        <OverflowIcon data-testid='context-usage-overflow'>
                            <AlertCircleOutlineIcon
                                size={14}
                                color={color}
                            />
                        </OverflowIcon>
                    )}
                </IndicatorContent>
            }
            dotMenuButton={IndicatorButton}
            dropdownMenu={IndicatorDropdown}
            title={tooltip}
            placement='bottom-end'
            portal={false}
            closeOnClick={false}
            testId='context-usage-indicator'
        >
            <PopoverHeader>
                <PopoverTitle>
                    <FormattedMessage defaultMessage='Context window'/>
                </PopoverTitle>
                <PopoverSubtitle>
                    {hasLimit ? (
                        <FormattedMessage
                            defaultMessage='{used} of {limit} tokens used ({pct}%)'
                            values={{
                                used: formatTokens(composition.total),
                                limit: formatTokens(composition.input_token_limit as number),
                                pct,
                            }}
                        />
                    ) : (
                        <FormattedMessage
                            defaultMessage='{used} tokens used'
                            values={{used: formatTokens(composition.total)}}
                        />
                    )}
                </PopoverSubtitle>
                {isOver && (
                    <OverflowNote>
                        <FormattedMessage defaultMessage='Context is over the limit — older messages are being dropped to fit.'/>
                    </OverflowNote>
                )}
                {!hasLimit && (
                    <EstimatedNote>
                        <FormattedMessage defaultMessage="Provider does not publish a context window limit; utilization can't be computed."/>
                    </EstimatedNote>
                )}
                {composition.total_source === 'estimated' && (
                    <EstimatedNote>
                        <FormattedMessage defaultMessage='Total is estimated; the provider does not report exact counts for this model.'/>
                    </EstimatedNote>
                )}
            </PopoverHeader>
            <ComponentList>
                {/* Go marshals a nil slice to JSON null, so components can
                    arrive null even though the type says array (e.g. a >0
                    counted total with no taggable content). */}
                {(composition.components ?? []).map((c) => (
                    <ComponentRow key={c.source}>
                        <RowHeader>
                            <RowLabel>{intl.formatMessage(sourceLabels[c.source])}</RowLabel>
                            <RowTokens>
                                <FormattedMessage
                                    defaultMessage='{tokens} ({pct}%)'
                                    values={{
                                        tokens: formatTokens(c.tokens),
                                        pct: Math.round(c.proportion * 100),
                                    }}
                                />
                            </RowTokens>
                        </RowHeader>
                        <RowBarTrack>
                            <RowBarFill $widthPct={Math.max(1, c.proportion * 100)}/>
                        </RowBarTrack>
                    </ComponentRow>
                ))}
            </ComponentList>
        </DotMenu>
    );
};

function ringColor(utilization: number): string {
    if (utilization >= CritAt) {
        return 'var(--dnd-indicator)';
    }
    if (utilization >= WarnAt) {
        return 'var(--away-indicator)';
    }
    return 'rgba(var(--center-channel-color-rgb), 0.72)';
}

function formatTokens(n: number): string {
    if (n >= 1_000_000) {
        return `${(n / 1_000_000).toFixed(1)}M`;
    }
    if (n >= 10_000) {
        return `${Math.round(n / 1000)}k`;
    }
    if (n >= 1000) {
        return `${(n / 1000).toFixed(1)}k`;
    }
    return String(n);
}

const sourceLabels: Record<CompositionSource, {id: string; defaultMessage: string}> = {
    system: {id: 'context.source.system', defaultMessage: 'System prompt'},
    history: {id: 'context.source.history', defaultMessage: 'Conversation history'},
    tool_defs: {id: 'context.source.tool_defs', defaultMessage: 'Tool definitions'},
    tool_results: {id: 'context.source.tool_results', defaultMessage: 'Tool results'},
    image: {id: 'context.source.image', defaultMessage: 'Image'},
};

const IndicatorButton = styled(DotMenuButton)<{isActive: boolean}>`
    display: flex;
    align-items: center;
    padding: 2px 6px;
    border-radius: 4px;
    height: 20px;
    width: auto;
    font-size: 11px;
    font-weight: 600;
    line-height: 16px;
    background-color: ${(props) => (props.isActive ? 'rgba(var(--center-channel-color-rgb), 0.16)' : 'transparent')};

    &:hover {
        background-color: rgba(var(--center-channel-color-rgb), 0.08);
    }
`;

const IndicatorContent = styled.div`
    display: flex;
    align-items: center;
    gap: 4px;
`;

const RingSvg = styled.svg`
    flex-shrink: 0;
`;

const Percent = styled.span<{$color: string}>`
    font-variant-numeric: tabular-nums;
    color: ${(props) => props.$color};
`;

const OverflowIcon = styled.span`
    display: inline-flex;
    align-items: center;
`;

const IndicatorDropdown = styled(DropdownMenu)`
    width: 280px;
    padding: 12px;
`;

const PopoverHeader = styled.div`
    padding-bottom: 8px;
    margin-bottom: 8px;
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
`;

const PopoverTitle = styled.div`
    font-size: 13px;
    font-weight: 600;
    color: var(--center-channel-color);
`;

const PopoverSubtitle = styled.div`
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    margin-top: 2px;
`;

const EstimatedNote = styled.div`
    font-size: 11px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    font-style: italic;
    margin-top: 4px;
`;

const OverflowNote = styled.div`
    font-size: 12px;
    color: var(--dnd-indicator);
    margin-top: 4px;
`;

const ComponentList = styled.div`
    display: flex;
    flex-direction: column;
    gap: 8px;
`;

const ComponentRow = styled.div`
    display: flex;
    flex-direction: column;
    gap: 4px;
`;

const RowHeader = styled.div`
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    gap: 8px;
`;

const RowLabel = styled.span`
    font-size: 12px;
    color: var(--center-channel-color);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    min-width: 0;
    flex: 1;
`;

const RowTokens = styled.span`
    font-size: 11px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    font-variant-numeric: tabular-nums;
    flex-shrink: 0;
`;

const RowBarTrack = styled.div`
    width: 100%;
    height: 4px;
    border-radius: 2px;
    background: rgba(var(--center-channel-color-rgb), 0.08);
    overflow: hidden;
`;

const RowBarFill = styled.div<{$widthPct: number}>`
    width: ${(props) => props.$widthPct}%;
    height: 100%;
    background: rgba(var(--button-bg-rgb), 0.72);
    border-radius: 2px;
`;

export default ContextUsageIndicator;

// Test-only exports.
export const testOnly = {ringColor, formatTokens, HideBelow, WarnAt, CritAt};
