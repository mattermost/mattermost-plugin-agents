import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import {
    OpenAIMockContainer,
    RunOpenAIMocks,
    buildChatCompletionMockRule,
    buildTextResponse,
    buildToolCallResponse,
    titleGenerationMockRule,
} from 'helpers/openai-mock';
import { RunToolConfigContainerForScopedAgents, setupRegularTestUser } from 'helpers/tool-config-container';
import { AgentAPIHelper } from 'helpers/agent-api';
import { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Scoped channel mode with a mocked LLM. The agent's channel turns read through
 * a server-scoped session for its bot: the triggering channel, public channels
 * on open teams, and nothing else, with no approval step. The mock answers the
 * follow-up only once the tool result reaches it, so a reply proves the tool
 * ran without anyone approving it; mock history shows what the tool could read.
 *
 * Scoped sessions need a Mattermost server that can mint them (12.2+); on an
 * older server every test is skipped.
 */

const botUsername = 'scopedagent';
const readChannelTool = 'mattermost__read_channel';
const searchPostsTool = 'mattermost__search_posts';
const minServerVersion = [12, 2, 0];

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;
let serverSupported = false;
let teamID: string;
let townSquareID: string;

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

// mockToolTurn makes the agent call tool once with args, then answer with
// done. The follow-up rule is registered last so it wins for the request that
// carries the tool result.
async function mockToolTurn(userMessage: string, callID: string, tool: string, args: object, done: string) {
    await openAIMock.addMocks([
        titleGenerationMockRule(),
        buildChatCompletionMockRule(
            buildToolCallResponse(callID, tool, JSON.stringify(args)),
            {bodyContains: userMessage},
        ),
        buildChatCompletionMockRule(buildTextResponse(done), {bodyContains: callID}),
    ]);
}

function mockReadChannelTurn(userMessage: string, callID: string, channelID: string, done: string) {
    return mockToolTurn(userMessage, callID, readChannelTool, {channel_id: channelID}, done);
}

// mentionAgentAndAwaitReply @mentions the agent in Town Square as the admin
// and waits for its reply in that thread to contain done.
async function mentionAgentAndAwaitReply(message: string, done: string) {
    const adminClient = await mattermost.getAdminClient();
    const bot = await adminClient.getUserByUsername(botUsername);
    const root = await adminClient.createPost({channel_id: townSquareID, message: `@${botUsername} ${message}`} as any);
    await expect.poll(async () => {
        const thread = await adminClient.getPostThread(root.id);
        return Object.values(thread.posts).some((post) => post.user_id === bot.id && post.message.includes(done));
    }, {
        message: `expected ${botUsername} to reply with ${done}`,
        timeout: 45000,
        intervals: [500, 1000, 2000],
    }).toBe(true);
}

// botPostsIn counts the agent's posts in channelID outside the thread rooted
// at exceptRootID.
async function botPostsIn(channelID: string, exceptRootID = '') {
    const adminClient = await mattermost.getAdminClient();
    const bot = await adminClient.getUserByUsername(botUsername);
    const posts = await adminClient.getPosts(channelID);
    return Object.values(posts.posts).filter((post) => post.user_id === bot.id && (!exceptRootID || post.root_id !== exceptRootID)).length;
}

test.describe.serial('Scoped channel mode (Mocked LLM)', () => {
    test.beforeAll(async () => {
        test.setTimeout(180000);
        mattermost = await RunToolConfigContainerForScopedAgents();
        openAIMock = await RunOpenAIMocks(mattermost.network);

        const adminClient = await mattermost.getAdminClient();
        const config = await adminClient.getClientConfig();
        serverSupported = versionAtLeast(config.Version ?? '', minServerVersion);
        if (!serverSupported) {
            return;
        }

        await setupRegularTestUser(mattermost);
        const team = await adminClient.getTeamByName('test');
        await adminClient.patchTeam({id: team.id, allow_open_invite: true} as any);
        teamID = team.id;
        townSquareID = (await adminClient.getChannelByName(team.id, 'town-square')).id;

        await new AgentAPIHelper(mattermost.url()).createAgent(adminClient.getToken(), {
            displayName: 'Scoped Agent',
            username: botUsername,
            serviceID: 'mock-service',
            customInstructions: '',
            mcpDynamicToolLoading: false,
            enabledNativeTools: [],
            useServiceAccountAuth: true,
            scopedChannelMode: true,
            autoEnableNewMCPTools: false,
            enabledMCPTools: [
                {server_origin: 'embedded://mattermost', tool_name: 'read_channel'},
                {server_origin: 'embedded://mattermost', tool_name: 'search_posts'},
            ],
        });
    });

    test.beforeEach(() => {
        test.skip(!serverSupported, 'scoped sessions need Mattermost Server 12.2 or later');
    });

    test.afterAll(async () => {
        await openAIMock?.stop();
        await mattermost?.stop();
    });

    test('the agent, on no team, reads a public channel on an open team without asking', async () => {
        test.setTimeout(120000);
        const runID = Date.now();
        const evidence = `SCOPED_EVIDENCE_${runID}`;
        const done = `SCOPED_DONE_${runID}`;
        const userMessage = `find the evidence ${runID}`;
        const evidenceChannel = await createChannelWithPost(`scoped-evidence-${runID}`, 'O', evidence);
        await mockReadChannelTurn(userMessage, `call_evidence_${runID}`, evidenceChannel.id, done);

        await mentionAgentAndAwaitReply(userMessage, done);

        expect(await openAIMock.countRequestsContaining(evidence)).toBeGreaterThan(0);
        expect(await botPostsIn(evidenceChannel.id)).toBe(0);
    });

    test("the bot's DM and a private channel it was added to stay unreachable", async () => {
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
        const dmPost = await userClient.createPost({channel_id: dm.id, message: dmSecret} as any);

        // The agent answers DMs as usual. Let that reply finish before the
        // scoped turns, whose mocks reset the request history.
        await expect.poll(async () => {
            const thread = await adminClient.getPostThread(dmPost.id);
            return Object.values(thread.posts).some((post) => post.user_id === bot.id);
        }, {timeout: 45000}).toBe(true);

        for (const target of [
            {name: 'private', channelID: privateChannel.id, secret: privateSecret},
            {name: 'dm', channelID: dm.id, secret: dmSecret},
        ]) {
            const done = `SCOPED_DENIED_${target.name}_${runID}`;
            const userMessage = `read the ${target.name} channel ${runID}`;
            await mockReadChannelTurn(userMessage, `call_${target.name}_${runID}`, target.channelID, done);

            await mentionAgentAndAwaitReply(userMessage, done);

            expect(await openAIMock.countRequestsContaining(`call_${target.name}_${runID}`)).toBeGreaterThan(0);
            expect(await openAIMock.countRequestsContaining(target.secret)).toBe(0);
        }
        expect(await botPostsIn(privateChannel.id)).toBe(0);
        expect(await botPostsIn(dm.id, dmPost.id)).toBe(0);
    });

    test('keyword search only returns posts in scope', async () => {
        test.setTimeout(120000);
        const runID = Date.now();
        const adminClient = await mattermost.getAdminClient();
        const bot = await adminClient.getUserByUsername(botUsername);
        const term = `scopedterm${runID}`;

        const evidenceChannel = await createChannelWithPost(`scoped-search-${runID}`, 'O', `${term} SEARCH_EVIDENCE_${runID}`);
        const privateChannel = await createChannelWithPost(`search-private-${runID}`, 'P', `${term} SEARCH_PRIVATE_${runID}`);
        await adminClient.addToChannel(bot.id, privateChannel.id);

        const done = `SCOPED_SEARCHED_${runID}`;
        const userMessage = `search for ${term}`;
        await mockToolTurn(userMessage, `call_search_${runID}`, searchPostsTool, {query: term, team_id: teamID}, done);

        await mentionAgentAndAwaitReply(userMessage, done);

        expect(await openAIMock.countRequestsContaining(`SEARCH_EVIDENCE_${runID}`)).toBeGreaterThan(0);
        expect(await openAIMock.countRequestsContaining(`SEARCH_PRIVATE_${runID}`)).toBe(0);
        expect(await botPostsIn(evidenceChannel.id)).toBe(0);
    });

    test('every run revoked its scoped session', async () => {
        const adminClient = await mattermost.getAdminClient();
        const bot = await adminClient.getUserByUsername(botUsername);
        await expect.poll(async () => {
            const sessions = await adminClient.getSessions(bot.id);
            return sessions.filter((session) => session.props?.scope).length;
        }, {timeout: 15000}).toBe(0);
    });
});
