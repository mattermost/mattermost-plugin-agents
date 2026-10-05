// spec: channel agent context — channel instructions and agent-context pins reach the LLM request
// seed: tests/seed.spec.ts
//
// Context is configured via the REST API; the channel-settings tab and post
// menu are covered by webapp unit tests. Each round swaps in a pair of
// mutually exclusive smocker mocks keyed on sentinel strings, so which reply
// the agent streams proves whether the sentinel reached the LLM request.

import { test, expect } from '@playwright/test';
import type { Client4 } from '@mattermost/client';
import type { Channel } from '@mattermost/types/channels';
import type { UserProfile } from '@mattermost/types/users';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { mattermostAIPluginRoutes } from 'helpers/plugin-http';
import {
    OpenAIMockContainer,
    RunOpenAIMocks,
    responseTest,
    responseTest2,
    responseTest2Text,
    responseTestText,
} from 'helpers/openai-mock';

const username = 'regularuser';
const password = 'regularuser';

type ContextPosts = { posts: Array<{ post_id: string }>; max_posts: number };

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.beforeAll(async () => {
    mattermost = await RunContainer();
    openAIMock = await RunOpenAIMocks(mattermost.network);
});

test.afterAll(async () => {
    await openAIMock?.stop();
    await mattermost?.stop();
});

/**
 * regularuser creates the channel, so context writes run as a regular member
 * holding the default manage_public_channel_properties permission.
 */
async function setupClientAndChannel(): Promise<{ client: Client4; botUser: UserProfile; channel: Channel }> {
    const client = await mattermost.getClient(username, password);
    const team = (await client.getMyTeams())[0];
    const botUser = await client.getUserByUsername('mock');
    const channel = await client.createChannel({
        team_id: team.id,
        name: `agent-context-${Date.now()}`,
        display_name: 'Agent context',
        type: 'O',
    } as Channel);
    return { client, botUser, channel };
}

function completionMock(bodyMatcher: { matcher: string; value: string }, response: string) {
    return {
        request: { method: 'POST', path: '/v1/chat/completions', body: bodyMatcher },
        context: { times: 100 },
        response: { status: 200, headers: { 'Content-Type': 'text/event-stream' }, body: response },
    };
}

/** Answers responseTest2 when the request matches, responseTest otherwise. */
async function mockByRequestBody(positive: string, negative: string, value: string) {
    await openAIMock.resetMocks();
    await openAIMock.addMocks([
        completionMock({ matcher: positive, value }, responseTest2),
        completionMock({ matcher: negative, value }, responseTest),
    ]);
}

async function mentionAgent(client: Client4, mmPage: MattermostPage, channelId: string, botUserId: string, expectedText: string) {
    const root = await client.createPost({ channel_id: channelId, message: '@mock what should I know about this channel?' });
    await mmPage.expectBotThreadReplyFromApi(client, channelId, botUserId, root.id, expectedText);
}

test.describe('Channel agent context', () => {
    test('instructions and agent-context pins reach the agent; regular pinned messages do not', async ({ page }) => {
        test.setTimeout(180000);

        const { client, botUser, channel } = await setupClientAndChannel();
        const routes = mattermostAIPluginRoutes(mattermost.url());
        const token = client.getToken();
        const mmPage = new MattermostPage(page);
        const tag = Date.now();
        const instructionsSentinel = `CHANNEL-INSTRUCTIONS-${tag}`;
        const pinnedSentinel = `AGENT-CONTEXT-PIN-${tag}`;
        const regularPinSentinel = `REGULAR-PIN-${tag}`;

        await routes.putJson(`channel/${channel.id}/instructions`, token, {
            instructions: `Follow the ${instructionsSentinel} release checklist.`,
        });
        expect(await routes.getJson(`channel/${channel.id}/instructions`, token)).toEqual({
            instructions: `Follow the ${instructionsSentinel} release checklist.`,
        });

        const contextPost = await client.createPost({ channel_id: channel.id, message: `Runbook: ${pinnedSentinel}` });
        const pinned = await routes.postJson(`channel/${channel.id}/context_posts`, token, { post_id: contextPost.id }) as ContextPosts;
        expect(pinned.posts.map((p) => p.post_id)).toEqual([contextPost.id]);

        const regularPost = await client.createPost({ channel_id: channel.id, message: `Lunch menu: ${regularPinSentinel}` });
        await client.pinPost(regularPost.id);

        // The two pin lists stay separate in both directions.
        const contextPins = await routes.getJson(`channel/${channel.id}/context_posts`, token) as ContextPosts;
        expect(contextPins.posts.map((p) => p.post_id)).toEqual([contextPost.id]);
        const channelPins = await client.getPinnedPosts(channel.id);
        expect(Object.keys(channelPins.posts)).toEqual([regularPost.id]);

        // Both the instructions and the pinned post are in the request.
        await mockByRequestBody('ShouldMatch', 'ShouldNotMatch', `${instructionsSentinel}[\\s\\S]*${pinnedSentinel}`);
        await mentionAgent(client, mmPage, channel.id, botUser.id, responseTest2Text);

        // The regular pinned message is not.
        await mockByRequestBody('ShouldContainSubstring', 'ShouldNotContainSubstring', regularPinSentinel);
        await mentionAgent(client, mmPage, channel.id, botUser.id, responseTestText);

        // Unpinning and clearing the instructions remove them from later requests.
        await routes.deleteRequest(`channel/${channel.id}/context_posts/${contextPost.id}`, token);
        await routes.putJson(`channel/${channel.id}/instructions`, token, { instructions: '' });
        await mockByRequestBody('ShouldMatch', 'ShouldNotMatch', `${instructionsSentinel}|${pinnedSentinel}`);
        await mentionAgent(client, mmPage, channel.id, botUser.id, responseTestText);
    });
});
