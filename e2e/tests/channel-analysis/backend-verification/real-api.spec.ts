// spec: tests/channel-analysis/integration.plan.md
// seed: tests/seed.spec.ts

import { test, expect, Page } from '@playwright/test';
import type { Client4 } from '@mattermost/client';

import { AIMockContainer, RunAIMockSidecar } from 'helpers/aimock-container';
import { buildToolNameAndTextResponse } from 'helpers/aimock-fixtures';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { LLMBotPostHelper } from 'helpers/llmbot-post';
import { AIMOCK_BOT_NAME, RunAIMockContainer } from 'helpers/plugincontainer';

const username = 'regularuser';
const password = 'regularuser';

const CHANNEL_ANALYSIS_MCP = {
    embeddedServer: { enabled: true },
    enablePluginServer: true,
    enabled: true,
    idleTimeoutMinutes: 30,
    servers: [] as unknown[],
};

type ConversationBlock = {
    type: string;
    id?: string;
    name?: string;
    tool_use_id?: string;
    content?: string;
};

type ConversationResponse = {
    turns: Array<{
        role: string;
        content: ConversationBlock[];
    }>;
};

type MattermostPost = {
    id: string;
    user_id: string;
    message: string;
    create_at: number;
    props?: {
        conversation_id?: string;
    };
};

class ChannelAnalysisBackendHelper {
    constructor(private page: Page) {}

    async waitForPageReady() {
        await this.page.waitForSelector('[class*="channel-header"], #channelHeaderInfo', { timeout: 30000 });
        await this.page.waitForTimeout(2000);
    }

    async navigateToChannel(mattermost: MattermostContainer, channelName: string) {
        await this.page.goto(mattermost.url() + `/test/channels/${channelName}`);
        await this.waitForPageReady();
    }
}

function buildReadChannelAnalysisFixtures(options: {
    toolCallId: string;
    finalContent: string;
    toolArguments?: Record<string, unknown>;
}) {
    return buildToolNameAndTextResponse({
        toolName: 'read_channel',
        toolCallId: options.toolCallId,
        finalContent: options.finalContent,
        toolArguments: options.toolArguments,
    });
}

function getPostsArray(postsResponse: {posts?: Record<string, MattermostPost>}): MattermostPost[] {
    return Object.values(postsResponse.posts || {});
}

async function fetchPostsForChannel(client: Client4, channelID: string): Promise<MattermostPost[]> {
    const getPosts = (client as unknown as { getPostsForChannel?: typeof client.getPosts }).getPostsForChannel ||
        client.getPosts;
    if (typeof getPosts !== 'function') {
        throw new Error('Mattermost client does not expose getPostsForChannel or getPosts');
    }

    const postsResponse = await getPosts.call(client, channelID, 0, 20);
    return getPostsArray(postsResponse);
}

async function fetchConversationForLatestLLMBotPost(
    mattermost: MattermostContainer,
): Promise<ConversationResponse> {
    const userClient = await mattermost.getClient(username, password);
    const user = await userClient.getMe();
    const botUser = await userClient.getUserByUsername(AIMOCK_BOT_NAME);
    const dmChannel = await userClient.createDirectChannel([user.id, botUser.id]);
    const latestBotPost = (await fetchPostsForChannel(userClient, dmChannel.id))
        .filter((post) => post.user_id === botUser.id && post.props?.conversation_id)
        .sort((a, b) => b.create_at - a.create_at)[0];

    if (!latestBotPost) {
        throw new Error(`Could not find latest ${AIMOCK_BOT_NAME} post with a conversation_id prop`);
    }

    const conversationID = latestBotPost.props?.conversation_id;
    if (!conversationID) {
        throw new Error(`Post ${latestBotPost.id} did not include a conversation_id prop`);
    }

    const response = await fetch(`${mattermost.url()}/plugins/mattermost-ai/conversations/${conversationID}`, {
        headers: {
            Authorization: `Bearer ${userClient.getToken()}`,
        },
    });
    if (!response.ok) {
        throw new Error(`Failed to fetch conversation ${conversationID}: ${response.status}`);
    }

    return response.json() as Promise<ConversationResponse>;
}

async function expectReadChannelToolResult(
    mattermost: MattermostContainer,
    expectedMarkers: string[],
    rejectedMarkers: string[] = [],
): Promise<void> {
    const conversation = await fetchConversationForLatestLLMBotPost(mattermost);
    const contentBlocks = conversation.turns.flatMap((turn) => turn.content);
    const readChannelToolUseIDs = new Set(contentBlocks
        .filter((block) => block.type === 'tool_use' && block.name === 'read_channel' && block.id)
        .map((block) => block.id!));
    const readChannelToolResultBlocks = conversation.turns
        .flatMap((turn) => turn.content)
        .filter((block) => block.type === 'tool_result' && (
            block.name === 'read_channel' ||
            (block.tool_use_id !== undefined && readChannelToolUseIDs.has(block.tool_use_id))
        ));
    if (readChannelToolResultBlocks.length === 0) {
        throw new Error('Could not find a read_channel tool_result block in the latest conversation');
    }

    const readChannelToolResult = readChannelToolResultBlocks
        .map((block) => block.content ?? '')
        .join('\n');

    for (const marker of expectedMarkers) {
        expect(readChannelToolResult).toContain(marker);
    }
    for (const marker of rejectedMarkers) {
        expect(readChannelToolResult).not.toContain(marker);
    }
}

