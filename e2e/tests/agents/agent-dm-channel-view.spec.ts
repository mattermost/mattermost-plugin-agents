// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// An agent's DM in the Channels app shows the Agents product's chat (registerChannelViewComponent),
// with the chat's title bar in place of the host's channel header, and the agent's recent chats nested
// under its item in the sidebar (registerSidebarChannelChildrenComponent). DMs with people keep the
// post list. Hosts without both keep every DM as plain messages, so the agent view tests skip there.

import {expect, type Page} from '@playwright/test';

import {AIMockContainer, RunAIMockSidecar} from 'helpers/aimock-container';
import {buildTextResponse, TITLE_GENERATION_PROMPT_PREFIX} from 'helpers/aimock-fixtures';
import {AgentChatPage} from 'helpers/agent-chat-page';
import {hostShowsAgentDMView} from 'helpers/agent-dm-view';
import {containerTest} from 'helpers/container-fixture';
import {withDeadline} from 'helpers/deadline';
import {MattermostPage} from 'helpers/mm';
import MattermostContainer from 'helpers/mmcontainer';
import {AIMOCK_BOT_NAME, RunAIMockContainer} from 'helpers/plugincontainer';

const username = 'regularuser';
const password = 'regularuser';
const botDisplayName = 'Aimock Bot';
const teamName = 'test';

const NO_HOST_SUPPORT = 'The Mattermost image has no registerChannelViewComponent (set MM_IMAGE to a build from master), so agent DMs stay plain messages.';

// Replies carry a marker that does not repeat the prompt, so text locators cannot match the wrong post.
interface Chat {prompt: string; reply: string; title: string}
const LIST_CHAT: Chat = {prompt: 'dm-view-list-001', reply: 'DM view list reply marker.', title: 'DM View Listed Chat'};
const PICK_CHAT: Chat = {prompt: 'dm-view-pick-002', reply: 'DM view picked reply marker.', title: 'DM View Picked Chat'};
const LINK_CHAT: Chat = {prompt: 'dm-view-link-003', reply: 'DM view permalink reply marker.', title: 'DM View Linked Chat'};
const NEW_CHAT: Chat = {prompt: 'dm-view-new-004', reply: 'DM view new chat reply marker.', title: 'DM View New Chat'};

// The title request embeds the prompt (after a newline), so its fixture must come before the one for the prompt itself.
const fixtures = () => [LIST_CHAT, PICK_CHAT, LINK_CHAT, NEW_CHAT].flatMap(({prompt, reply, title}) => [
    {match: {userMessage: `${TITLE_GENERATION_PROMPT_PREFIX}\n${prompt}`}, response: {content: title}},
    ...buildTextResponse({userMessage: prompt, content: reply}).fixtures,
]);

let mattermost: MattermostContainer;
let aimock: AIMockContainer;
let botUserId = '';
let dmChannelId = '';
let personDMChannelId = '';

const dmUrl = (postId?: string) => `${mattermost.url()}/${teamName}/messages/@${AIMOCK_BOT_NAME}${postId ? `/${postId}` : ''}`;

const agentView = (page: Page) => page.getByTestId('agent-dm-channel-view');
const chatList = (page: Page) => page.getByRole('list', {name: `${botDisplayName} chats`});
const chatThread = (page: Page) => agentView(page).getByTestId('agent-chat-thread');

// Opens the agent DM (optionally a permalink in it) and skips the test on hosts that keep it as plain messages.
async function openAgentDM(page: Page, postId?: string): Promise<void> {
    await new MattermostPage(page).login(mattermost.url(), username, password);
    await page.goto(dmUrl(postId), {waitUntil: 'domcontentloaded'});
    test.skip(!(await hostShowsAgentDMView(page, dmChannelId)), NO_HOST_SUPPORT);
}

// Creates a finished chat through the API (a DM to the agent) and returns the root post id and the reply's id.
async function seedChat(chat: Chat): Promise<{rootId: string; replyId: string}> {
    const client = await mattermost.getClient(username, password);
    const root = await client.createPost({channel_id: dmChannelId, message: chat.prompt} as any);
    let replyId = '';
    await expect.poll(async () => {
        const thread = await client.getPostThread(root.id);
        replyId = Object.values(thread.posts).find((p) => p.user_id === botUserId && p.message.includes(chat.reply))?.id ?? '';
        return replyId;
    }, {timeout: 60000}).not.toBe('');
    return {rootId: root.id, replyId};
}

const MATTERMOST_START_MS = 240000;
const AIMOCK_START_MS = 120000;

const test = containerTest(async (own) => {
    const mattermost = own(await withDeadline('Starting Mattermost', MATTERMOST_START_MS, () => RunAIMockContainer()));
    const aimock = own(await withDeadline('Starting aimock', AIMOCK_START_MS, () => RunAIMockSidecar(mattermost.network, {fixtures: fixtures()})));
    return {mattermost, aimock};
});

