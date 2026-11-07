// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {ArrowDownIcon, ArrowUpIcon} from '@mattermost/compass-icons/components';

import {ProgressBar} from '@mattermost/compass-ui/components/progress-bar';

import {Column, Fields, surface} from './primitives';

/** "up" and "down" are the original spellings of "good" and "bad". */
export type Trend = 'good' | 'bad' | 'neutral' | 'up' | 'down';

type Sentiment = 'good' | 'bad' | 'neutral';

const sentimentOf = (trend: Trend): Sentiment => {
    switch (trend) {
    case 'good':
    case 'up':
        return 'good';
    case 'bad':
    case 'down':
        return 'bad';
    case 'neutral':
        return 'neutral';
    default: {
        const unhandled: never = trend;
        return unhandled;
    }
    }
};

// Up to four tiles share one row; in a narrow column four or more wrap to two
// per row, so nothing is left as a lone orphan tile.
const StatGridContainer = styled.div`
    container-type: inline-size;
    min-width: 0;
`;

const StatGridColumns = styled.div<{$count: number}>`
    display: grid;
    grid-template-columns: repeat(${({$count}) => Math.min($count, 4)}, minmax(0, 1fr));
    gap: var(--spacing-xs);

    @container (max-width: 420px) {
        grid-template-columns: repeat(${({$count}) => ($count === 3 ? 3 : Math.min($count, 2))}, minmax(0, 1fr));
    }
`;

export const StatGrid = ({count, children}: {count: number; children: React.ReactNode}) => (
    <StatGridContainer>
        <StatGridColumns $count={Math.max(count, 1)}>{children}</StatGridColumns>
    </StatGridContainer>
);

const StatTile = styled.div`
    ${surface}
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xxxxs);
    padding: var(--spacing-xs) var(--spacing-m);
    min-width: 0;
`;

const StatLabel = styled.span`
    font-size: var(--font-size-75);
    font-weight: var(--font-weight-semibold);
    line-height: var(--line-height-75);
    color: rgba(var(--center-channel-color-rgb), 0.64);
    overflow-wrap: anywhere;
`;

const StatValue = styled.span`
    font-family: var(--font-family-heading);
    font-size: var(--font-size-500);
    font-weight: var(--font-weight-semibold);
    line-height: var(--line-height-500);
    color: var(--center-channel-color);
    overflow-wrap: anywhere;
`;

const sentimentColor: Record<Sentiment, string> = {
    good: 'var(--color-success)',
    bad: 'var(--color-danger)',
    neutral: 'rgba(var(--center-channel-color-rgb), 0.64)',
};

const StatChange = styled.span<{$sentiment: Sentiment}>`
    display: inline-flex;
    align-items: center;
    gap: var(--spacing-xxxxs);
    font-size: var(--font-size-75);
    line-height: var(--line-height-75);
    color: ${({$sentiment}) => sentimentColor[$sentiment]};
`;

type Direction = 'up' | 'down' | 'none';

// The arrow shows which way the number moved; colour alone says if that's good.
const directionOf = (change: string): Direction => {
    const sign = change.trim().charAt(0);
    if (sign === '+') {
        return 'up';
    }
    if (sign === '-' || sign === '\u2212' || sign === '\u2013') {
        return 'down';
    }
    return 'none';
};

const ChangeArrow = ({direction}: {direction: Direction}) => {
    switch (direction) {
    case 'up':
        return <ArrowUpIcon size={12}/>;
    case 'down':
        return <ArrowDownIcon size={12}/>;
    case 'none':
        return null;
    default: {
        const unhandled: never = direction;
        return unhandled;
    }
    }
};

export const Stat = ({label, value, change, trend = 'neutral'}: {label: string; value: string | number; change?: string; trend?: Trend}) => {
    const direction = directionOf(change ?? '');
    const sentiment = sentimentOf(trend);
    return (
        <StatTile>
            <StatLabel>{label}</StatLabel>
            <StatValue>{value}</StatValue>
            {change && (
                <StatChange
                    $sentiment={sentiment}
                    data-direction={direction}
                    data-sentiment={sentiment}
                >
                    <ChangeArrow direction={direction}/>
                    {change}
                </StatChange>
            )}
        </StatTile>
    );
};

// Below this width a table can't show its columns without clipping, so each
// row is shown as a label/value list instead.
const NARROW_TABLE = '420px';

const TableContainer = styled.div`
    container-type: inline-size;
    min-width: 0;
`;

const TableScroll = styled.div`
    ${surface}
    overflow-x: auto;

    @container (max-width: ${NARROW_TABLE}) {
        display: none;
    }
`;

const StyledTable = styled.table`
    width: 100%;
    border-collapse: collapse;
    font-size: var(--font-size-100);
    line-height: var(--line-height-100);

    th, td {
        padding: var(--spacing-xs) var(--spacing-m);
        text-align: left;
        vertical-align: top;
        border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    }

    th {
        font-weight: var(--font-weight-semibold);
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

const StackedRows = styled.div`
    display: none;
    flex-direction: column;
    gap: var(--spacing-xs);

    @container (max-width: ${NARROW_TABLE}) {
        display: flex;
    }
`;

const StackedRow = styled.div`
    ${surface}
    padding: var(--spacing-xs) var(--spacing-m);
`;

export const Table = ({columns, rows}: {columns: string[]; rows: React.ReactNode[][]}) => (
    <TableContainer>
        <TableScroll data-testid='generative-ui-table-wide'>
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
        <StackedRows data-testid='generative-ui-table-narrow'>
            {rows.map((row, rowIndex) => (
                // eslint-disable-next-line react/no-array-index-key -- generated rows have no identity
                <StackedRow key={rowIndex}>
                    <Fields items={columns.map((column, colIndex) => ({label: column, value: row[colIndex] ?? ''}))}/>
                </StackedRow>
            ))}
        </StackedRows>
    </TableContainer>
);

const ChartTitle = styled.span`
    font-size: var(--font-size-75);
    font-weight: var(--font-weight-semibold);
    line-height: var(--line-height-75);
    color: rgba(var(--center-channel-color-rgb), 0.72);
`;

const BarRow = styled.div`
    display: grid;
    grid-template-columns: minmax(80px, 30%) 1fr minmax(48px, max-content);
    align-items: center;
    gap: var(--spacing-xs);
    font-size: var(--font-size-75);
    line-height: var(--line-height-75);
`;

const BarLabel = styled.span`
    color: var(--center-channel-color);
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
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
                    <BarRow
                        key={`${label}-${index}`}
                        role='img'
                        aria-label={`${label}: ${value}${unit ?? ''}`}
                    >
                        <BarLabel title={label}>{label}</BarLabel>
                        <ProgressBar
                            decorative={true}
                            value={percent}
                        />
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
    font-size: var(--font-size-75);
    line-height: var(--line-height-75);
    color: rgba(var(--center-channel-color-rgb), 0.72);
`;

const ProgressLabel = styled.span`
    font-weight: var(--font-weight-semibold);
    color: var(--center-channel-color);
`;

export const Progress = ({label, value, caption}: {label: string; value: number; caption?: string}) => {
    const clamped = Math.min(100, Math.max(0, Number.isFinite(value) ? value : 0));
    const valueText = caption ?? `${Math.round(clamped)}%`;
    return (
        <Column $gap='s'>
            <ProgressHeader>
                <ProgressLabel>{label}</ProgressLabel>
                <span>{valueText}</span>
            </ProgressHeader>
            <ProgressBar
                value={clamped}
                aria-label={label}
                aria-valuetext={valueText}
            />
        </Column>
    );
};
