import { test, expect, type Page } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import {
    OpenAIMockContainer,
    RunOpenAIMocks,
    buildChatCompletionMockRule,
    buildTextResponse,
    buildToolCallResponse,
    titleGenerationMockRule,
} from 'helpers/openai-mock';
import { RunToolConfigContainerWithScopedAgent, setupRegularTestUser } from 'helpers/tool-config-container';
import { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Scoped channel mode with a mocked LLM. The agent's channel turns read through
 * a server-scoped session for its bot: the triggering channel, public channels
 * on open teams, and nothing else, with no approval step.
 *
 * Scoped sessions need a Mattermost server that can mint them (12.2+); on an
 * older server every test is skipped.
 */

const botUsername = 'scopedagent';
const botDisplayName = 'Scoped Agent';
const readChannelTool = 'mattermost__read_channel';
const minServerVersion = [12, 2, 0];

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;
let serverSupported = false;
let teamID: string;

function versionAtLeast(version: string, minimum: number[]): boolean {
    const parts = version.split('.').map((part) => parseInt(part, 10));
    for (let i = 0; i < minimum.length; i++) {
        if (Number.isNaN(parts[i])) {
            return false;
        }
        if (parts[i] !== minimum[i]) {
            return parts[i] > minimum[i];
        }
    }
    return true;
}

async function createChannelWithPost(name: string, type: 'O' | 'P', message: string) {
    const adminClient = await mattermost.getAdminClient();
    const channel = await adminClient.createChannel({team_id: teamID, name, display_name: name, type} as any);
    await adminClient.createPost({channel_id: channel.id, message} as any);
    return channel;
}

// mockReadChannelTurn makes the agent read channelID once, then answer with
// done. The follow-up rule is registered last so it wins for the request that
// carries the tool result.
async function mockReadChannelTurn(userMessage: string, callID: string, channelID: string, done: string) {
    await openAIMock.addMocks([
        titleGenerationMockRule(),
        buildChatCompletionMockRule(
            buildToolCallResponse(callID, readChannelTool, JSON.stringify({channel_id: channelID})),
            {bodyContains: userMessage},
        ),
        buildChatCompletionMockRule(buildTextResponse(done), {bodyContains: callID}),
    ]);
}

async function mentionAgentAndOpenThread(page: Page, message: string) {
    const mmPage = new MattermostPage(page);
    await mmPage.login(mattermost.url(), adminUsername, adminPassword);
    await mmPage.goto('test', 'channels/town-square');
    await mmPage.mentionBot(botUsername, message);

    const sentPost = page.locator('.post').filter({
        has: page.locator('.post-message__text').getByText(`@${botUsername} ${message}`, {exact: true}),
    }).last();
    await expect(sentPost).toBeVisible({timeout: 30000});
    const replyIndicator = sentPost.getByText(/\d+ repl/i);
    await expect(replyIndicator).toBeVisible({timeout: 30000});
    await replyIndicator.click();

    const rhs = page.locator('#rhsContainer');
    await expect(rhs.locator('[data-testid="llm-bot-post"]').first()).toBeVisible({timeout: 10000});
    return rhs;
}

async function botPostsOutsideTownSquare(channelIDs: string[]) {
    const adminClient = await mattermost.getAdminClient();
    const bot = await adminClient.getUserByUsername(botUsername);
    let count = 0;
    for (const channelID of channelIDs) {
        const posts = await adminClient.getPosts(channelID);
        count += Object.values(posts.posts).filter((post) => post.user_id === bot.id).length;
    }
    return count;
}

test.describe.serial('Scoped channel mode (Mocked LLM)', () => {
    test.beforeAll(async () => {
        test.setTimeout(180000);
        mattermost = await RunToolConfigContainerWithScopedAgent();
        openAIMock = await RunOpenAIMocks(mattermost.network);

        const adminClient = await mattermost.getAdminClient();
        const config = await adminClient.getClientConfigOld();
        serverSupported = versionAtLeast(config.Version ?? '', minServerVersion);
        if (!serverSupported) {
            return;
        }

        await setupRegularTestUser(mattermost);
        const team = await adminClient.getTeamByName('test');
        await adminClient.patchTeam({id: team.id, allow_open_invite: true} as any);
        teamID = team.id;
    });

    test.beforeEach(() => {
        test.skip(!serverSupported, 'scoped sessions need Mattermost Server 12.2 or later');
    });

    test.afterAll(async () => {
        await openAIMock?.stop();
        await mattermost?.stop();
    });

    test('the agent, on no team, reads a public channel on an open team without asking', async ({ page }) => {
        test.setTimeout(120000);
        const runID = Date.now();
        const evidence = `SCOPED_EVIDENCE_${runID}`;
        const done = `SCOPED_DONE_${runID}`;
        const userMessage = `find the evidence ${runID}`;
        const evidenceChannel = await createChannelWithPost(`scoped-evidence-${runID}`, 'O', evidence);
        await mockReadChannelTurn(userMessage, `call_evidence_${runID}`, evidenceChannel.id, done);

        const rhs = await mentionAgentAndOpenThread(page, userMessage);

        await expect(rhs.getByText(done)).toBeVisible({timeout: 45000});
        await expect(rhs.getByText('Auto-approved')).toBeVisible();
        await expect(rhs.getByRole('button', {name: /^accept$/i})).not.toBeVisible();
        await expect.poll(() => openAIMock.countRequestsContaining(evidence)).toBeGreaterThan(0);
        expect(await botPostsOutsideTownSquare([evidenceChannel.id])).toBe(0);
    });

    test("the bot's DM and a private channel it was added to stay unreachable", async ({ page }) => {
        test.setTimeout(150000);
        const runID = Date.now();
        const adminClient = await mattermost.getAdminClient();
        const bot = await adminClient.getUserByUsername(botUsername);

        // Membership grants nothing to a scoped session.
        await mattermost.addUserToTeam(botUsername, 'test');
        const privateSecret = `SCOPED_PRIVATE_SECRET_${runID}`;
        const privateChannel = await createChannelWithPost(`bot-private-${runID}`, 'P', privateSecret);
        await adminClient.addToChannel(bot.id, privateChannel.id);

        const dmSecret = `SCOPED_DM_SECRET_${runID}`;
        const userClient = await mattermost.getClient('regularuser', 'regularuser');
        const regularUser = await userClient.getMe();
        const dm = await userClient.createDirectChannel([regularUser.id, bot.id]);
        await userClient.createPost({channel_id: dm.id, message: dmSecret} as any);

        for (const target of [
            {name: 'private', channelID: privateChannel.id, secret: privateSecret},
            {name: 'dm', channelID: dm.id, secret: dmSecret},
        ]) {
            const done = `SCOPED_DENIED_${target.name}_${runID}`;
            const userMessage = `read the ${target.name} channel ${runID}`;
            await mockReadChannelTurn(userMessage, `call_${target.name}_${runID}`, target.channelID, done);

            const rhs = await mentionAgentAndOpenThread(page, userMessage);

            await expect(rhs.getByText(done)).toBeVisible({timeout: 45000});
            await expect(rhs.getByRole('button', {name: /^accept$/i})).not.toBeVisible();
            expect(await openAIMock.countRequestsContaining(target.secret)).toBe(0);
        }
        expect(await botPostsOutsideTownSquare([privateChannel.id, dm.id])).toBe(0);
    });
});
