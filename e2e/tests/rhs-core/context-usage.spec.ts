// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {expect, test} from '@playwright/test';
import type {Locator} from '@playwright/test';
import type {Client4} from '@mattermost/client';
import type {Post} from '@mattermost/types/posts';

import {AIPlugin} from 'helpers/ai-plugin';
import {MattermostPage} from 'helpers/mm';
import MattermostContainer from 'helpers/mmcontainer';
import {
    OpenAIMockContainer,
    RunOpenAIMocks,
    buildChatCompletionMockRule,
    buildTextResponse,
    buildTitleMockRule,
} from 'helpers/openai-mock';
import {mattermostAIPluginRoutes, PluginRoutesApi} from 'helpers/plugin-http';
import {RunAIMockContainer} from 'helpers/plugincontainer';

const username = 'regularuser';
const password = 'regularuser';
const inputTokenLimit = 12000;

type CompositionSource = 'system' | 'history' | 'tool_defs' | 'tool_results' | 'image';

type CompositionComponent = {
    source: CompositionSource;
    proportion: number;
    tokens: number;
};

type Composition = {
    components: CompositionComponent[] | null;
    total: number;
    total_source: 'counted' | 'provider' | 'estimated';
    input_token_limit?: number;
};

function longPrompt(marker: string, label: string, repetitions: number): string {
    const payload = Array.from(
        {length: repetitions},
        (_, index) => `${label}-${index}-persistent-context-evidence`,
    ).join(' ');
    return `${marker}: remember this conversation context. ${payload}`;
}

async function getPostsForChannel(client: Client4, channelID: string): Promise<Post[]> {
    const response = await client.getPosts(channelID, 0, 200);
    return Object.values(response.posts);
}

async function findPersistedDMConversationID(
    client: Client4,
    responseText: string,
): Promise<string> {
    const user = await client.getMe();
    const botUser = await client.getUserByUsername('aimock');
    const channel = await client.createDirectChannel([user.id, botUser.id]);
    let matchedPost: Post | undefined;

    await expect.poll(async () => {
        const posts = await getPostsForChannel(client, channel.id);
        matchedPost = posts.find((post) => (
            post.user_id === botUser.id &&
            post.message.includes(responseText) &&
            typeof post.props?.conversation_id === 'string'
        ));
        return matchedPost?.props?.conversation_id ?? '';
    }, {
        message: 'bot response post did not persist a conversation_id',
        timeout: 30000,
        intervals: [250, 500, 1000],
    }).not.toBe('');

    const conversationID = matchedPost?.props?.conversation_id;
    if (typeof conversationID !== 'string' || !conversationID || !matchedPost) {
        throw new Error('persisted bot response did not contain a usable conversation_id');
    }
    return conversationID;
}

async function getComposition(
    routes: PluginRoutesApi,
    token: string,
    conversationID: string,
): Promise<Composition> {
    return routes.getJson(`conversations/${conversationID}/context`, token) as Promise<Composition>;
}

function expectPositiveComponent(
    composition: Composition,
    source: CompositionSource,
): void {
    const component = composition.components?.find((candidate) => candidate.source === source);
    if (!component) {
        throw new Error(`context composition did not include required ${source} component`);
    }
    expect(component.tokens).toBeGreaterThan(0);
}

function expectMeaningfulComposition(composition: Composition): void {
    expect(composition.input_token_limit).toBe(inputTokenLimit);
    expect(composition.total).toBeGreaterThan(0);
    expect(['counted', 'estimated']).toContain(composition.total_source);
    expectPositiveComponent(composition, 'system');
    expectPositiveComponent(composition, 'history');
}

function utilizationPercent(composition: Composition): number {
    return Math.round((composition.total / inputTokenLimit) * 100);
}

function formatTokens(tokens: number): string {
    if (tokens >= 1_000_000) {
        return `${(tokens / 1_000_000).toFixed(1)}M`;
    }
    if (tokens >= 10_000) {
        return `${Math.round(tokens / 1000)}k`;
    }
    if (tokens >= 1000) {
        return `${(tokens / 1000).toFixed(1)}k`;
    }
    return String(tokens);
}

async function expectIndicatorSemantics(indicator: Locator, composition: Composition): Promise<void> {
    const percent = utilizationPercent(composition);
    await expect(indicator).toBeVisible({timeout: 30000});
    await expect(indicator).toHaveText(`${percent}%`);
}

async function expectCompositionSource(
    popover: Locator,
    composition: Composition,
    source: CompositionSource,
    label: string,
): Promise<void> {
    const component = composition.components?.find((candidate) => candidate.source === source);
    if (!component) {
        throw new Error(`context composition did not include required ${source} component`);
    }

    const row = popover.getByText(label, {exact: true}).locator('xpath=../..');
    const renderedValue = `${formatTokens(component.tokens)} (${Math.round(component.proportion * 100)}%)`;
    await expect(row.getByText(renderedValue, {exact: true})).toBeVisible();
}

