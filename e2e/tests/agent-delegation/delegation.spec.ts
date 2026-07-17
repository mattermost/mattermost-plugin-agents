// Agent-to-agent delegation (ask_agent) with deterministic aimock fixtures.
// Covers: DM happy path with the delegation card, nested approval inside the
// delegation thread, and the channel-mention flow with the share gate.
// seed: tests/seed.spec.ts

import {test, expect, Page} from '@playwright/test';

import {AIMockContainer, RunAIMockSidecar} from 'helpers/aimock-container';
import {
    AIMOCK_COMPATIBLE_SERVICE,
    AIMockFixtureFile,
    buildTitleFixture,
    mergeFixtureFiles,
} from 'helpers/aimock-fixtures';
import MattermostContainer from 'helpers/mmcontainer';
import {MattermostPage} from 'helpers/mm';
import {RunSystemConsoleContainer} from 'helpers/system-console-container';
import {setupRegularTestUser} from 'helpers/tool-config-container';

const username = 'regularuser';
const password = 'regularuser';
const orchestratorBot = 'orchestrator';
const subagentBot = 'subagent';

const ASK_AGENT_TOOL = 'mattermost__ask_agent';
const GET_CHANNEL_INFO_TOOL = 'mattermost__get_channel_info';

let mattermost: MattermostContainer;
let aimock: AIMockContainer;

type DelegationFixtureOptions = {
    promptMarker: string;
    taskMarker: string;
    parentCallId: string;
    subAnswer: string;
    finalText: string;

    // When set, the sub-agent first calls get_channel_info (ask policy) so
    // the initiator must approve inside the delegation thread.
    nestedToolCallId?: string;
    nestedToolChannelId?: string;
};

// buildDelegationFixtures chains: orchestrator turn -> ask_agent tool call;
// sub-agent turn (matched by the task marker) -> optional nested tool call ->
// final sub answer; orchestrator follow-up (matched by the ask_agent call id)
// -> final synthesis.
function buildDelegationFixtures(options: DelegationFixtureOptions): AIMockFixtureFile {
    const fixtures: AIMockFixtureFile = {fixtures: []};

    fixtures.fixtures.push({
        match: {userMessage: options.promptMarker, hasToolResult: false},
        response: {
            toolCalls: [{
                id: options.parentCallId,
                name: ASK_AGENT_TOOL,
                arguments: {agent: subagentBot, task: `${options.taskMarker} What shipped last sprint?`},
            }],
            finishReason: 'tool_calls',
        },
    });

    if (options.nestedToolCallId) {
        fixtures.fixtures.push({
            match: {userMessage: options.taskMarker, hasToolResult: false},
            response: {
                toolCalls: [{
                    id: options.nestedToolCallId,
                    name: GET_CHANNEL_INFO_TOOL,
                    arguments: {channel_id: options.nestedToolChannelId},
                }],
                finishReason: 'tool_calls',
            },
        });
        fixtures.fixtures.push({
            match: {toolCallId: options.nestedToolCallId},
            response: {content: options.subAnswer, finishReason: 'stop'},
        });
    } else {
        fixtures.fixtures.push({
            match: {userMessage: options.taskMarker, hasToolResult: false},
            response: {content: options.subAnswer, finishReason: 'stop'},
        });
    }

    fixtures.fixtures.push({
        match: {toolCallId: options.parentCallId},
        response: {content: options.finalText, finishReason: 'stop'},
    });

    return fixtures;
}

async function openDMWithBot(page: Page, baseUrl: string, botUsername: string): Promise<void> {
    await page.goto(`${baseUrl}/test/messages/@${botUsername}`);
    await page.getByTestId('channel_view').waitFor({state: 'visible', timeout: 30000});
    await page.waitForTimeout(1000);
}

async function openThreadForMarker(page: Page, marker: string, timeout = 120000): Promise<void> {
    const rootPost = page.locator('div.post', {hasText: marker}).first();
    await rootPost.getByText(/\d+ repl/).waitFor({timeout});
    await rootPost.getByText(/\d+ repl/).click();
    await page.locator('#rhsContainer').waitFor({state: 'visible', timeout: 15000});
    await page.waitForTimeout(500);
}

