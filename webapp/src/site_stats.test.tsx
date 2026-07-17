// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {createIntl} from 'react-intl';

import {AnalyticsVisualizationType} from '@mattermost/types/admin';

import type {UsageStatsResponse} from '@/types/usage_stats';

import {buildSiteStatsRows, getAgentsSiteStats} from './site_stats';

jest.mock('@/client', () => ({
    getUsageStats: jest.fn(),
}));

const {getUsageStats} = jest.requireMock('@/client') as {
    getUsageStats: jest.MockedFunction<() => Promise<UsageStatsResponse>>;
};

const intl = createIntl({locale: 'en', messages: {}, defaultLocale: 'en'});

function makeStats(overrides: Partial<UsageStatsResponse> = {}): UsageStatsResponse {
    return {
        monthly_active_users: 42,
        active_users_per_agent: [
            {bot_id: 'bot1', display_name: 'Matty', active_users: 12},
            {bot_id: 'bot2', display_name: 'Helper', active_users: 5},
        ],
        unique_users_7d: 10,
        unique_users_60d: 50,
        unique_users_90d: 60,
        tokens_30d: {input_tokens: 120000, output_tokens: 45000, total_tokens: 165000},
        cost_30d: 12.3456,
        tokens_per_day_30d: [
            {day: '2026-06-15', input_tokens: 100, output_tokens: 50, total_tokens: 150},
            {day: '2026-06-16', input_tokens: 200, output_tokens: 80, total_tokens: 280},
            {day: '2026-06-17', input_tokens: 300, output_tokens: 100, total_tokens: 400},
        ],
        ...overrides,
    };
}

const fakeStore = {
    getState: () => ({entities: {i18n: {locale: 'en'}}}),
} as any;

const EXPECTED_KEYS = [
    'agents_monthly_active_users',
    'agents_active_users_7d',
    'agents_active_users_60d',
    'agents_active_users_90d',
    'agents_tokens_30d',
    'agents_cost_30d',
    'agents_mau_per_agent',
    'agents_tokens_per_day',
];

