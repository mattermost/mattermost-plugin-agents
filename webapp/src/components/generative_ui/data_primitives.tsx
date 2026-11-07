// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {ArrowDownIcon, ArrowUpIcon, MinusIcon} from '@mattermost/compass-icons/components';

import {ProgressBar} from '@mattermost/compass-ui/components/progress-bar';

import {Column} from './primitives';

export type Trend = 'up' | 'down' | 'neutral';

export const StatGrid = styled.div`
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
    gap: var(--spacing-xs);
`;

const StatTile = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xxxxs);
    padding: var(--spacing-xs) var(--spacing-m);
    border-radius: var(--radius-s);
    background: rgba(var(--center-channel-color-rgb), 0.04);
`;

const StatLabel = styled.span`
    font-size: 12px;
    font-weight: 600;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const StatValue = styled.span`
    font-family: Metropolis, sans-serif;
    font-size: 22px;
    font-weight: 600;
    line-height: 28px;
    color: var(--center-channel-color);
`;

const trendColor: Record<Trend, string> = {
    up: 'var(--online-indicator)',
    down: 'var(--error-text)',
    neutral: 'rgba(var(--center-channel-color-rgb), 0.64)',
};

const StatChange = styled.span<{$trend: Trend}>`
    display: inline-flex;
    align-items: center;
    gap: var(--spacing-xxxxs);
    font-size: 12px;
    line-height: 16px;
    color: ${({$trend}) => trendColor[$trend]};
`;

const TrendIcon = ({trend}: {trend: Trend}) => {
    switch (trend) {
    case 'up':
        return <ArrowUpIcon size={12}/>;
    case 'down':
        return <ArrowDownIcon size={12}/>;
    case 'neutral':
        return <MinusIcon size={12}/>;
    default: {
        const unhandled: never = trend;
        return unhandled;
    }
    }
};

export const Stat = ({label, value, change, trend = 'neutral'}: {label: string; value: string | number; change?: string; trend?: Trend}) => (
    <StatTile>
        <StatLabel>{label}</StatLabel>
        <StatValue>{value}</StatValue>
        {change && (
            <StatChange $trend={trend}>
                <TrendIcon trend={trend}/>
                {change}
            </StatChange>
        )}
    </StatTile>
);

const TableScroll = styled.div`
    overflow-x: auto;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    border-radius: var(--radius-s);
`;

const StyledTable = styled.table`
    width: 100%;
    border-collapse: collapse;
    font-size: 14px;
    line-height: 20px;

    th, td {
        padding: var(--spacing-xs) var(--spacing-m);
        text-align: left;
        vertical-align: top;
        border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    }

    th {
        font-weight: 600;
        color: rgba(var(--center-channel-color-rgb), 0.72);
        background: rgba(var(--center-channel-color-rgb), 0.04);
        white-space: nowrap;
    }

    tr:last-child td {
        border-bottom: none;
    }

    tbody tr:hover {
        background: rgba(var(--center-channel-color-rgb), 0.04);
    }
`;

export const Table = ({columns, rows}: {columns: string[]; rows: React.ReactNode[][]}) => (
    <TableScroll>
        <StyledTable>
            <thead>
                <tr>
                    {columns.map((column, index) => (
                        <th
                            key={`${column}-${index}`}
                            scope='col'
                        >
                            {column}
                        </th>
                    ))}
                </tr>
            </thead>
            <tbody>
                {rows.map((row, rowIndex) => (
                    // eslint-disable-next-line react/no-array-index-key -- generated rows have no identity
                    <tr key={rowIndex}>
                        {columns.map((column, colIndex) => (
                            <td key={`${column}-${colIndex}`}>{row[colIndex] ?? ''}</td>
                        ))}
                    </tr>
                ))}
            </tbody>
        </StyledTable>
    </TableScroll>
);

const ChartTitle = styled.span`
    font-size: 12px;
    font-weight: 600;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
`;

const BarRow = styled.div`
    display: grid;
    grid-template-columns: minmax(80px, 30%) 1fr minmax(48px, max-content);
    align-items: center;
    gap: var(--spacing-xs);
    font-size: 12px;
    line-height: 16px;
`;

const BarLabel = styled.span`
    color: var(--center-channel-color);
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
`;

const BarTrack = styled.div`
    height: 8px;
    border-radius: var(--radius-full);
    background: rgba(var(--center-channel-color-rgb), 0.08);
    overflow: hidden;
`;

const BarFill = styled.div.attrs<{$percent: number}>(({$percent}) => ({
    style: {width: `${$percent}%`},
}))<{$percent: number}>`
    height: 100%;
    border-radius: var(--radius-full);
    background: var(--button-bg);
    transition: width 300ms ease-out;
`;

const BarValue = styled.span`
    color: rgba(var(--center-channel-color-rgb), 0.72);
    text-align: right;
    font-variant-numeric: tabular-nums;
`;

export const BarChart = ({title, labels, values, unit}: {title?: string; labels: string[]; values: number[]; unit?: string}) => {
    const max = Math.max(0, ...values.filter((v) => Number.isFinite(v)));
    return (
        <Column $gap='s'>
            {title && <ChartTitle>{title}</ChartTitle>}
            {labels.map((label, index) => {
                const value = Number.isFinite(values[index]) ? values[index] : 0;
                const percent = max > 0 ? Math.max(0, (value / max) * 100) : 0;
                return (
                    <BarRow key={`${label}-${index}`}>
                        <BarLabel title={label}>{label}</BarLabel>
                        <BarTrack
                            role='img'
                            aria-label={`${label}: ${value}${unit ?? ''}`}
                        >
                            <BarFill $percent={percent}/>
                        </BarTrack>
                        <BarValue>{`${value}${unit ?? ''}`}</BarValue>
                    </BarRow>
                );
            })}
        </Column>
    );
};

const ProgressHeader = styled.div`
    display: flex;
    justify-content: space-between;
    gap: var(--spacing-xs);
    font-size: 12px;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
`;

const ProgressLabel = styled.span`
    font-weight: 600;
    color: var(--center-channel-color);
`;

export const Progress = ({label, value, caption}: {label: string; value: number; caption?: string}) => {
    const clamped = Math.min(100, Math.max(0, Number.isFinite(value) ? value : 0));
    return (
        <Column $gap='s'>
            <ProgressHeader>
                <ProgressLabel>{label}</ProgressLabel>
                <span>{caption ?? `${Math.round(clamped)}%`}</span>
            </ProgressHeader>
            <ProgressBar
                value={clamped}
                size='small'
                aria-label={label}
            />
        </Column>
    );
};
