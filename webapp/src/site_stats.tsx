// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import type {Store, UnknownAction} from 'redux';
import {createIntl, defineMessages, FormattedMessage, type IntlShape} from 'react-intl';

import {AnalyticsVisualizationType, type PluginAnalyticsRow} from '@mattermost/types/admin';
import type {GlobalState} from '@mattermost/types/store';

import {getUsageStats} from '@/client';
import type {UsageStatsResponse} from '@/types/usage_stats';

const messages = defineMessages({
    mau: {id: 'agents.site_stats.mau', defaultMessage: 'Agents Monthly Active Users'},
    activeUsers7d: {id: 'agents.site_stats.active_users_7d', defaultMessage: 'Agents Users (Last 7 Days)'},
    activeUsers60d: {id: 'agents.site_stats.active_users_60d', defaultMessage: 'Agents Users (Last 60 Days)'},
    activeUsers90d: {id: 'agents.site_stats.active_users_90d', defaultMessage: 'Agents Users (Last 90 Days)'},
    tokens30d: {id: 'agents.site_stats.tokens_30d', defaultMessage: 'Agents Tokens (30 Days)'},
    cost30d: {id: 'agents.site_stats.cost_30d', defaultMessage: 'Agents Est. Cost USD (30 Days)'},
    mauPerAgent: {id: 'agents.site_stats.mau_per_agent', defaultMessage: 'Monthly Active Users per Agent'},
    tokensPerDay: {id: 'agents.site_stats.tokens_per_day', defaultMessage: 'Agents Tokens per Day (Input vs. Output)'},
    inputTokens: {id: 'agents.site_stats.input_tokens', defaultMessage: 'Input tokens'},
    outputTokens: {id: 'agents.site_stats.output_tokens', defaultMessage: 'Output tokens'},
});

const doughnutBackground = ['#46BFBD', '#FDB45C', '#F7464A', '#3CB470', '#502D86', '#949FB1', '#36A2EB', '#4D5360'];
const doughnutHover = ['#5AD3D1', '#FFC870', '#FF5A5E', '#3CB470', '#502D86', '#A8B3C9', '#5AB3F0', '#616774'];

export function buildSiteStatsRows(stats: UsageStatsResponse, intl: IntlShape): Record<string, PluginAnalyticsRow> {
    const perAgent = stats.active_users_per_agent ?? [];
    const perDay = stats.tokens_per_day_30d ?? [];
    const tokens30d = stats.tokens_30d ?? {input_tokens: 0, output_tokens: 0, total_tokens: 0};

    return {
        agents_monthly_active_users: {
            id: 'agents_monthly_active_users',
            name: <FormattedMessage {...messages.mau}/>,
            icon: 'fa-users',
            value: stats.monthly_active_users ?? 0,
            visualizationType: AnalyticsVisualizationType.Count,
        },
        agents_active_users_7d: {
            id: 'agents_active_users_7d',
            name: <FormattedMessage {...messages.activeUsers7d}/>,
            icon: 'fa-user',
            value: stats.unique_users_7d ?? 0,
            visualizationType: AnalyticsVisualizationType.Count,
        },
        agents_active_users_60d: {
            id: 'agents_active_users_60d',
            name: <FormattedMessage {...messages.activeUsers60d}/>,
            icon: 'fa-user',
            value: stats.unique_users_60d ?? 0,
            visualizationType: AnalyticsVisualizationType.Count,
        },
        agents_active_users_90d: {
            id: 'agents_active_users_90d',
            name: <FormattedMessage {...messages.activeUsers90d}/>,
            icon: 'fa-user',
            value: stats.unique_users_90d ?? 0,
            visualizationType: AnalyticsVisualizationType.Count,
        },
        agents_tokens_30d: {
            id: 'agents_tokens_30d',
            name: <FormattedMessage {...messages.tokens30d}/>,
            icon: 'fa-bar-chart',
            value: tokens30d.total_tokens,
            visualizationType: AnalyticsVisualizationType.Count,
        },
        agents_cost_30d: {
            id: 'agents_cost_30d',
            name: <FormattedMessage {...messages.cost30d}/>,
            icon: 'fa-usd',
            value: Math.round((stats.cost_30d ?? 0) * 100) / 100,
            visualizationType: AnalyticsVisualizationType.Count,
        },
        agents_mau_per_agent: {
            id: 'agents_mau_per_agent',
            name: <FormattedMessage {...messages.mauPerAgent}/>,
            value: {
                labels: perAgent.map((a) => a.display_name),
                datasets: [{
                    data: perAgent.map((a) => a.active_users),
                    backgroundColor: perAgent.map((_, i) => doughnutBackground[i % doughnutBackground.length]),
                    hoverBackgroundColor: perAgent.map((_, i) => doughnutHover[i % doughnutHover.length]),
                }],
            },
            visualizationType: AnalyticsVisualizationType.DoughnutChart,
        },
        agents_tokens_per_day: {
            id: 'agents_tokens_per_day',
            name: <FormattedMessage {...messages.tokensPerDay}/>,
            value: {
                labels: perDay.map((d) => d.day),
                datasets: [
                    {
                        label: intl.formatMessage(messages.inputTokens),
                        data: perDay.map((d) => d.input_tokens),
                        borderColor: 'rgba(151,187,205,1)',
                        backgroundColor: 'rgba(151,187,205,0.2)',
                        pointBackgroundColor: 'rgba(151,187,205,1)',
                        pointBorderColor: '#fff',
                        fill: true,
                    },
                    {
                        label: intl.formatMessage(messages.outputTokens),
                        data: perDay.map((d) => d.output_tokens),
                        borderColor: 'rgba(70,191,189,1)',
                        backgroundColor: 'rgba(70,191,189,0.2)',
                        pointBackgroundColor: 'rgba(70,191,189,1)',
                        pointBorderColor: '#fff',
                        fill: true,
                    },
                ],
            },
            visualizationType: AnalyticsVisualizationType.LineChart,
        },
    };
}

type WebappStore = Store<GlobalState, UnknownAction>;

function getSiteStatsIntl(store: WebappStore): IntlShape {
    const state = store.getState() as any;
    const locale = state.entities?.i18n?.locale ?? 'en';
    let msgs: Record<string, string>;
    try {
        // eslint-disable-next-line global-require, import/no-dynamic-require
        msgs = require(`./i18n/${locale}.json`);
    } catch {
        // eslint-disable-next-line global-require
        msgs = require('./i18n/en.json');
    }
    return createIntl({
        locale,
        messages: msgs,
        defaultLocale: 'en',
    });
}

export async function getAgentsSiteStats(store: WebappStore): Promise<Record<string, PluginAnalyticsRow>> {
    try {
        const stats = await getUsageStats();
        return buildSiteStatsRows(stats, getSiteStatsIntl(store));
    } catch {
        // A rejected handler breaks the whole core Site Statistics fetch
        // (core Promise.all's every registered handler), so degrade to no rows.
        return {};
    }
}
