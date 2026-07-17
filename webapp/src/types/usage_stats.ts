// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export type AgentActiveUsers = {
    bot_id: string;
    display_name: string;
    active_users: number;
};

export type TokenTotals = {
    input_tokens: number;
    output_tokens: number;
    total_tokens: number;
};

export type DailyTokenCount = {
    day: string; // YYYY-MM-DD, UTC
    input_tokens: number;
    output_tokens: number;
    total_tokens: number;
};

export type UsageStatsResponse = {
    monthly_active_users: number;
    active_users_per_agent: AgentActiveUsers[];
    unique_users_7d: number;
    unique_users_60d: number;
    unique_users_90d: number;
    tokens_30d: TokenTotals;
    cost_30d: number;
    tokens_per_day_30d: DailyTokenCount[];
};