describe('buildSiteStatsRows', () => {
    const stats = makeStats();
    const rows = buildSiteStatsRows(stats, intl);

    test('returns exactly the 8 pinned row keys', () => {
        expect(Object.keys(rows).sort()).toEqual([...EXPECTED_KEYS].sort());
    });

    describe.each([
        {
            key: 'agents_monthly_active_users',
            icon: 'fa-users',
            defaultMessage: 'Agents Monthly Active Users',
            expectedValue: 42,
        },
        {
            key: 'agents_active_users_7d',
            icon: 'fa-user',
            defaultMessage: 'Agents Users (Last 7 Days)',
            expectedValue: 10,
        },
        {
            key: 'agents_active_users_60d',
            icon: 'fa-user',
            defaultMessage: 'Agents Users (Last 60 Days)',
            expectedValue: 50,
        },
        {
            key: 'agents_active_users_90d',
            icon: 'fa-user',
            defaultMessage: 'Agents Users (Last 90 Days)',
            expectedValue: 60,
        },
        {
            key: 'agents_tokens_30d',
            icon: 'fa-bar-chart',
            defaultMessage: 'Agents Tokens (30 Days)',
            expectedValue: 165000,
        },
        {
            key: 'agents_cost_30d',
            icon: 'fa-usd',
            defaultMessage: 'Agents Est. Cost USD (30 Days)',
            expectedValue: 12.35,
        },
    ])('count row $key', ({key, icon, defaultMessage, expectedValue}) => {
        test('has pinned id, icon, visualizationType, value, and defaultMessage', () => {
            const row = rows[key];
            expect(row.id).toBe(key);
            expect(row.visualizationType).toBe(AnalyticsVisualizationType.Count);
            expect(row.icon).toBe(icon);
            expect(row.value).toBe(expectedValue);
            expect((row.name as React.ReactElement).props.defaultMessage).toBe(defaultMessage);
        });
    });

    test('rounds cost_30d to 2 decimals; 0 stays 0', () => {
        expect(buildSiteStatsRows(makeStats({cost_30d: 12.3456}), intl).agents_cost_30d.value).toBe(12.35);
        expect(buildSiteStatsRows(makeStats({cost_30d: 0}), intl).agents_cost_30d.value).toBe(0);
    });

    test('doughnut chart has agent labels, data, and core palette colors', () => {
        const doughnut = rows.agents_mau_per_agent;
        expect(doughnut.visualizationType).toBe(AnalyticsVisualizationType.DoughnutChart);
        expect(doughnut.icon).toBeUndefined();
        expect((doughnut.name as React.ReactElement).props.defaultMessage).toBe('Monthly Active Users per Agent');

        const value = doughnut.value as {
            labels: string[];
            datasets: Array<{
                data: number[];
                backgroundColor: string[];
                hoverBackgroundColor: string[];
            }>;
        };
        expect(value.labels).toEqual(['Matty', 'Helper']);
        expect(value.datasets).toHaveLength(1);
        expect(value.datasets[0].data).toEqual([12, 5]);
        expect(value.datasets[0].backgroundColor).toEqual(['#46BFBD', '#FDB45C']);
        expect(value.datasets[0].hoverBackgroundColor).toEqual(['#5AD3D1', '#FFC870']);
    });

    test('doughnut palette cycles when there are more agents than colors', () => {
        const manyAgents = Array.from({length: 10}, (_, i) => ({
            bot_id: `bot${i}`,
            display_name: `Agent ${i}`,
            active_users: i + 1,
        }));
        const doughnut = buildSiteStatsRows(makeStats({active_users_per_agent: manyAgents}), intl).agents_mau_per_agent;
        const colors = (doughnut.value as {datasets: Array<{backgroundColor: string[]}>}).datasets[0].backgroundColor;
        expect(colors).toHaveLength(10);
        expect(colors[0]).toBe('#46BFBD');
        expect(colors[7]).toBe('#4D5360');
        expect(colors[8]).toBe('#46BFBD');
        expect(colors[9]).toBe('#FDB45C');
    });

    test('line chart has day labels, two datasets, chart.js-3 colors, and is JSON-serializable', () => {
        const line = rows.agents_tokens_per_day;
        expect(line.visualizationType).toBe(AnalyticsVisualizationType.LineChart);
        expect(line.icon).toBeUndefined();
        expect((line.name as React.ReactElement).props.defaultMessage).toBe('Agents Tokens per Day (Input vs. Output)');

        const value = line.value as {
            labels: string[];
            datasets: Array<{
                label: string;
                data: number[];
                borderColor: string;
                fill: boolean;
            }>;
        };
        expect(value.labels).toEqual(['2026-06-15', '2026-06-16', '2026-06-17']);
        expect(value.datasets).toHaveLength(2);
        expect(value.datasets[0].label).toBe('Input tokens');
        expect(value.datasets[0].data).toEqual([100, 200, 300]);
        expect(value.datasets[0].borderColor).toBe('rgba(151,187,205,1)');
        expect(value.datasets[0].fill).toBe(true);
        expect(value.datasets[1].label).toBe('Output tokens');
        expect(value.datasets[1].data).toEqual([50, 80, 100]);
        expect(value.datasets[1].borderColor).toBe('rgba(70,191,189,1)');
        expect(value.datasets[1].fill).toBe(true);

        expect(JSON.parse(JSON.stringify(value))).toEqual(value);
    });

    test('empty data still yields 8 rows with zero counts and empty chart arrays', () => {
        const empty = buildSiteStatsRows(makeStats({
            monthly_active_users: 0,
            unique_users_7d: 0,
            unique_users_60d: 0,
            unique_users_90d: 0,
            tokens_30d: {input_tokens: 0, output_tokens: 0, total_tokens: 0},
            cost_30d: 0,
            active_users_per_agent: [],
            tokens_per_day_30d: [],
        }), intl);

        expect(Object.keys(empty).sort()).toEqual([...EXPECTED_KEYS].sort());
        expect(empty.agents_monthly_active_users.value).toBe(0);
        expect(empty.agents_active_users_7d.value).toBe(0);
        expect(empty.agents_tokens_30d.value).toBe(0);
        expect(empty.agents_cost_30d.value).toBe(0);
        expect(Number.isNaN(empty.agents_monthly_active_users.value)).toBe(false);

        const doughnut = empty.agents_mau_per_agent.value as {labels: string[]; datasets: Array<{data: number[]}>};
        expect(doughnut.labels).toEqual([]);
        expect(doughnut.datasets[0].data).toEqual([]);

        const line = empty.agents_tokens_per_day.value as {labels: string[]; datasets: Array<{data: number[]}>};
        expect(line.labels).toEqual([]);
        expect(line.datasets[0].data).toEqual([]);
        expect(line.datasets[1].data).toEqual([]);
    });
});

describe('getAgentsSiteStats', () => {
    beforeEach(() => {
        getUsageStats.mockReset();
    });

    test('success path returns rows from buildSiteStatsRows', async () => {
        const stats = makeStats();
        getUsageStats.mockResolvedValue(stats);

        const result = await getAgentsSiteStats(fakeStore);
        const expected = buildSiteStatsRows(stats, intl);

        expect(Object.keys(result).sort()).toEqual(Object.keys(expected).sort());
        expect(result.agents_monthly_active_users.value).toBe(expected.agents_monthly_active_users.value);
        expect(result.agents_tokens_30d.value).toBe(expected.agents_tokens_30d.value);
    });

    test('failure path resolves to empty object and does not reject', async () => {
        getUsageStats.mockRejectedValue(new Error('boom'));

        await expect(getAgentsSiteStats(fakeStore)).resolves.toEqual({});
    });
});