test.describe('Agent delegation (ask_agent)', () => {
    // Serial: one shared Mattermost + aimock sidecar; fixtures reload per test.
    test.describe.configure({mode: 'serial'});

    test.beforeAll(async () => {
        test.setTimeout(240000);
        mattermost = await RunSystemConsoleContainer({
            enableChannelMentionToolCalling: true,
            defaultBotName: orchestratorBot,
            services: [{...AIMOCK_COMPATIBLE_SERVICE}],
            bots: [
                {
                    id: 'orchestrator-bot',
                    name: orchestratorBot,
                    displayName: 'Orchestrator',
                    serviceID: AIMOCK_COMPATIBLE_SERVICE.id,
                    customInstructions: '',
                    disableTools: false,
                    mcpDynamicToolLoading: false,
                    enabledNativeTools: [],
                },
                {
                    id: 'subagent-bot',
                    name: subagentBot,
                    displayName: 'Sub Agent',
                    serviceID: AIMOCK_COMPATIBLE_SERVICE.id,
                    customInstructions: '',
                    disableTools: false,
                    mcpDynamicToolLoading: false,
                    enabledNativeTools: [],
                },
            ],
            mcp: {
                enabled: true,
                enablePluginServer: true,
                embeddedServer: {
                    enabled: true,
                    // ask_agent defaults to policy "ask"; pin get_channel_info
                    // to "ask" for the nested-approval test.
                    tool_configs: [{name: 'get_channel_info', policy: 'ask', enabled: true}],
                },
                idleTimeoutMinutes: 30,
                servers: [],
            },
        });
        await setupRegularTestUser(mattermost);
        aimock = await RunAIMockSidecar(mattermost.network, {
            fixtures: {fixtures: [buildTitleFixture('Delegation bootstrap')]},
        });
    });

    test.afterAll(async () => {
        await aimock?.stop();
        if (mattermost) {
            await mattermost.stop();
        }
    });

    test('DM happy path: delegation card, visible sub-thread, synthesized answer', async ({page}) => {
        test.setTimeout(300000);

        const promptMarker = `delegate happy ${Date.now()}`;
        const taskMarker = `SUBTASK_HAPPY_${Date.now()}`;
        const subAnswer = `SUB_ANSWER_HAPPY: the realtime sync engine shipped. ${Date.now()}`;
        const finalText = `FINAL_HAPPY_${Date.now()}`;
        const parentCallId = `call_ask_agent_happy_${Date.now()}`;

        await aimock.setFixtures(mergeFixtureFiles(
            {fixtures: [buildTitleFixture('Delegation happy path')]},
            buildDelegationFixtures({promptMarker, taskMarker, parentCallId, subAnswer, finalText}),
        ));

        const mm = new MattermostPage(page);
        const baseUrl = mattermost.url();
        await mm.login(baseUrl, username, password);
        await openDMWithBot(page, baseUrl, orchestratorBot);

        await mm.sendChannelMessage(promptMarker);
        await openThreadForMarker(page, promptMarker);
        const rhs = page.locator('#rhsContainer');

        // Delegation card renders instead of the generic tool card, pending approval.
        const card = rhs.getByTestId('delegation-card').last();
        await card.waitFor({state: 'visible', timeout: 120000});
        await expect(rhs.getByText('Waiting for your approval to delegate this task')).toBeVisible({timeout: 30000});

        await card.getByRole('button', {name: 'Accept', exact: true}).click();

        // The card completes and links to the delegation conversation.
        await expect(card.getByText('Completed', {exact: true})).toBeVisible({timeout: 120000});
        await expect(rhs.getByTestId('delegation-view-conversation').last()).toBeVisible({timeout: 15000});

        // Orchestrator synthesizes using the sub answer.
        await expect(rhs.getByText(finalText)).toBeVisible({timeout: 120000});

        // The delegation thread lives in the initiator's DM with the sub-agent.
        await openDMWithBot(page, baseUrl, subagentBot);
        await expect(page.getByText(`Task from @${orchestratorBot} on behalf of @${username}`).last()).toBeVisible({timeout: 30000});
        await openThreadForMarker(page, taskMarker, 30000);
        await expect(rhs.getByText(subAnswer.slice(0, 40)).last()).toBeVisible({timeout: 30000});
    });

    test('Nested approval: sub-agent tool approval in the delegation thread resumes the parent', async ({page}) => {
        test.setTimeout(300000);

        const promptMarker = `delegate nested ${Date.now()}`;
        const taskMarker = `SUBTASK_NESTED_${Date.now()}`;
        const subAnswer = `SUB_ANSWER_NESTED_${Date.now()}`;
        const finalText = `FINAL_NESTED_${Date.now()}`;
        const parentCallId = `call_ask_agent_nested_${Date.now()}`;
        const nestedToolCallId = `call_channel_info_${Date.now()}`;

        const userClient = await mattermost.getClient(username, password);
        const teams = await userClient.getMyTeams();
        const channels = await userClient.getMyChannels(teams[0].id);
        const townSquare = channels.find((channel: {name: string}) => channel.name === 'town-square');
        expect(townSquare).toBeTruthy();

        await aimock.setFixtures(mergeFixtureFiles(
            {fixtures: [buildTitleFixture('Delegation nested approval')]},
            buildDelegationFixtures({
                promptMarker,
                taskMarker,
                parentCallId,
                subAnswer,
                finalText,
                nestedToolCallId,
                nestedToolChannelId: townSquare!.id,
            }),
        ));

        const mm = new MattermostPage(page);
        const baseUrl = mattermost.url();
        await mm.login(baseUrl, username, password);
        await openDMWithBot(page, baseUrl, orchestratorBot);

        await mm.sendChannelMessage(promptMarker);
        await openThreadForMarker(page, promptMarker);
        const rhs = page.locator('#rhsContainer');

        const card = rhs.getByTestId('delegation-card').last();
        await card.waitFor({state: 'visible', timeout: 120000});
        await card.getByRole('button', {name: 'Accept', exact: true}).click();

        // Sub-agent hits the ask-policy tool: the parent card flips to
        // waiting-on-you while the approval card renders in the delegation thread.
        await expect(rhs.getByText('Waiting on you')).toBeVisible({timeout: 120000});

        // The parent keeps waiting (no park): approve in the delegation thread.
        await openDMWithBot(page, baseUrl, subagentBot);
        await openThreadForMarker(page, taskMarker);
        const nestedAccept = rhs.getByRole('button', {name: 'Accept', exact: true}).last();
        await nestedAccept.waitFor({timeout: 60000});
        await nestedAccept.click();

        // Sub answer streams into the delegation thread.
        await expect(rhs.getByText(subAnswer)).toBeVisible({timeout: 120000});

        // Parent resumes and synthesizes.
        await openDMWithBot(page, baseUrl, orchestratorBot);
        await openThreadForMarker(page, promptMarker);
        await expect(rhs.getByTestId('delegation-card').last().getByText('Completed', {exact: true})).toBeVisible({timeout: 120000});
        await expect(rhs.getByText(finalText)).toBeVisible({timeout: 120000});
    });

    test('Channel mention: sub answer stays private until shared', async ({page}) => {
        test.setTimeout(300000);

        const promptMarker = `delegate channel ${Date.now()}`;
        const taskMarker = `SUBTASK_CHANNEL_${Date.now()}`;
        const subAnswer = `SUB_ANSWER_CHANNEL_${Date.now()}`;
        const finalText = `FINAL_CHANNEL_${Date.now()}`;
        const parentCallId = `call_ask_agent_channel_${Date.now()}`;

        await aimock.setFixtures(mergeFixtureFiles(
            {fixtures: [buildTitleFixture('Delegation channel flow')]},
            buildDelegationFixtures({promptMarker, taskMarker, parentCallId, subAnswer, finalText}),
        ));

        const mm = new MattermostPage(page);
        const baseUrl = mattermost.url();
        await mm.login(baseUrl, username, password);
        await page.goto(`${baseUrl}/test/channels/off-topic`);
        await page.getByTestId('channel_view').waitFor({state: 'visible', timeout: 30000});
        await page.waitForTimeout(1000);

        await mm.mentionBot(orchestratorBot, promptMarker);
        await openThreadForMarker(page, promptMarker);
        const rhs = page.locator('#rhsContainer');

        const card = rhs.getByTestId('delegation-card').last();
        await card.waitFor({state: 'visible', timeout: 120000});
        await card.getByRole('button', {name: 'Accept', exact: true}).click();

        // In channels the executed result waits for a share decision; the
        // synthesis must not stream before Share.
        const shareButton = rhs.getByRole('button', {name: 'Share', exact: true}).last();
        await shareButton.waitFor({timeout: 120000});
        await expect(rhs.getByText(finalText)).not.toBeVisible();

        await shareButton.click();

        // Channel-visible synthesis streams after sharing.
        await expect(rhs.getByText(finalText)).toBeVisible({timeout: 120000});
    });
});