async function expectPopoverSemantics(
    popover: Locator,
    composition: Composition,
): Promise<void> {
    if (!composition.input_token_limit) {
        throw new Error('context composition did not include an input token limit');
    }

    await expect(popover).toBeVisible();
    await expect(popover.getByText('Context window', {exact: true})).toBeVisible();
    await expect(popover.getByText(
        `${formatTokens(composition.total)} of ${formatTokens(composition.input_token_limit)} tokens used ` +
        `(${utilizationPercent(composition)}%)`,
        {exact: true},
    )).toBeVisible();
    await expectCompositionSource(popover, composition, 'system', 'System prompt');
    await expectCompositionSource(popover, composition, 'history', 'Conversation history');

    const estimatedNote = popover.getByText(
        'Total is estimated; the provider does not report exact counts for this model.',
        {exact: true},
    );
    if (composition.total_source === 'estimated') {
        await expect(estimatedNote).toBeVisible();
    } else {
        await expect(estimatedNote).not.toBeVisible();
    }
}

test.describe('RHS context-window usage', () => {
    let mattermost: MattermostContainer;
    let openAIMock: OpenAIMockContainer;

    test.beforeAll(async () => {
        test.setTimeout(180000);
        mattermost = await RunAIMockContainer({
            service: {
                tokenLimit: inputTokenLimit,
                outputTokenLimit: 2048,
            },
            bot: {
                disableTools: true,
            },
        });
        openAIMock = await RunOpenAIMocks(mattermost.network);
    });

    test.afterAll(async () => {
        await openAIMock?.stop();
        await mattermost?.stop();
    });

    test('shows real composition and refreshes it after a follow-up', async ({page}) => {
        test.setTimeout(180000);

        const marker = `CONTEXT_USAGE_${Date.now()}`;
        const initialMessage = longPrompt(marker, 'initial', 180);
        const followUpMessage = longPrompt(`${marker}_FOLLOW_UP`, 'follow-up', 140);
        const title = `Context usage ${marker}`;
        const initialResponse = `Initial context response ${marker}`;
        const followUpResponse = `Updated context response ${marker}`;

        await openAIMock.addMocks([
            // Smocker gives the last matching rule priority. Register the broad initial
            // response first, the follow-up response second, and the title rule last.
            buildChatCompletionMockRule(buildTextResponse(initialResponse), {
                bodyContains: marker,
                times: 1,
            }),
            buildChatCompletionMockRule(buildTextResponse(followUpResponse), {
                bodyContains: `${marker}_FOLLOW_UP`,
                times: 1,
            }),
            buildTitleMockRule(title, initialMessage),
        ]);

        const userClient = await mattermost.getClient(username, password);
        const routes = mattermostAIPluginRoutes(mattermost.url());
        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);

        await mmPage.login(mattermost.url(), username, password);
        await aiPlugin.openRHS();
        await aiPlugin.sendMessage(initialMessage);
        await aiPlugin.waitForBotResponse(initialResponse);

        const conversationID = await findPersistedDMConversationID(userClient, initialResponse);
        const initialComposition = await getComposition(routes, userClient.getToken(), conversationID);
        expectMeaningfulComposition(initialComposition);
        expect(initialComposition.total / inputTokenLimit).toBeGreaterThanOrEqual(0.05);

        const rhs = aiPlugin.getRhsContainer();
        const indicator = rhs.getByTestId('context-usage-indicator');
        await expectIndicatorSemantics(indicator, initialComposition);
        await indicator.click();

        let popover = rhs.getByTestId('dropdownmenu').filter({hasText: 'Context window'});
        await expectPopoverSemantics(popover, initialComposition);
        await indicator.click();

        let refreshedComposition: Composition | undefined;
        const contextRefresh = page.waitForResponse(async (response) => {
            const path = new URL(response.url()).pathname;
            if (
                response.request().method() !== 'GET' ||
                path !== `/plugins/mattermost-ai/conversations/${conversationID}/context` ||
                response.status() !== 200
            ) {
                return false;
            }

            const candidate = await response.json() as Composition;
            if (candidate.total <= initialComposition.total) {
                return false;
            }
            refreshedComposition = candidate;
            return true;
        }, {timeout: 60000});

        await Promise.all([
            contextRefresh,
            (async () => {
                await aiPlugin.sendMessage(followUpMessage);
                await aiPlugin.waitForBotResponse(followUpResponse);
            })(),
        ]);

        if (!refreshedComposition) {
            throw new Error('follow-up did not trigger an increased context response');
        }
        expect(refreshedComposition.total).toBeGreaterThan(initialComposition.total);
        expect(utilizationPercent(refreshedComposition)).toBeGreaterThan(utilizationPercent(initialComposition));
        await expectIndicatorSemantics(indicator, refreshedComposition);

        await indicator.click();
        popover = rhs.getByTestId('dropdownmenu').filter({hasText: 'Context window'});
        await expectPopoverSemantics(popover, refreshedComposition);
    });
});