test.describe('Agent DM channel view', () => {
    test.beforeAll(async ({containers}) => {
        ({mattermost, aimock} = containers);
        const client = await mattermost.getClient(username, password);
        const me = await client.getMe();
        botUserId = (await client.getUserByUsername(AIMOCK_BOT_NAME)).id;
        dmChannelId = (await client.createDirectChannel([me.id, botUserId])).id;
        const person = await client.getUserByUsername(mattermost.username);
        personDMChannelId = (await client.createDirectChannel([me.id, person.id])).id;
        await seedChat(LIST_CHAT);
    });

    test('an agent DM shows the agent chat in place of the host header, with its chats under the agent in the sidebar', async ({page}) => {
        await openAgentDM(page);

        await expect(chatList(page).getByRole('button', {name: LIST_CHAT.title, exact: true})).toBeVisible({timeout: 30000});
        await expect(new AgentChatPage(page).newChatTextbox()).toBeEnabled({timeout: 30000});

        // One header: the chat's own title bar names the agent, the host's channel header is hidden.
        await expect(page.locator('#channel-header')).toBeHidden();
        await expect(agentView(page).getByTestId('agent-chat-menu-button')).toContainText(botDisplayName);

        // No post list or channel composer (the new chat's composer is the host editor, in the agent view).
        await expect(page.getByTestId('post-create')).toHaveCount(0);
        await expect(page.locator('#postListContent')).toHaveCount(0);
    });

    test('picking a chat opens it in place and puts it in the URL, which survives a reload', async ({page}) => {
        const {rootId} = await seedChat(PICK_CHAT);
        await openAgentDM(page);

        await chatList(page).getByRole('button', {name: PICK_CHAT.title, exact: true}).click({timeout: 30000});
        await expect(chatThread(page).getByText(PICK_CHAT.reply)).toBeVisible({timeout: 30000});
        await expect(page).toHaveURL(new RegExp(`/${teamName}/messages/@${AIMOCK_BOT_NAME}/${rootId}$`));
        await expect(agentView(page).getByRole('heading', {level: 1, name: PICK_CHAT.title})).toBeVisible();

        await page.reload({waitUntil: 'domcontentloaded'});
        await expect(chatThread(page).getByText(PICK_CHAT.reply)).toBeVisible({timeout: 30000});

        // New chat leaves the chat and takes it out of the URL.
        await agentView(page).getByTestId('agent-chat-new-chat-button').click();
        await expect(new AgentChatPage(page).newChatComposer()).toBeVisible();
        await expect(page).toHaveURL(new RegExp(`/${teamName}/messages/@${AIMOCK_BOT_NAME}$`));
    });

    test('a chat started in the DM opens there and joins the list', async ({page}) => {
        await openAgentDM(page);

        const chat = new AgentChatPage(page);
        await expect(chat.newChatTextbox()).toBeEnabled({timeout: 30000});
        await chat.newChatTextbox().fill(NEW_CHAT.prompt);
        await chat.pressEnterUntilSent(botUserId, new RegExp(`/${teamName}/messages/@${AIMOCK_BOT_NAME}/[a-z0-9]{26}$`));

        await expect(chatThread(page).getByText(NEW_CHAT.reply)).toBeVisible({timeout: 60000});
        await expect(chatList(page).getByRole('button', {name: NEW_CHAT.title, exact: true})).toBeVisible({timeout: 30000});
    });

    test('a permalink to an agent reply opens its chat', async ({page}) => {
        const {replyId} = await seedChat(LINK_CHAT);
        await openAgentDM(page, replyId);

        await expect(chatThread(page).getByText(LINK_CHAT.reply)).toBeVisible({timeout: 30000});
        await expect(chatThread(page).getByText(LINK_CHAT.prompt, {exact: true})).toBeVisible();
        await expect(chatThread(page).getByTestId('reply_textbox')).toBeEnabled({timeout: 30000});

        // The host drops the post id from the URL a few seconds after a permalink opens; the chat stays.
        await expect(page).toHaveURL(new RegExp(`/${teamName}/messages/@${AIMOCK_BOT_NAME}$`), {timeout: 15000});
        await expect(chatThread(page).getByText(LINK_CHAT.reply)).toBeVisible();

        // The host's permalink handling may also open the thread in its RHS (collapsed reply threads).
        test.info().annotations.push({type: 'host RHS thread open after reply permalink', description: String(await page.locator('#rhsContainer').getByText(LINK_CHAT.reply).isVisible())});
    });

    test('a DM with a person keeps its messages', async ({page}) => {
        await new MattermostPage(page).login(mattermost.url(), username, password);
        await page.goto(`${mattermost.url()}/${teamName}/messages/@${mattermost.username}`, {waitUntil: 'domcontentloaded'});

        expect(await hostShowsAgentDMView(page, personDMChannelId)).toBe(false);
        await expect(page.getByTestId('post-create').getByTestId('post_textbox')).toBeEnabled();
    });
});
