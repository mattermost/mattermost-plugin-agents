// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {test, expect} from '@playwright/test';

import MattermostContainer from 'helpers/mmcontainer';
import {MattermostPage} from 'helpers/mm';
import {MATTERMOST_AI_PLUGIN_ID} from 'helpers/plugin-http';
import RunSystemConsoleContainer, {adminUsername, adminPassword} from 'helpers/system-console-container';

const regularUsername = 'regularuser';
const regularPassword = 'regularuser';

type TokenTotals = {
    input_tokens: number;
    output_tokens: number;
    total_tokens: number;
};

type DailyTokenCount = {
    day: string;
    input_tokens: number;
    output_tokens: number;
    total_tokens: number;
};

type UsageStatsResponse = {
    monthly_active_users: number;
    active_users_per_agent: Array<{
        bot_id: string;
        display_name: string;
        active_users: number;
    }>;
    unique_users_7d: number;
    unique_users_60d: number;
    unique_users_90d: number;
    tokens_30d: TokenTotals;
    cost_30d: number;
    tokens_per_day_30d: DailyTokenCount[];
};

let mattermost: MattermostContainer;

async function setupRegularUser(mattermostInstance: MattermostContainer): Promise<void> {
    await mattermostInstance.createUser('regularuser@sample.com', regularUsername, regularPassword);
    await mattermostInstance.addUserToTeam(regularUsername, 'test');

    const userClient = await mattermostInstance.getClient(regularUsername, regularPassword);
    const user = await userClient.getMe();
    await userClient.savePreferences(user.id, [
        {user_id: user.id, category: 'tutorial_step', name: user.id, value: '999'},
        {user_id: user.id, category: 'onboarding_task_list', name: 'onboarding_task_list_show', value: 'false'},
        {user_id: user.id, category: 'onboarding_task_list', name: 'onboarding_task_list_open', value: 'false'},
        {
            user_id: user.id,
            category: 'drafts',
            name: 'drafts_cloud_tip_showed',
            value: JSON.stringify({drafts_cloud_tip_showed: true}),
        },
        {user_id: user.id, category: 'crt_thread_pane_step', name: user.id, value: '999'},
    ]);
}

function adminStatsUrl(baseUrl: string): string {
    return `${baseUrl.replace(/\/$/, '')}/plugins/${MATTERMOST_AI_PLUGIN_ID}/admin/stats`;
}

function assertZeroFilledUsageStats(stats: UsageStatsResponse): void {
    expect(stats.monthly_active_users).toBe(0);
    expect(stats.unique_users_7d).toBe(0);
    expect(stats.unique_users_60d).toBe(0);
    expect(stats.unique_users_90d).toBe(0);
    expect(stats.active_users_per_agent).toEqual([]);
    expect(stats.tokens_30d).toEqual({
        input_tokens: 0,
        output_tokens: 0,
        total_tokens: 0,
    });
    expect(stats.cost_30d).toBe(0);
    expect(stats.tokens_per_day_30d).toHaveLength(30);

    for (let i = 0; i < stats.tokens_per_day_30d.length; i++) {
        const point = stats.tokens_per_day_30d[i];
        expect(point.day).toMatch(/^\d{4}-\d{2}-\d{2}$/);
        expect(point.input_tokens).toBe(0);
        expect(point.output_tokens).toBe(0);
        expect(point.total_tokens).toBe(0);
        if (i > 0) {
            expect(point.day > stats.tokens_per_day_30d[i - 1].day).toBe(true);
        }
    }
}

/**
 * Agents usage metrics on System Console → Reporting → Site Statistics.
 * Zero data is enough: Phase 3 /admin/stats zero-fills empty tables.
 */
test.describe.serial('Agents usage statistics', () => {
    test.beforeAll(async () => {
        test.setTimeout(180000);

        mattermost = await RunSystemConsoleContainer({
            services: [],
            bots: [],
        });
        await setupRegularUser(mattermost);
    });

    test.afterAll(async () => {
        if (mattermost) {
            await mattermost.stop();
        }
    });

    test('GET /admin/stats returns zero-filled schema for sysadmin and 403 for regular user', async () => {
        test.setTimeout(60000);

        const adminClient = await mattermost.getClient(adminUsername, adminPassword);
        const adminResponse = await fetch(adminStatsUrl(mattermost.url()), {
            method: 'GET',
            headers: {
                Authorization: `Bearer ${adminClient.getToken()}`,
            },
        });
        expect(adminResponse.status).toBe(200);

        const bodyText = await adminResponse.text();
        expect(bodyText).toContain('"active_users_per_agent":[]');
        expect(bodyText).not.toContain('"active_users_per_agent":null');

        const stats = JSON.parse(bodyText) as UsageStatsResponse;
        assertZeroFilledUsageStats(stats);

        const regularClient = await mattermost.getClient(regularUsername, regularPassword);
        const regularResponse = await fetch(adminStatsUrl(mattermost.url()), {
            method: 'GET',
            headers: {
                Authorization: `Bearer ${regularClient.getToken()}`,
            },
        });
        expect(regularResponse.status).toBe(403);
    });

    test('Site Statistics page renders Agents usage tiles and chart titles', async ({page}) => {
        test.setTimeout(60000);

        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        await page.goto(`${mattermost.url()}/admin_console/reporting/system_analytics`, {
            waitUntil: 'domcontentloaded',
        });

        // Core prefixes plugin row keys as `${pluginId}.${key}` for StatisticCount
        // data-testid values. The release-11.9 webapp used by this harness sets testids on
        // the title/value nodes (`${id}` / `${id}Title`), not the outer `${id}Card` wrapper.
        const mauValue = page.getByTestId('mattermost-ai.agents_monthly_active_users');
        await expect(mauValue).toBeVisible({timeout: 30000});
        await expect(mauValue).toHaveText('0');
        await expect(page.getByTestId('mattermost-ai.agents_monthly_active_usersTitle')).toBeVisible();

        const tokensValue = page.getByTestId('mattermost-ai.agents_tokens_30d');
        await expect(tokensValue).toBeVisible();
        await expect(tokensValue).toHaveText('0');
        await expect(page.getByTestId('mattermost-ai.agents_tokens_30dTitle')).toBeVisible();

        await expect(page.getByText('Monthly Active Users per Agent')).toBeVisible();
        await expect(page.getByText('Agents Tokens per Day (Input vs. Output)')).toBeVisible();
    });
});