test.describe('Channel Analysis Aimock Backend Verification', () => {
    test.describe.configure({ mode: 'serial' });

    let mattermost: MattermostContainer;
    let aimock: AIMockContainer;

    test.beforeAll(async () => {
        test.setTimeout(180000);
        mattermost = await RunAIMockContainer({ mcp: CHANNEL_ANALYSIS_MCP });
        // aimock strict mode requires at least one fixture at startup; each test replaces these.
        aimock = await RunAIMockSidecar(mattermost.network, {
            fixtures: buildReadChannelAnalysisFixtures({
                toolCallId: 'bootstrap_read_channel',
                finalContent: 'bootstrap response',
            }),
        });
    });

    test.afterAll(async () => {
        if (aimock) {
            await aimock.stop();
        }
        if (mattermost) {
            await mattermost.stop();
        }
    });

    test('Sanity check: Channel analysis produces valid summary', async ({ page }) => {
        test.setTimeout(360000);

        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);
        const llmBotHelper = new LLMBotPostHelper(page);
        const apiHelper = new ChannelAnalysisBackendHelper(page);

        const summaryMarker = `phase5-summary-sso-${Date.now()}`;
        const deadlineMarker = `phase5-summary-friday-${Date.now()}`;
        const toolCallId = `call_phase5_summary_read_${Date.now()}`;

        await aimock.setFixtures(
            buildReadChannelAnalysisFixtures({
                toolCallId,
                finalContent: 'The channel discussed implementing SSO, with the deadline next Friday.',
            }),
        );

        await mmPage.login(mattermost.url(), username, password);
        await apiHelper.waitForPageReady();

        await mmPage.sendChannelMessage(`Feature discussion ${summaryMarker}: We need to implement SSO.`);
        await mmPage.sendChannelMessage(`Deadline ${deadlineMarker}: Next Friday.`);

        await aiPlugin.openChannelAnalysisPopover();
        await aiPlugin.sendChannelAnalysisMessage('What feature and deadline were discussed?');

        await llmBotHelper.waitForStreamingComplete();

        const postText = llmBotHelper.getPostText();
        await expect(postText).toBeVisible();
        const content = await postText.textContent();
        expect(content).toBeTruthy();
        expect(content!.toLowerCase()).toContain('sso');
        expect(content!.toLowerCase()).toContain('friday');
        await expectReadChannelToolResult(mattermost, [summaryMarker, deadlineMarker]);
    });

    test('Context isolation: Analysis reflects correct channel after switching', async ({ page }) => {
        test.setTimeout(480000);

        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);
        const llmBotHelper = new LLMBotPostHelper(page);
        const apiHelper = new ChannelAnalysisBackendHelper(page);

        const townMarker = `phase5-town-picnic-${Date.now()}`;
        const offTopicMarker = `phase5-offtopic-scifi-${Date.now()}`;
        const toolCallId = `call_phase5_isolation_read_${Date.now()}`;

        await mmPage.login(mattermost.url(), username, password);
        await apiHelper.waitForPageReady();

        await mmPage.sendChannelMessage(`Town square topic ${townMarker}: Company picnic.`);
        await apiHelper.navigateToChannel(mattermost, 'off-topic');
        await mmPage.sendChannelMessage(`Off-topic discussion ${offTopicMarker}: Best sci-fi movies.`);

        await aimock.setFixtures(
            buildReadChannelAnalysisFixtures({
                toolCallId,
                finalContent: 'The active channel discussion is about sci-fi movies.',
            }),
        );

        await aiPlugin.openChannelAnalysisPopover();
        await aiPlugin.sendChannelAnalysisMessage('What is the discussion topic?');

        await llmBotHelper.waitForStreamingComplete();

        const postText = llmBotHelper.getPostText();
        await expect(postText).toBeVisible();
        const content = await postText.textContent();
        expect(content).toBeTruthy();
        expect(content!.toLowerCase()).toMatch(/sci-fi|movie/);
        expect(content!.toLowerCase()).not.toContain('picnic');
        expect(content!).not.toContain(townMarker);
        await expectReadChannelToolResult(mattermost, [offTopicMarker], [townMarker]);
    });
});
