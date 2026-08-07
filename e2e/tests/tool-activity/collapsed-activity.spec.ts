import {test, expect} from '@playwright/test';

import {AIPlugin} from 'helpers/ai-plugin';
import {AIMockContainer, RunAIMockSidecar} from 'helpers/aimock-container';
import {
    AIMockFixtureFile,
    buildMultiTurnToolSequence,
    buildTitleFixture,
    EMBEDDED_GET_CHANNEL_INFO_TOOL,
    mergeFixtureFiles,
} from 'helpers/aimock-fixtures';
import {
    collapseToolActivity,
    expandToolActivity,
    expectToolActivityCollapsed,
    expectToolActivitySummary,
    LLMBotPostHelper,
    TOOL_STATUS_SELECTOR,
} from 'helpers/llmbot-post';
import {MattermostPage} from 'helpers/mm';
import MattermostContainer from 'helpers/mmcontainer';
import {RunToolConfigAIMockContainer, setupRegularTestUser} from 'helpers/tool-config-container';

/**
 * The collapsed tool-activity area of a bot post, driven end to end through
 * auto-run tools so nothing ever waits on a decision. Animation timing is
 * covered by unit tests; these cases only assert what is on screen once the
 * row has settled.
 */

const username = 'regularuser';
const password = 'regularuser';

const EMBEDDED_READ_CHANNEL_TOOL = 'mattermost__read_channel';
const getChannelInfoLabel = 'Get Channel Info';
const readChannelLabel = 'Read Channel';

const multiToolPrompt = 'tool activity multi tool auto run';
const multiToolFinal = 'MULTI_TOOL_ANSWER: both lookups are done.';

const narrationPrompt = 'tool activity narration between tools';
const narrationText = 'Let me check the channel before answering.';
const narrationFinal = 'NARRATION_ANSWER: here is what I found.';

const failingPrompt = 'tool activity failing tool';
const failingFinal = 'FAILING_ANSWER: the lookup did not work.';

const reasoningPrompt = 'tool activity reasoning with tools';
const reasoningText = 'Weighing the channel metadata before replying.';
const reasoningFinal = 'REASONING_ANSWER: the channel looks healthy.';

const stopPrompt = 'tool activity stop mid tools';
const stopAnswerStart = 'STOP_ANSWER: beginning a long explanation';
const stopAnswerTail = 'that keeps going for a while so the stream is still open when Stop is pressed. ';
const stopAnswer = `${stopAnswerStart} ${stopAnswerTail.repeat(16)}`;

