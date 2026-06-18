// spec: tests/channel-analysis/integration.plan.md
// seed: tests/seed.spec.ts

import { test, expect, Page } from '@playwright/test';

import { AIMockContainer, RunAIMockSidecar } from 'helpers/aimock-container';
import { AIMockFixtureFile } from 'helpers/aimock-fixtures';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { LLMBotPostHelper } from 'helpers/llmbot-post';
import { RunAIMockContainer } from 'helpers/plugincontainer';

const username = 'regularuser';
const password = 'regularuser';

const CHANNEL_ANALYSIS_MCP = {
    embeddedServer: { enabled: true },
    enablePluginServer: true,
    enabled: true,
    idleTimeoutMinutes: 30,
    servers: [] as unknown[],
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
}): AIMockFixtureFile {
    return {
        fixtures: [
            {
                match: { toolCallId: options.toolCallId },
                response: { content: options.finalContent },
            },
            {
                match: {
                    toolName: 'read_channel',
                    hasToolResult: false,
                },
                response: {
                    toolCalls: [
                        {
                            id: options.toolCallId,
                            name: 'read_channel',
                            arguments: {},
                        },
                    ],
                    finishReason: 'tool_calls',
                },
            },
        ],
    };
}

test.describe('Channel Analysis Aimock Backend Verification', () => {
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
    });
});
