// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {expect, Locator, Page, test} from '@playwright/test';

import {AIPlugin} from 'helpers/ai-plugin';
import {MattermostPage} from 'helpers/mm';
import MattermostContainer from 'helpers/mmcontainer';
import {
    OpenAIMockContainer,
    RunOpenAIMocks,
    buildChatCompletionMockRule,
    buildTextResponse,
    buildTitleMockRule,
    buildToolCallResponse,
} from 'helpers/openai-mock';
import {mattermostAIAdminConfigApiFromClient} from 'helpers/plugin-http';
import RunContainer from 'helpers/plugincontainer';

const username = 'regularuser';
const password = 'regularuser';
const onlookerUsername = 'seconduser';
const onlookerPassword = 'seconduser';
const botUsername = 'mock';
const askUserQuestionTool = 'AskUserQuestion';

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

function escapeRegExp(value: string): string {
    return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function questionRequestPattern(marker: string): string {
    const escapedMarker = escapeRegExp(marker);
    const toolNamePattern = `"name"\\s*:\\s*"${escapeRegExp(askUserQuestionTool)}"`;
    return `(?s)(${escapedMarker}.*${toolNamePattern}|${toolNamePattern}.*${escapedMarker})`;
}

function escapedToolResult(result: {selected: string[]; custom?: string}): string {
    return JSON.stringify(JSON.stringify(result)).slice(1, -1);
}

function getChannelPost(page: Page, message: string): Locator {
    return page.locator('.post').filter({
        has: page.locator('.post-message__text').getByText(message, {exact: true}),
    }).last();
}

async function openPostThread(page: Page, post: Locator): Promise<Locator> {
    const replyIndicator = post.getByText(/\d+ repl/i);
    await expect(replyIndicator).toBeVisible({timeout: 60000});
    await replyIndicator.click();

    const rhs = page.locator('#rhsContainer');
    await expect(rhs).toBeVisible({timeout: 10000});
    await expect(rhs.locator('[data-testid="llm-bot-post"]').last()).toBeVisible({timeout: 30000});
    return rhs;
}

async function navigateToChannel(page: Page, channelName: string): Promise<void> {
    await page.goto(`${mattermost.url()}/test/channels/${channelName}`);
    await expect(page.getByTestId('channel_view')).toBeVisible({timeout: 60000});
}

test.describe('AskUserQuestion', () => {
    test.beforeAll(async () => {
        test.setTimeout(180000);
        mattermost = await RunContainer();
        openAIMock = await RunOpenAIMocks(mattermost.network);

        const adminClient = await mattermost.getAdminClient();
        const configAPI = mattermostAIAdminConfigApiFromClient(adminClient, mattermost.url());
        const config = await configAPI.get();
        await configAPI.put({...config, enableChannelMentionToolCalling: true});

        const onlookerClient = await mattermost.getClient(onlookerUsername, onlookerPassword);
        const onlooker = await onlookerClient.getMe();
        await onlookerClient.savePreferences(onlooker.id, [
            {user_id: onlooker.id, category: 'tutorial_step', name: onlooker.id, value: '999'},
            {user_id: onlooker.id, category: 'onboarding_task_list', name: 'onboarding_task_list_show', value: 'false'},
            {user_id: onlooker.id, category: 'onboarding_task_list', name: 'onboarding_task_list_open', value: 'false'},
            {
                user_id: onlooker.id,
                category: 'drafts',
                name: 'drafts_tour_tip_showed',
                value: JSON.stringify({drafts_tour_tip_showed: true}),
            },
            {user_id: onlooker.id, category: 'crt_thread_pane_step', name: onlooker.id, value: '999'},
        ]);
    });

    test.afterAll(async () => {
        await openAIMock.stop();
        await mattermost.stop();
    });

    test('answers a single-select question in a DM and restores it from history', async ({page}) => {
        test.setTimeout(120000);

        const marker = `DM_ANSWER_${Date.now()}`;
        const userMessage = `Ask me to choose a route ${marker}`;
        const question = `Which route should we take? ${marker}`;
        const selectedOption = `Blue route ${marker}`;
        const otherOption = `Green route ${marker}`;
        const toolCallID = `call_${marker}`;
        const title = `Question ${marker}`;
        const continuation = `Confirmed your selection: ${selectedOption}`;

        await openAIMock.addMocks([
            buildTitleMockRule(title, userMessage),
            buildChatCompletionMockRule(
                buildToolCallResponse(toolCallID, askUserQuestionTool, JSON.stringify({
                    question,
                    options: [
                        {label: selectedOption, description: 'Use the blue deployment route'},
                        {label: otherOption, description: 'Use the green deployment route'},
                    ],
                    multi_select: false,
                    allow_free_form: false,
                })),
                {bodyMatches: questionRequestPattern(marker), times: 1},
            ),
            buildChatCompletionMockRule(buildTextResponse(continuation), {
                bodyContains: escapedToolResult({selected: [selectedOption]}),
                times: 1,
            }),
        ]);

        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);
        await mmPage.login(mattermost.url(), username, password);
        await aiPlugin.openRHS();
        await aiPlugin.sendMessage(userMessage);

        const rhs = aiPlugin.getRhsContainer();
        const botPost = rhs.locator('[data-testid="llm-bot-post"]').last();
        await expect(botPost.getByText(question, {exact: true})).toBeVisible({timeout: 60000});

        const accept = botPost.getByRole('button', {name: /^Accept$/});
        await expect(accept).toBeDisabled();
        await botPost.getByRole('button', {name: new RegExp(selectedOption)}).click();
        await expect(accept).toBeEnabled();
        await accept.click();

        await expect(botPost.getByText('Answered', {exact: true})).toBeVisible({timeout: 30000});
        await expect(botPost.getByText(continuation, {exact: true})).toBeVisible({timeout: 60000});
        await expect(rhs.getByRole('button', {name: /stop/i})).not.toBeVisible({timeout: 30000});

        await page.reload({waitUntil: 'domcontentloaded'});
        await expect(page.getByTestId('channel_view')).toBeVisible({timeout: 60000});
        await aiPlugin.openRHS();
        await aiPlugin.openChatHistory();

        const historyItem = aiPlugin.threadsListContainer.getByText(title, {exact: true});
        await expect(historyItem).toBeVisible({timeout: 30000});
        await historyItem.click();

        const restoredPost = aiPlugin.getRhsContainer().locator('[data-testid="llm-bot-post"]').last();
        await expect(restoredPost.getByText(question, {exact: true})).toBeVisible({timeout: 30000});
        await expect(restoredPost.getByRole('button', {name: new RegExp(escapeRegExp(selectedOption))})).
            toHaveAttribute('aria-pressed', 'true');
        await expect(restoredPost.getByRole('button', {name: new RegExp(escapeRegExp(otherOption))})).
            toHaveAttribute('aria-pressed', 'false');
        await expect(restoredPost.getByText('Answered', {exact: true})).toBeVisible();
        await expect(restoredPost.getByText(continuation, {exact: true})).toBeVisible();
        await expect(restoredPost.getByRole('button', {name: /^Accept$/})).not.toBeVisible();
        await expect(restoredPost.getByRole('button', {name: /^Skip$/})).not.toBeVisible();
    });

    test('skips a DM question and resumes with a terminal skipped state', async ({page}) => {
        test.setTimeout(120000);

        const marker = `DM_SKIP_${Date.now()}`;
        const userMessage = `Ask me for an optional preference ${marker}`;
        const question = `Which optional preference do you want? ${marker}`;
        const toolCallID = `call_${marker}`;
        const title = `Skipped question ${marker}`;
        const continuation = `Continued without a preference ${marker}`;

        await openAIMock.addMocks([
            buildTitleMockRule(title, userMessage),
            buildChatCompletionMockRule(
                buildToolCallResponse(toolCallID, askUserQuestionTool, JSON.stringify({
                    question,
                    options: [
                        {label: `Fast ${marker}`},
                        {label: `Thorough ${marker}`},
                    ],
                    multi_select: false,
                    allow_free_form: false,
                })),
                {bodyMatches: questionRequestPattern(marker), times: 1},
            ),
            buildChatCompletionMockRule(buildTextResponse(continuation), {
                bodyContains: 'User skipped the question',
                times: 1,
            }),
        ]);

        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);
        await mmPage.login(mattermost.url(), username, password);
        await aiPlugin.openRHS();
        await aiPlugin.sendMessage(userMessage);

        const rhs = aiPlugin.getRhsContainer();
        const botPost = rhs.locator('[data-testid="llm-bot-post"]').last();
        await expect(botPost.getByText(question, {exact: true})).toBeVisible({timeout: 60000});
        await botPost.getByRole('button', {name: /^Skip$/}).click();

        await expect(botPost.getByText('Skipped', {exact: true})).toBeVisible({timeout: 30000});
        await expect(botPost.getByText(continuation, {exact: true})).toBeVisible({timeout: 60000});
        await expect(botPost.getByRole('button', {name: /^Accept$/})).not.toBeVisible();
        await expect(botPost.getByRole('button', {name: /^Skip$/})).not.toBeVisible();
        await expect(rhs.getByRole('button', {name: /stop/i})).not.toBeVisible({timeout: 30000});

        await aiPlugin.openChatHistory();
        await expect(aiPlugin.threadsListContainer.getByText(title, {exact: true})).toBeVisible({timeout: 30000});
    });

    test('round-trips multiple DM selections and a free-form answer', async ({page}) => {
        test.setTimeout(120000);

        const marker = `DM_MULTI_${Date.now()}`;
        const userMessage = `Ask me to choose launch requirements ${marker}`;
        const question = `Which launch requirements should we include? ${marker}`;
        const firstOption = `Audit logs ${marker}`;
        const secondOption = `Rate limits ${marker}`;
        const unselectedOption = `Guest access ${marker}`;
        const customAnswer = `Regional failover ${marker}`;
        const toolCallID = `call_${marker}`;
        const title = `Launch requirements ${marker}`;
        const continuation = `Recorded all launch requirements ${marker}`;

        await openAIMock.addMocks([
            buildTitleMockRule(title, userMessage),
            buildChatCompletionMockRule(
                buildToolCallResponse(toolCallID, askUserQuestionTool, JSON.stringify({
                    question,
                    options: [
                        {label: firstOption},
                        {label: secondOption},
                        {label: unselectedOption},
                    ],
                    multi_select: true,
                    allow_free_form: true,
                })),
                {bodyMatches: questionRequestPattern(marker), times: 1},
            ),
            buildChatCompletionMockRule(buildTextResponse(continuation), {
                bodyContains: escapedToolResult({
                    selected: [firstOption, secondOption],
                    custom: customAnswer,
                }),
                times: 1,
            }),
        ]);

        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);
        await mmPage.login(mattermost.url(), username, password);
        await aiPlugin.openRHS();
        await aiPlugin.sendMessage(userMessage);

        const rhs = aiPlugin.getRhsContainer();
        const botPost = rhs.locator('[data-testid="llm-bot-post"]').last();
        await expect(botPost.getByText(question, {exact: true})).toBeVisible({timeout: 60000});

        const accept = botPost.getByRole('button', {name: /^Accept$/});
        await expect(botPost.getByText('None selected', {exact: true})).toBeVisible();
        await expect(accept).toBeDisabled();

        const firstOptionButton = botPost.getByRole('button', {name: firstOption, exact: true});
        const secondOptionButton = botPost.getByRole('button', {name: secondOption, exact: true});
        const unselectedOptionButton = botPost.getByRole('button', {name: unselectedOption, exact: true});
        await firstOptionButton.click();
        await expect(botPost.getByText('1 selected', {exact: true})).toBeVisible();
        await expect(accept).toBeEnabled();

        await secondOptionButton.click();
        await expect(botPost.getByText('2 selected', {exact: true})).toBeVisible();

        const freeFormButton = botPost.getByRole('button', {name: 'Something else…', exact: true});
        await freeFormButton.click();
        await expect(botPost.getByText('2 selected', {exact: true})).toBeVisible();
        const freeFormInput = botPost.getByPlaceholder('Something else…');
        await freeFormInput.fill(customAnswer);
        await expect(botPost.getByText('3 selected', {exact: true})).toBeVisible();
        await expect(firstOptionButton).toHaveAttribute('aria-pressed', 'true');
        await expect(secondOptionButton).toHaveAttribute('aria-pressed', 'true');
        await expect(unselectedOptionButton).toHaveAttribute('aria-pressed', 'false');
        await expect(freeFormButton).toHaveAttribute('aria-pressed', 'true');
        await accept.click();

        await expect(botPost.getByText('Answered', {exact: true})).toBeVisible({timeout: 30000});
        await expect(botPost.getByText(continuation, {exact: true})).toBeVisible({timeout: 60000});
        await expect(firstOptionButton).toHaveAttribute('aria-pressed', 'true');
        await expect(secondOptionButton).toHaveAttribute('aria-pressed', 'true');
        await expect(unselectedOptionButton).toHaveAttribute('aria-pressed', 'false');
        await expect(freeFormButton).toHaveAttribute('aria-pressed', 'true');
        await expect(freeFormInput).toHaveValue(customAnswer);
        await expect(freeFormInput).toBeDisabled();
        await expect(rhs.getByRole('button', {name: /stop/i})).not.toBeVisible({timeout: 30000});

        await aiPlugin.openChatHistory();
        await expect(aiPlugin.threadsListContainer.getByText(title, {exact: true})).toBeVisible({timeout: 30000});
    });

    test('redacts a pending channel question for onlookers, then shares the answer without a share decision', async ({browser}) => {
        test.setTimeout(150000);

        const marker = `CHANNEL_QUESTION_${Date.now()}`;
        const userMessage = `Ask me to choose a channel plan ${marker}`;
        const channelMessage = `@${botUsername} ${userMessage}`;
        const question = `Which channel plan should we use? ${marker}`;
        const selectedOption = `Public plan ${marker}`;
        const privateOption = `Private plan ${marker}`;
        const toolCallID = `call_${marker}`;
        const continuation = `Channel plan confirmed: ${selectedOption}`;

        await openAIMock.addMocks([
            buildTitleMockRule(`Channel question ${marker}`, channelMessage),
            buildChatCompletionMockRule(
                buildToolCallResponse(toolCallID, askUserQuestionTool, JSON.stringify({
                    question,
                    options: [
                        {label: selectedOption},
                        {label: privateOption},
                    ],
                    multi_select: false,
                    allow_free_form: false,
                })),
                {bodyMatches: questionRequestPattern(marker), times: 1},
            ),
            buildChatCompletionMockRule(buildTextResponse(continuation), {
                bodyContains: escapedToolResult({selected: [selectedOption]}),
                times: 1,
            }),
        ]);

        const requesterContext = await browser.newContext();
        const onlookerContext = await browser.newContext();
        const requesterPage = await requesterContext.newPage();
        const onlookerPage = await onlookerContext.newPage();

        try {
            const requesterMM = new MattermostPage(requesterPage);
            const onlookerMM = new MattermostPage(onlookerPage);
            await requesterMM.login(mattermost.url(), username, password);
            await onlookerMM.login(mattermost.url(), onlookerUsername, onlookerPassword);
            await navigateToChannel(requesterPage, 'off-topic');
            await navigateToChannel(onlookerPage, 'off-topic');

            await requesterMM.mentionBot(botUsername, userMessage);
            const requesterRootPost = getChannelPost(requesterPage, channelMessage);
            await expect(requesterRootPost).toBeVisible({timeout: 30000});
            const requesterRhs = await openPostThread(requesterPage, requesterRootPost);

            const onlookerRootPost = getChannelPost(onlookerPage, channelMessage);
            await expect(onlookerRootPost).toBeVisible({timeout: 30000});
            const onlookerRhs = await openPostThread(onlookerPage, onlookerRootPost);

            const requesterBotPost = requesterRhs.locator('[data-testid="llm-bot-post"]').last();
            const onlookerBotPost = onlookerRhs.locator('[data-testid="llm-bot-post"]').last();
            await expect(requesterBotPost.getByText(question, {exact: true})).toBeVisible({timeout: 60000});

            await expect(onlookerBotPost.getByText(askUserQuestionTool, {exact: true})).toBeVisible({timeout: 30000});
            await expect(onlookerBotPost.getByText(question, {exact: true})).toHaveCount(0);
            await expect(onlookerBotPost.getByText(selectedOption, {exact: true})).toHaveCount(0);
            await expect(onlookerBotPost.getByText(privateOption, {exact: true})).toHaveCount(0);
            await expect(onlookerRhs.getByRole('button', {name: /^Accept$/})).toHaveCount(0);
            await expect(onlookerRhs.getByRole('button', {name: /^Skip$/})).toHaveCount(0);

            await requesterBotPost.getByRole('button', {name: new RegExp(selectedOption)}).click();
            await requesterBotPost.getByRole('button', {name: /^Accept$/}).click();

            await expect(requesterRhs.getByRole('button', {name: /^Share$/})).toHaveCount(0);
            await expect(requesterRhs.getByRole('button', {name: /^Keep private$/})).toHaveCount(0);
            await expect(requesterBotPost.getByText('Answered', {exact: true})).toBeVisible({timeout: 30000});
            await expect(requesterBotPost.getByText(continuation, {exact: true})).toBeVisible({timeout: 60000});
            await expect(onlookerBotPost.getByText(question, {exact: true})).toBeVisible({timeout: 60000});
            await expect(onlookerBotPost.getByRole('button', {name: new RegExp(escapeRegExp(selectedOption))})).
                toHaveAttribute('aria-pressed', 'true');
            await expect(onlookerBotPost.getByText('Answered', {exact: true})).toBeVisible();
            await expect(onlookerBotPost.getByText(continuation, {exact: true})).toBeVisible({timeout: 60000});

            await expect(requesterRhs.getByRole('button', {name: /^Share$/})).toHaveCount(0);
            await expect(requesterRhs.getByRole('button', {name: /^Keep private$/})).toHaveCount(0);
            await expect(onlookerRhs.getByRole('button', {name: /^Share$/})).toHaveCount(0);
            await expect(onlookerRhs.getByRole('button', {name: /^Keep private$/})).toHaveCount(0);
            await expect(requesterRhs.getByRole('button', {name: /stop/i})).not.toBeVisible({timeout: 30000});
        } finally {
            await requesterContext.close();
            await onlookerContext.close();
        }
    });
});