test.describe('Collapsed Tool Activity (Aimock)', () => {
    test.describe.configure({mode: 'serial'});

    let mattermost: MattermostContainer;
    let aimock: AIMockContainer;

    test.beforeAll(async () => {
        test.setTimeout(300000);
        mattermost = await RunToolConfigAIMockContainer({
            toolConfigs: [
                {name: 'get_channel_info', policy: 'auto_run_in_dm', enabled: true},
                {name: 'read_channel', policy: 'auto_run_in_dm', enabled: true},
            ],
            reasoningEnabled: true,
        });
        await setupRegularTestUser(mattermost);

        const userClient = await mattermost.getClient(username, password);
        const teams = await userClient.getMyTeams();
        const channels = await userClient.getMyChannels(teams[0].id);
        const townSquare = channels.find((channel: {name: string}) => channel.name === 'town-square');
        if (!townSquare) {
            throw new Error('town-square channel not found');
        }

        // The title fixture has to come first: aimock matches user messages by
        // substring, and a title request embeds the original prompt.
        const fixtures: AIMockFixtureFile = mergeFixtureFiles(
            {fixtures: [buildTitleFixture('Tool activity')]},
            buildMultiTurnToolSequence({
                userPromptMarker: multiToolPrompt,
                steps: [
                    {
                        toolCallId: 'call_ta_multi_info',
                        toolName: EMBEDDED_GET_CHANNEL_INFO_TOOL,
                        args: {channel_name: 'Town Square'},
                    },
                    {
                        matchAfterToolCallId: 'call_ta_multi_info',
                        toolCallId: 'call_ta_multi_read',
                        toolName: EMBEDDED_READ_CHANNEL_TOOL,
                        args: {channel_id: townSquare.id, limit: 5},
                    },
                    {matchAfterToolCallId: 'call_ta_multi_read', text: multiToolFinal},
                ],
            }),
            {
                fixtures: [
                    // Narration: assistant text in the same turn as the tool call.
                    {
                        match: {userMessage: narrationPrompt, hasToolResult: false},
                        response: {
                            content: narrationText,
                            toolCalls: [{
                                id: 'call_ta_narration',
                                name: EMBEDDED_GET_CHANNEL_INFO_TOOL,
                                arguments: {channel_name: 'Town Square'},
                            }],
                            finishReason: 'tool_calls',
                        },
                    },
                    {
                        match: {toolCallId: 'call_ta_narration'},
                        response: {content: narrationFinal},
                    },

                    // A malformed channel id makes read_channel fail its
                    // argument validation, which lands as a tool error.
                    {
                        match: {userMessage: failingPrompt, hasToolResult: false},
                        response: {
                            toolCalls: [{
                                id: 'call_ta_failing',
                                name: EMBEDDED_READ_CHANNEL_TOOL,
                                arguments: {channel_id: 'not-a-channel-id'},
                            }],
                            finishReason: 'tool_calls',
                        },
                    },
                    {
                        match: {toolCallId: 'call_ta_failing'},
                        response: {content: failingFinal},
                    },

                    // Reasoning lands on the answer round, so its Thinking row
                    // sits next to the collapsed activity area rather than
                    // inside it.
                    {
                        match: {userMessage: reasoningPrompt, hasToolResult: false},
                        response: {
                            toolCalls: [{
                                id: 'call_ta_reasoning',
                                name: EMBEDDED_GET_CHANNEL_INFO_TOOL,
                                arguments: {channel_name: 'Town Square'},
                            }],
                            finishReason: 'tool_calls',
                        },
                    },
                    {
                        match: {toolCallId: 'call_ta_reasoning'},
                        response: {reasoning: reasoningText, content: reasoningFinal},
                    },

                    // The answer trickles in after the tool round, so the
                    // activity area is on screen while Stop is still offered.
                    {
                        match: {userMessage: stopPrompt, hasToolResult: false},
                        response: {
                            toolCalls: [{
                                id: 'call_ta_stop',
                                name: EMBEDDED_GET_CHANNEL_INFO_TOOL,
                                arguments: {channel_name: 'Town Square'},
                            }],
                            finishReason: 'tool_calls',
                        },
                    },
                    {
                        match: {toolCallId: 'call_ta_stop'},
                        response: {content: stopAnswer},
                        chunkSize: 2,
                        streamingProfile: {ttft: 300, tps: 4, jitter: 0},
                    },
                ],
            },
        );

        aimock = await RunAIMockSidecar(mattermost.network, {fixtures});
    });

    test.afterAll(async () => {
        await aimock?.stop();
        await mattermost?.stop();
    });

    test('collapses multiple auto-run tools into a summary that expands and collapses again', async ({page}) => {
        test.setTimeout(180000);

        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);
        await mmPage.login(mattermost.url(), username, password);
        await aiPlugin.resetState();
        await aiPlugin.sendMessage(multiToolPrompt);

        const rhs = page.getByTestId('mattermost-ai-rhs');
        const botPost = rhs.locator('[data-testid="llm-bot-post"]').last();
        await expect(botPost.getByText(multiToolFinal)).toBeVisible({timeout: 120000});
        await expect(page.getByRole('button', {name: /stop/i})).not.toBeVisible({timeout: 30000});

        // Collapsed by default: a single row, no tool cards anywhere.
        await expectToolActivityCollapsed(botPost);
        await expectToolActivitySummary(botPost, 2, 'success');
        await expect(botPost.getByText(getChannelInfoLabel, {exact: true})).toHaveCount(0);
        await expect(botPost.getByText(readChannelLabel, {exact: true})).toHaveCount(0);

        const activityRounds = await expandToolActivity(botPost);
        await expect(activityRounds.getByText(getChannelInfoLabel, {exact: true})).toBeVisible({timeout: 30000});
        await expect(activityRounds.getByText(readChannelLabel, {exact: true})).toBeVisible();

        await collapseToolActivity(botPost);
        await expectToolActivityCollapsed(botPost);
        await expect(botPost.getByText(getChannelInfoLabel, {exact: true})).toHaveCount(0);
        await expect(botPost.getByText(readChannelLabel, {exact: true})).toHaveCount(0);

        // The answer is never part of the activity area.
        await expect(botPost.getByText(multiToolFinal)).toBeVisible();
    });

    test('hides intermediate narration text until the activity area is expanded', async ({page}) => {
        test.setTimeout(180000);

        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);
        await mmPage.login(mattermost.url(), username, password);
        await aiPlugin.resetState();
        await aiPlugin.sendMessage(narrationPrompt);

        const rhs = page.getByTestId('mattermost-ai-rhs');
        const botPost = rhs.locator('[data-testid="llm-bot-post"]').last();
        await expect(botPost.getByText(narrationFinal)).toBeVisible({timeout: 120000});
        await expect(page.getByRole('button', {name: /stop/i})).not.toBeVisible({timeout: 30000});

        await expectToolActivityCollapsed(botPost);
        await expect(botPost.getByText(narrationText)).toHaveCount(0);

        const activityRounds = await expandToolActivity(botPost);
        await expect(activityRounds.getByText(narrationText)).toBeVisible({timeout: 30000});
        await expect(botPost.getByText(narrationFinal)).toBeVisible();
    });

    test('summarizes a failed tool with an error glyph', async ({page}) => {
        test.setTimeout(180000);

        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);
        await mmPage.login(mattermost.url(), username, password);
        await aiPlugin.resetState();
        await aiPlugin.sendMessage(failingPrompt);

        const rhs = page.getByTestId('mattermost-ai-rhs');
        const botPost = rhs.locator('[data-testid="llm-bot-post"]').last();
        await expect(botPost.getByText(failingFinal)).toBeVisible({timeout: 120000});
        await expect(page.getByRole('button', {name: /stop/i})).not.toBeVisible({timeout: 30000});

        await expectToolActivitySummary(botPost, 1, 'error');

        const activityRounds = await expandToolActivity(botPost);
        await expect(activityRounds.getByText(readChannelLabel, {exact: true})).toBeVisible({timeout: 30000});
    });

    test('shows the reasoning row alongside the activity area, each expanding independently', async ({page}) => {
        test.setTimeout(180000);

        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);
        const llmBotHelper = new LLMBotPostHelper(page);
        await mmPage.login(mattermost.url(), username, password);
        await aiPlugin.resetState();
        await aiPlugin.sendMessage(reasoningPrompt);

        const rhs = page.getByTestId('mattermost-ai-rhs');
        const botPost = rhs.locator('[data-testid="llm-bot-post"]').last();
        await expect(botPost.getByText(reasoningFinal)).toBeVisible({timeout: 120000});
        await expect(page.getByRole('button', {name: /stop/i})).not.toBeVisible({timeout: 30000});

        // Both collapsed rows coexist on the finished post.
        await expectToolActivitySummary(botPost, 1, 'success');
        await llmBotHelper.expectReasoningLabelVisible(true);
        await llmBotHelper.expectReasoningExpanded(false);

        // Opening the activity area leaves the reasoning row untouched.
        const activityRounds = await expandToolActivity(botPost);
        await expect(activityRounds.getByText(getChannelInfoLabel, {exact: true})).toBeVisible({timeout: 30000});
        await llmBotHelper.expectReasoningLabelVisible(true);

        // Opening the reasoning row leaves the activity stack open.
        await llmBotHelper.clickReasoningToggle();
        await llmBotHelper.expectReasoningText(reasoningText);
        await expect(activityRounds.getByText(getChannelInfoLabel, {exact: true})).toBeVisible();

        // Collapsing the activity area leaves the reasoning open.
        await collapseToolActivity(botPost);
        await expectToolActivityCollapsed(botPost);
        await llmBotHelper.expectReasoningText(reasoningText);
    });

    test('settles the post without a stuck spinner when generation is stopped after a tool round', async ({page}) => {
        test.setTimeout(180000);

        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);
        await mmPage.login(mattermost.url(), username, password);
        await aiPlugin.resetState();
        await aiPlugin.sendMessage(stopPrompt);

        const rhs = page.getByTestId('mattermost-ai-rhs');
        const botPost = rhs.locator('[data-testid="llm-bot-post"]').last();

        // The tool round runs first and the answer then trickles in, which is
        // the window where Stop is on offer. Whether the finished tool round
        // is already folded into an activity row by now is not asserted: a
        // client that mounts after the round resolves misses its websocket
        // event and only picks the round up when the turn is persisted.
        const stopButton = page.getByRole('button', {name: /stop/i});
        await expect(stopButton).toBeVisible({timeout: 120000});
        await expect.
            poll(async () => (await botPost.textContent()) ?? '', {timeout: 120000}).
            toContain(stopAnswerStart);

        await stopButton.click();

        // Stopping settles the post: the partial answer stays, the controls
        // hand back to Regenerate and nothing is left spinning.
        await expect(stopButton).not.toBeVisible({timeout: 60000});
        await expect(botPost.getByText(stopAnswerStart)).toBeVisible();
        await expect(botPost).not.toContainText(stopAnswer);
        await expect(botPost.getByText('Starting...')).toHaveCount(0);
        await expect(botPost.locator(`${TOOL_STATUS_SELECTOR}[data-status="running"]`)).toHaveCount(0);
        await expect(botPost.getByRole('button', {name: /regenerate/i})).toBeVisible({timeout: 30000});
    });
});
