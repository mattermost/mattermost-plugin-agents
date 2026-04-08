// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {test, expect, type Page, type Locator} from '@playwright/test';
import type {Client4} from '@mattermost/client';

import MattermostContainer from 'helpers/mmcontainer';
import {MattermostPage} from 'helpers/mm';
import {RunToolConfigContainerWithPolicies} from 'helpers/tool-config-container';
import {OpenAIMockContainer, RunOpenAIMocks, buildTextResponse} from 'helpers/openai-mock';
import {adminUsername, adminPassword} from 'helpers/system-console-container';

let mattermost: MattermostContainer | undefined;
let openAIMock: OpenAIMockContainer | undefined;

const botUsername = 'toolbot';
const userMentionMessage = `@${botUsername} incoming webhook activate_ai e2e`;

async function getTownSquareChannelID(mm: MattermostContainer): Promise<string> {
    const adminClient = await mm.getAdminClient();
    const teams = await adminClient.getMyTeams();
    if (!teams.length) {
        throw new Error('expected admin to belong to at least one team (getMyTeams returned empty)');
    }
    const defaultTeam = teams[0];
    const channels = await adminClient.getMyChannels(defaultTeam.id);
    const townSquare = channels.find((channel) => channel.name === 'town-square');
    if (!townSquare) {
        throw new Error('town-square channel not found');
    }
    return townSquare.id;
}

async function createIncomingWebhookURL(adminClient: Client4, channelId: string): Promise<string> {
    const base = adminClient.getUrl();
    const token = adminClient.getToken();
    const res = await fetch(`${base}/api/v4/hooks/incoming`, {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json',
            Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({
            channel_id: channelId,
            display_name: 'E2E activate_ai webhook',
            description: 'end-to-end test for webhook + activate_ai',
        }),
    });
    if (!res.ok) {
        const text = await res.text();
        throw new Error(`create incoming webhook failed: ${res.status} ${text}`);
    }
    const hook = (await res.json()) as {id: string};
    if (!hook?.id) {
        throw new Error('incoming webhook response missing id');
    }
    return `${base}/hooks/${hook.id}`;
}

async function postIncomingWebhook(webhookURL: string, text: string, props: Record<string, unknown>): Promise<void> {
    const res = await fetch(webhookURL, {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({text, props}),
    });
    if (!res.ok) {
        const body = await res.text();
        throw new Error(`incoming webhook post failed: ${res.status} ${body}`);
    }
}

async function waitForSentPostContaining(page: Page, textPattern: RegExp, timeout = 60000) {
    // Webhook posts may split @mentions across nodes; match on a stable substring instead of exact text.
    const post = page.locator('.post').filter({
        has: page.locator('.post-message__text').filter({hasText: textPattern}),
    }).last();
    await expect(post).toBeVisible({timeout});
    return post;
}

async function openThreadForPost(post: Locator, timeout = 60000) {
    const replyIndicator = post.getByText(/\d+ repl/i);
    await expect(replyIndicator).toBeVisible({timeout});
    await replyIndicator.click();
    const rhs = post.page().locator('#rhsContainer');
    await rhs.waitFor({state: 'visible', timeout: 10000});
    await rhs.locator('[data-testid="llm-bot-post"]').first().waitFor({state: 'visible', timeout});
}

test.describe('Incoming webhook + activate_ai', () => {
    test.beforeAll(async () => {
        mattermost = await RunToolConfigContainerWithPolicies();
        openAIMock = await RunOpenAIMocks(mattermost.network);
    });

    test.afterAll(async () => {
        if (openAIMock) {
            await openAIMock.stop();
        }
        if (mattermost) {
            await mattermost.stop();
        }
    });

    test('webhook post with @bot mention and activate_ai receives LLM reply in thread', async ({page}) => {
        test.setTimeout(120000);

        const replyText = 'Webhook activate_ai e2e assistant reply';

        if (!mattermost) {
            throw new Error('mattermost container not started');
        }
        if (!openAIMock) {
            throw new Error('OpenAI mock not started');
        }
        await openAIMock.addCompletionMock(buildTextResponse(replyText));

        const channelId = await getTownSquareChannelID(mattermost);
        const adminClient = await mattermost.getAdminClient();
        const webhookURL = await createIncomingWebhookURL(adminClient, channelId);

        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        const teamName = mattermost.teamName || 'test';
        await page.goto(`${mattermost.url()}/${teamName}/channels/town-square`);
        await page.getByTestId('channel_view').waitFor({state: 'visible', timeout: 60000});

        await postIncomingWebhook(webhookURL, userMentionMessage, {activate_ai: 'true'});

        const userPost = await waitForSentPostContaining(page, /incoming webhook activate_ai e2e/);
        await openThreadForPost(userPost);

        const rhs = page.locator('#rhsContainer');
        await expect(rhs.locator('[data-testid="llm-bot-post"]').first()).toBeVisible({timeout: 60000});
        await expect(rhs).toContainText(replyText, {timeout: 60000});
    });
});
