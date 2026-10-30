// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Agents product view: the product-switcher entry lands in a new chat with the
// workspace default agent, a left sidebar lists agents/threads/Custom Prompts,
// and a thread renders in the host ThreadViewer under the product chrome. The
// same thread must stay visible in the Channels DM with the agent.

import fs from 'fs';
import path from 'path';

import {expect, type Page} from '@playwright/test';

import {AIMockContainer, RunAIMockSidecar} from 'helpers/aimock-container';
import {containerTest} from 'helpers/container-fixture';
import {withDeadline} from 'helpers/deadline';
import {buildTextResponse, TITLE_GENERATION_PROMPT_PREFIX} from 'helpers/aimock-fixtures';
import MattermostContainer from 'helpers/mmcontainer';
import {MattermostPage} from 'helpers/mm';
import {AIMOCK_BOT_NAME, RunAIMockContainer} from 'helpers/plugincontainer';

const username = 'regularuser';
const password = 'regularuser';
const botDisplayName = 'Aimock Bot';
const teamName = 'test';

const FIRST_PROMPT = 'product-view-first-001';
const FIRST_REPLY = 'Product view first reply marker.';
const THREAD_TITLE = 'Product View Thread Title';
const RENAMED_TITLE = 'Renamed Product View Chat';
const ENTER_PROMPT = 'new chat sent with enter';

// Every test that needs an existing conversation creates its own (through the UI, or through the API when
// the test is not about creating one), so tests never depend on each other or on the order they run in.
// Replies carry a marker that does not repeat the prompt, so text locators cannot match the wrong post.
interface Chat {prompt: string; reply: string; title?: string}
const SIDEBAR_CHAT: Chat = {prompt: 'product-view-sidebar-008', reply: 'Sidebar chat reply marker.', title: 'Product View Sidebar Chat'};
const HEADER_CHAT: Chat = {prompt: 'product-view-header-009', reply: 'Header chat reply marker.', title: 'Product View Header Chat'};
const VIEW_CHAT: Chat = {prompt: 'product-view-thread-010', reply: 'Thread view reply marker.'};
const DM_CHAT: Chat = {prompt: 'product-view-dm-011', reply: 'Channels DM reply marker.'};
const ENTER_REPLY_CHAT: Chat = {prompt: 'product-view-reply-enter-012', reply: 'Reply composer enter marker.'};
const COMPLETE_CHAT: Chat = {prompt: 'product-view-autocomplete-013', reply: 'Autocomplete chat reply marker.'};
const STOP_CHAT: Chat = {prompt: 'product-view-stop-seed-014', reply: 'Stop chat reply marker.'};
const ATTACH_PROMPT = 'product-view-attach-004';
const ATTACH_FILE = 'product-view-attachment.txt';
const STOP_PROMPT = 'product-view-stop-005';
const STOP_TAIL = 'and that is where the slow reply finally ends.';
const BUSY_PROMPT = 'product-view-busy-007';
const BUSY_TAIL = 'and that is the end of the busy reply.';
const JUMP_PROMPT = 'product-view-jump-006';
const JUMP_PARAGRAPHS = Array.from({length: 30}, (_, i) => `Paragraph ${i + 1} of the long reply, which is only here to make the chat scroll while it streams in.`);
const JUMP_LAST = JUMP_PARAGRAPHS[JUMP_PARAGRAPHS.length - 1];

const replyFixtures = (prompts: string[]) => prompts.flatMap((prompt) => buildTextResponse({userMessage: prompt, content: `Reply to ${prompt}.`}).fixtures);

// The title request embeds the prompt (after a newline), so its fixture must come before the one for the prompt itself.
const chatFixtures = (...chats: Chat[]) => chats.flatMap(({prompt, reply, title}) => [
    ...(title ? [{match: {userMessage: `${TITLE_GENERATION_PROMPT_PREFIX}\n${prompt}`}, response: {content: title}}] : []),
    ...buildTextResponse({userMessage: prompt, content: reply}).fixtures,
]);
const baseFixtures = () => [
    ...chatFixtures({prompt: FIRST_PROMPT, reply: FIRST_REPLY, title: THREAD_TITLE}, SIDEBAR_CHAT, HEADER_CHAT, VIEW_CHAT, DM_CHAT, ENTER_REPLY_CHAT, COMPLETE_CHAT, STOP_CHAT),
    ...replyFixtures([ENTER_PROMPT, ATTACH_PROMPT]),

    // Streams slowly enough to be stopped mid-way.
    {
        match: {userMessage: STOP_PROMPT},
        response: {content: `A slow reply that streams a couple of characters at a time, long enough to stop part way through, ${STOP_TAIL}`},
        chunkSize: 2,
        streamingProfile: {ttft: 300, tps: 6, jitter: 0},
    },

    // Slow enough to leave the chat while it streams.
    {
        match: {userMessage: BUSY_PROMPT},
        response: {content: `A reply that keeps the agent busy for a few seconds while the user looks at another chat, ${BUSY_TAIL}`},
        chunkSize: 2,
        streamingProfile: {ttft: 300, tps: 8, jitter: 0},
    },

    // Long enough to need scrolling, slow enough to scroll up while it streams.
    {
        match: {userMessage: JUMP_PROMPT},
        response: {content: JUMP_PARAGRAPHS.join('\n\n')},
        chunkSize: 20,
        streamingProfile: {ttft: 300, tps: 20, jitter: 0},
    },
];

// Rename responses seen during the run, written next to the screenshots.
const conversationResponses: Array<{test: string; method: string; url: string; status: number; body: string}> = [];

function recordConversationResponses(page: Page, testName: string): void {
    page.on('response', async (response) => {
        const url = response.url();
        if (!(/\/conversations\/[^/]+\/title$/).test(url)) {
            return;
        }
        const body = await response.text().catch((e) => `<unreadable: ${e}>`);
        conversationResponses.push({test: testName, method: response.request().method(), url: new URL(url).pathname, status: response.status(), body});
    });
}

const GREETINGS = [
    'How can I help today?',
    'Let\'s get some work done',
    'What should we tackle?',
    'Ready when you are',
    'Where should we start?',
    'Got something on your mind?',
];

const SCREENSHOT_DIR = process.env.AGENTS_PRODUCT_VIEW_SHOT_DIR || path.join(__dirname, '..', '..', 'screenshots', 'agents-product-view');

let mattermost: MattermostContainer;
let aimock: AIMockContainer;
let botUserId = '';
let dmChannelId = '';

async function shot(page: Page, name: string): Promise<void> {
    fs.mkdirSync(SCREENSHOT_DIR, {recursive: true});
    await page.evaluate(() => document.fonts.ready);
    await page.screenshot({path: path.join(SCREENSHOT_DIR, `${name}.png`), fullPage: true});
}

async function login(page: Page): Promise<void> {
    await new MattermostPage(page).login(mattermost.url(), username, password);
}

const sidebar = (page: Page) => page.getByRole('complementary', {name: 'Agents'});
const newChatComposer = (page: Page) => page.getByTestId('agent-chat-new-composer');

// The plugin's pill reply composer under the thread (the host reply box is hidden).
const replyComposer = (page: Page) => page.getByTestId('agent-chat-thread').getByRole('textbox', {name: `Reply to ${botDisplayName}`});

async function openAgentsFromSwitcher(page: Page): Promise<void> {
    // The host labels this button differently across versions ("Open product menu", "Product switch menu").
    await page.getByRole('button', {name: /product.*menu/i}).click();
    await page.getByRole('menuitem', {name: 'Agents'}).click();
    await expect(page).toHaveURL(/\/plug\/mattermost-ai\/agents/);
}

const threadUrl = (rootId: string) => `${mattermost.url()}/plug/mattermost-ai/agents/chat/${botUserId}/${rootId}`;

// Sends the chat's prompt from the new-chat composer and returns the new thread's root post id.
async function startChatViaUi(page: Page, chat: Chat): Promise<string> {
    await login(page);
    await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents/chat/${botUserId}`, {waitUntil: 'domcontentloaded'});
    const textbox = newChatComposer(page).getByRole('textbox');
    await expect(textbox).toBeEnabled({timeout: 30000});
    await textbox.fill(chat.prompt);
    await textbox.press('Enter');
    await expect(page).toHaveURL(new RegExp(`/agents/chat/${botUserId}/[a-z0-9]{26}$`), {timeout: 30000});
    const rootId = new URL(page.url()).pathname.split('/').pop()!;
    await expect(page.getByTestId('agent-chat-thread').getByText(chat.reply)).toBeVisible({timeout: 60000});
    return rootId;
}

// Creates a finished conversation through the API (a DM to the agent) and returns its root post id.
async function seedThread(chat: Chat): Promise<string> {
    const client = await mattermost.getClient(username, password);
    const root = await client.createPost({channel_id: dmChannelId, message: chat.prompt} as any);
    await expect.poll(async () => {
        const thread = await client.getPostThread(root.id);
        return Object.values(thread.posts).some((p) => p.user_id === botUserId && p.message.includes(chat.reply));
    }, {timeout: 60000}).toBe(true);
    return root.id;
}

const MATTERMOST_START_MS = 240000;
const AIMOCK_START_MS = 120000;

const test = containerTest(async (own) => {
    const mattermost = own(await withDeadline('Starting Mattermost', MATTERMOST_START_MS, () => RunAIMockContainer()));
    const aimock = own(await withDeadline('Starting aimock', AIMOCK_START_MS, () => RunAIMockSidecar(mattermost.network, {fixtures: baseFixtures()})));
    return {mattermost, aimock};
});

test.describe('Agents product view', () => {
    test.beforeAll(async ({containers}) => {
        ({mattermost, aimock} = containers);
        const client = await mattermost.getClient(username, password);
        const me = await client.getMe();
        const bot = await client.getUserByUsername(AIMOCK_BOT_NAME);
        botUserId = bot.id;
        dmChannelId = (await client.createDirectChannel([me.id, botUserId])).id;
    });

    test.afterAll(async () => {
        fs.mkdirSync(SCREENSHOT_DIR, {recursive: true});
        fs.writeFileSync(path.join(SCREENSHOT_DIR, 'conversation_responses.json'), JSON.stringify(conversationResponses, null, 2));
    });

    test('product switcher opens a new chat with the default agent, not the agents list', async ({page}) => {
        await login(page);
        await openAgentsFromSwitcher(page);

        const chat = page.getByRole('region', {name: `Chat with ${botDisplayName}`});
        await expect(chat).toBeVisible({timeout: 30000});
        await expect(chat.getByRole('heading', {name: botDisplayName, level: 1})).toBeVisible();

        // Exactly one greeting out of the six, never the agents list.
        const greeting = chat.getByRole('heading', {level: 2, name: new RegExp(`^(${GREETINGS.map((g) => g.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|')})$`)});
        await expect(greeting).toHaveCount(1);
        await expect(page.getByPlaceholder('Search agents...')).toHaveCount(0);

        const composer = newChatComposer(page);
        const textbox = composer.getByRole('textbox');
        await expect(textbox).toBeEnabled();
        await expect(textbox).toBeFocused();

        // Horizontally centered in the chat pane; vertically in the middle band
        // (it is nudged slightly above true center by design).
        const paneBox = (await chat.boundingBox())!;
        const composerBox = (await composer.boundingBox())!;
        const horizontalOffset = Math.abs((composerBox.x + (composerBox.width / 2)) - (paneBox.x + (paneBox.width / 2)));
        expect(horizontalOffset).toBeLessThan(4);
        const verticalRatio = ((composerBox.y + (composerBox.height / 2)) - paneBox.y) / paneBox.height;
        expect(verticalRatio).toBeGreaterThan(0.3);
        expect(verticalRatio).toBeLessThan(0.65);

        await shot(page, '01_landing_new_chat');
    });

    test('cold-load of the product URL renders an enabled composer without a spinner', async ({page}) => {
        await login(page);

        // Fresh document load of the product route (no client-side navigation).
        await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents`, {waitUntil: 'domcontentloaded'});

        const composer = newChatComposer(page);
        await expect(composer.getByRole('textbox')).toBeEnabled({timeout: 30000});
        await expect(composer.getByRole('textbox')).toHaveAttribute('placeholder', `Message ${botDisplayName}`);
        await expect(page.getByRole('region', {name: `Chat with ${botDisplayName}`}).getByRole('progressbar')).toHaveCount(0);
        await expect(page.getByText('Setting up chat…')).toHaveCount(0);

        const teamState = await page.evaluate(() => {
            const store = (window as any).store;
            const teams = store?.getState().entities.teams;
            return {currentTeamId: teams?.currentTeamId ?? null, myTeams: Object.keys(teams?.myMembers ?? {}).length};
        });
        test.info().annotations.push({type: 'cold-load currentTeamId', description: JSON.stringify(teamState)});
    });

    test('sending a first message opens the thread, lists it in the sidebar and leaves it read', async ({page}) => {
        await login(page);
        await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents`, {waitUntil: 'domcontentloaded'});

        const textbox = newChatComposer(page).getByRole('textbox');
        await expect(textbox).toBeEnabled({timeout: 30000});
        await textbox.fill(FIRST_PROMPT);
        // The Send button is used here; Enter is covered by its own tests below.
        await newChatComposer(page).getByRole('button', {name: 'Send message'}).click();

        await expect(page).toHaveURL(new RegExp(`/plug/mattermost-ai/agents/chat/${botUserId}/[a-z0-9]{26}$`), {timeout: 30000});
        const rootId = new URL(page.url()).pathname.split('/').pop()!;

        const thread = page.getByTestId('agent-chat-thread');
        await expect(thread.getByText(FIRST_PROMPT, {exact: true})).toBeVisible({timeout: 30000});
        await expect(thread.getByText(FIRST_REPLY)).toBeVisible({timeout: 60000});

        // The pill reply composer is pinned below the messages (not centered any more).
        const reply = replyComposer(page);
        await expect(reply).toBeVisible();
        const paneBox = (await page.getByRole('region', {name: `Chat with ${botDisplayName}`}).boundingBox())!;
        const replyBox = (await reply.boundingBox())!;
        expect(replyBox.y + replyBox.height).toBeGreaterThan(paneBox.y + (paneBox.height * 0.8));

        // The thread is listed under the agent (title may still be the placeholder).
        const threadList = sidebar(page).getByRole('list', {name: `${botDisplayName} chats`});
        await expect(threadList.getByRole('button', {name: new RegExp(`^(${THREAD_TITLE}|New chat)$`)})).toHaveCount(1, {timeout: 30000});
        await expect(threadList.getByRole('button', {name: THREAD_TITLE, exact: true})).toBeVisible({timeout: 60000});
        await expect(threadList.getByRole('button', {name: THREAD_TITLE, exact: true})).toHaveAttribute('aria-current', /^(page|true)$/);

        // Not left unread: no unread replies/mentions on the thread for the user.
        const client = await mattermost.getClient(username, password);
        const me = await client.getMe();
        const team = (await client.getMyTeams()).find((t) => t.name === teamName)!;
        await expect.poll(async () => {
            const response = await fetch(`${mattermost.url()}/api/v4/users/${me.id}/teams/${team.id}/threads/${rootId}`, {
                headers: {Authorization: `Bearer ${client.getToken()}`},
            });
            if (!response.ok) {
                return `status ${response.status}`;
            }
            const body = await response.json();
            return {replies: body.unread_replies, mentions: body.unread_mentions};
        }, {timeout: 30000}).toEqual({replies: 0, mentions: 0});

        await shot(page, '02_thread');
    });

    test('sidebar: All agents, new chat, thread selection, collapse and keyboard use', async ({page}) => {
        const rootId = await startChatViaUi(page, SIDEBAR_CHAT);
        await page.goto(threadUrl(rootId), {waitUntil: 'domcontentloaded'});

        const nav = sidebar(page);
        const threadRow = nav.getByRole('button', {name: SIDEBAR_CHAT.title, exact: true});
        await expect(threadRow).toBeVisible({timeout: 30000});
        await expect(page.getByTestId('agent-chat-thread').getByText(SIDEBAR_CHAT.reply)).toBeVisible({timeout: 30000});

        // All agents shows the existing agents list at /all.
        await nav.getByRole('button', {name: 'All agents'}).click();
        await expect(page).toHaveURL(/\/plug\/mattermost-ai\/agents\/all$/);
        await expect(page.getByPlaceholder('Search agents...')).toBeVisible();
        await shot(page, '03_all_agents');

        // [+] starts a fresh chat with that agent.
        await nav.getByRole('button', {name: `New chat with ${botDisplayName}`}).click();
        await expect(page).toHaveURL(new RegExp(`/agents/chat/${botUserId}$`));
        await expect(newChatComposer(page).getByRole('textbox')).toBeFocused();

        // Clicking a thread opens it.
        await threadRow.click();
        await expect(page).toHaveURL(new RegExp(`/agents/chat/${botUserId}/${rootId}$`));
        await expect(page.getByTestId('agent-chat-thread').getByText(SIDEBAR_CHAT.reply)).toBeVisible();

        // Chevron collapses and expands the agent's thread list.
        const collapse = nav.getByRole('button', {name: `Collapse ${botDisplayName} chats`});
        await expect(collapse).toHaveAttribute('aria-expanded', 'true');
        await collapse.click();
        await expect(threadRow).toHaveCount(0);
        const expand = nav.getByRole('button', {name: `Expand ${botDisplayName} chats`});
        await expect(expand).toHaveAttribute('aria-expanded', 'false');
        await expand.click();
        await expect(threadRow).toBeVisible();

        // Keyboard: rows are real buttons reachable and activatable without a mouse.
        await nav.getByRole('button', {name: 'All agents'}).focus();
        await page.keyboard.press('Enter');
        await expect(page).toHaveURL(/\/agents\/all$/);
        await threadRow.focus();
        await page.keyboard.press('Enter');
        await expect(page).toHaveURL(new RegExp(`/agents/chat/${botUserId}/${rootId}$`));
        await nav.getByRole('button', {name: `New chat with ${botDisplayName}`}).focus();
        await page.keyboard.press('Space');
        await expect(page).toHaveURL(new RegExp(`/agents/chat/${botUserId}$`));
    });

    test('thread shows your message and the reply, with its time in the controls and no host reply box', async ({page}) => {
        const rootId = await seedThread(VIEW_CHAT);
        await login(page);
        await page.goto(threadUrl(rootId), {waitUntil: 'domcontentloaded'});
        const thread = page.getByTestId('agent-chat-thread');
        await expect(thread.getByText(VIEW_CHAT.reply)).toBeVisible({timeout: 30000});

        // This thread is the test's own, so each filter matches exactly one post.
        const ownPost = thread.locator('.post').filter({hasText: VIEW_CHAT.prompt});
        const replyPost = thread.locator('.post').filter({hasText: VIEW_CHAT.reply});
        await expect(ownPost).toBeVisible();

        // The reply's time is in its controls bar, not in a host header.
        await expect(replyPost.getByTestId('controls-bar-time')).toBeVisible();

        // Your message sits to the right of the agent's reply.
        const ownBox = (await ownPost.locator('.post__content > .post__img + div').boundingBox())!;
        const replyBox = (await replyPost.locator('.post__content > .post__img + div').boundingBox())!;
        expect(ownBox.x).toBeGreaterThan(replyBox.x);

        // The host reply box is replaced by the plugin's composer, not shown alongside.
        await expect(thread.getByTestId('comment-create')).toBeHidden();
        await expect(replyComposer(page)).toBeVisible();

        // Hovering the reply tints its bubble; a control clicked with the mouse keeps focus, but the bubble
        // must not stay tinted once the pointer has left (focus from a click is not keyboard focus).
        const bubble = replyPost.locator('.post__content > .post__img + div');
        const tint = () => bubble.evaluate((el) => getComputedStyle(el).backgroundColor);
        await page.mouse.move(0, 0);
        const idle = await tint();
        await bubble.hover();
        await expect.poll(tint).not.toBe(idle);
        await replyPost.getByRole('button', {name: 'Copy'}).click();
        await page.mouse.move(0, 0);
        await expect.poll(tint).toBe(idle);
    });

    test('header: chat title opens the agent menu; the actions menu switches, renames and creates chats', async ({page}) => {
        recordConversationResponses(page, 'header rename');
        const rootId = await startChatViaUi(page, HEADER_CHAT);
        await page.goto(threadUrl(rootId), {waitUntil: 'domcontentloaded'});
        const chat = page.getByRole('region', {name: `Chat with ${botDisplayName}`});
        await expect(chat.getByTestId('agent-chat-thread').getByText(HEADER_CHAT.reply)).toBeVisible({timeout: 30000});

        // The header is a <header> landmark named after the agent; in an open chat the h1 is the chat title.
        const header = chat.locator(`header[aria-label="${botDisplayName}"]`);
        await expect(header).toBeVisible();
        await expect(header.getByRole('heading', {level: 1, name: HEADER_CHAT.title, exact: true})).toBeVisible({timeout: 15000});

        // The name button carries the full title and the agent name in its accessible name.
        const trigger = page.getByTestId('agent-chat-menu-button');
        await expect(trigger).toHaveAttribute('aria-label', `${HEADER_CHAT.title}, ${botDisplayName} options`);
        await trigger.click();
        await expect(trigger).toHaveAttribute('aria-expanded', 'true');
        const nameMenu = page.getByRole('menu', {name: `${HEADER_CHAT.title}, ${botDisplayName} options`});
        // The menu has a Recent chats submenu first, then the chat actions. A regular user cannot edit the agent, so no Agent settings.
        await expect(nameMenu.getByRole('menuitem')).toHaveText(['Recent chats', 'New chat', 'Rename chat', 'View all agents']);
        await page.keyboard.press('Escape');
        await expect(nameMenu).toHaveCount(0);

        // The Recent chats submenu lists this agent's chats, the open one marked.
        await trigger.click();
        const menu = page.getByRole('menu', {name: `${HEADER_CHAT.title}, ${botDisplayName} options`});
        await menu.getByRole('menuitem', {name: 'Recent chats'}).click();
        const recent = page.getByRole('menu', {name: 'Recent chats'});
        await expect(recent.getByRole('menuitem', {name: HEADER_CHAT.title, exact: true})).toHaveAttribute('aria-current', 'true');
        await expect(menu.getByRole('menuitem', {name: 'Rename chat'})).toBeEnabled();

        // Rename updates the sidebar title.
        await menu.getByRole('menuitem', {name: 'Rename chat'}).click();
        const name = page.getByLabel('Chat name');
        await expect(name).toHaveValue(HEADER_CHAT.title, {timeout: 10000});
        await name.fill(RENAMED_TITLE);
        const renameResponse = page.waitForResponse((r) => r.url().endsWith('/title') && r.request().method() === 'PUT');
        await page.getByRole('button', {name: 'Save'}).click();
        expect((await renameResponse).status()).toBe(200);
        await expect(name).toHaveCount(0);
        await expect(sidebar(page).getByRole('button', {name: RENAMED_TITLE, exact: true})).toBeVisible({timeout: 15000});
        await expect(header.getByRole('heading', {level: 1, name: RENAMED_TITLE, exact: true})).toBeVisible();

        // New chat from the header's new-chat button.
        await page.getByTestId('agent-chat-new-chat-button').click();
        await expect(page).toHaveURL(new RegExp(`/agents/chat/${botUserId}$`));

        // The new-chat state shows the agent name and has no chat to rename.
        await expect(header.getByRole('heading', {level: 1, name: botDisplayName})).toBeVisible();
        await expect(page.getByTestId('agent-chat-new-chat-button')).toBeDisabled();
        await trigger.click();
        const newMenu = page.getByRole('menu', {name: `${botDisplayName} options`});
        await expect(newMenu.getByRole('menuitem', {name: 'View all agents'})).toBeVisible();
        await expect(newMenu.getByRole('menuitem', {name: 'Rename chat'})).toHaveCount(0);
        await page.keyboard.press('Escape');
    });

    test('Agent settings is offered to an admin and opens the agent editor', async ({page}) => {
        await new MattermostPage(page).login(mattermost.url(), 'admin', 'admin');
        await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents/chat/${botUserId}`, {waitUntil: 'domcontentloaded'});
        const chat = page.getByRole('region', {name: `Chat with ${botDisplayName}`});
        await expect(newChatComposer(page).getByRole('textbox')).toBeEnabled({timeout: 30000});

        const trigger = page.getByTestId('agent-chat-menu-button');
        const menu = page.getByRole('menu', {name: `${botDisplayName} options`});
        await expect.poll(async () => {
            await trigger.click();

            // count() does not auto-wait: let the menu render before counting.
            await expect(menu.getByRole('menuitem', {name: 'View all agents'})).toBeVisible();
            const count = await menu.getByRole('menuitem', {name: 'Agent settings'}).count();
            await page.keyboard.press('Escape');
            await expect(menu).toHaveCount(0);
            return count;
        }, {timeout: 15000}).toBe(1);

        await trigger.click();
        await menu.getByRole('menuitem', {name: 'Agent settings'}).click();

        // The editor opens as a modal over the chat, with its tabs.
        await expect(page.getByRole('tab', {name: 'Configuration'})).toBeVisible({timeout: 15000});
    });

    test('Custom Prompts page: create, edit, pin and delete a prompt', async ({page}) => {
        await login(page);
        await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents/chat/${botUserId}`, {waitUntil: 'domcontentloaded'});
        await sidebar(page).getByRole('button', {name: 'Custom Prompts'}).click();
        await expect(page).toHaveURL(/\/agents\/prompts$/);
        await expect(page.getByRole('heading', {name: 'Custom Prompts', level: 1}).and(page.locator(':not(#global-header *)'))).toBeVisible({timeout: 30000});

        await page.getByRole('button', {name: 'Create new'}).click();
        await page.getByPlaceholder('Enter a title for your prompt').fill('Product Page Prompt');
        await page.getByPlaceholder('Enter a brief description').fill('Created on the product page');
        await page.getByPlaceholder('Enter the system prompt template').fill('Summarize {{.Username}} notes');
        await page.getByRole('button', {name: 'Save', exact: true}).click();

        const open = page.getByRole('button', {name: 'Open prompt Product Page Prompt'});
        await expect(open).toBeVisible({timeout: 10000});
        await shot(page, '04_custom_prompts');

        // Edit.
        await open.click();
        const title = page.getByPlaceholder('Enter a title for your prompt');
        await expect(title).toHaveValue('Product Page Prompt');
        await title.fill('Product Page Prompt Edited');
        await page.getByRole('button', {name: 'Save', exact: true}).click();
        await expect(page.getByRole('button', {name: 'Open prompt Product Page Prompt Edited'})).toBeVisible({timeout: 10000});

        // Pin (persisted server side).
        await page.getByRole('button', {name: 'Pin prompt'}).click();
        await expect(page.getByRole('button', {name: 'Unpin prompt'})).toHaveAttribute('aria-pressed', 'true');
        const client = await mattermost.getClient(username, password);
        const prompts = await (await fetch(`${mattermost.url()}/plugins/mattermost-ai/custom-prompts`, {
            headers: {Authorization: `Bearer ${client.getToken()}`},
        })).json();
        const created = prompts.find((p: {name: string}) => p.name === 'Product Page Prompt Edited');
        expect(created).toBeTruthy();
        await expect.poll(async () => {
            const pins = await (await fetch(`${mattermost.url()}/plugins/mattermost-ai/custom-prompts/pins`, {
                headers: {Authorization: `Bearer ${client.getToken()}`},
            })).json();
            return pins;
        }).toContain(created.id);

        // Delete with confirmation: cancel keeps it, confirm removes it.
        await page.getByRole('button', {name: 'Open prompt Product Page Prompt Edited'}).click();
        await page.getByRole('button', {name: 'Delete', exact: true}).click();
        const dialog = page.getByRole('dialog', {name: 'Delete prompt'});
        await expect(dialog).toBeVisible();
        await dialog.getByRole('button', {name: 'Cancel'}).click();
        await expect(dialog).toBeHidden();
        await page.getByRole('button', {name: 'Delete', exact: true}).click();
        await page.getByRole('dialog', {name: 'Delete prompt'}).getByRole('button', {name: 'Delete'}).click();
        await expect(page.getByRole('button', {name: 'Open prompt Product Page Prompt Edited'})).toHaveCount(0, {timeout: 10000});
    });

    test('the same thread is visible in the Channels DM and the stock DM view still works', async ({page}) => {
        const rootId = await seedThread(DM_CHAT);
        await login(page);
        await page.goto(`${mattermost.url()}/${teamName}/messages/@${AIMOCK_BOT_NAME}`, {waitUntil: 'domcontentloaded'});

        const channelView = page.getByTestId('channel_view');
        const messages = channelView.getByTestId('post-message-text');
        await expect(messages.getByText(DM_CHAT.prompt, {exact: true})).toBeVisible({timeout: 30000});

        // With collapsed reply threads the DM shows the root; the bot reply is in its thread. The DM
        // holds other tests' threads too, so use this thread's own root post.
        const rootPost = channelView.locator(`#post_${rootId}`);
        await rootPost.getByRole('button', {name: /1 reply/i}).click();
        await expect(page.locator('#rhsContainer').getByText(DM_CHAT.reply)).toBeVisible({timeout: 15000});
        await expect(page.locator('#rhsContainer').getByText(DM_CHAT.prompt, {exact: true})).toBeVisible();

        // Stock composer still posts into the DM.
        const textbox = page.getByTestId('post_textbox');
        await expect(textbox).toBeEnabled();
        await textbox.fill('stock dm composer still works');
        await textbox.press('Enter');
        await expect(messages.getByText('stock dm composer still works')).toBeVisible();
    });

    test('a draft typed in the product new-chat composer does not leak into the Channels DM composer', async ({page}) => {
        await login(page);
        await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents/chat/${botUserId}`, {waitUntil: 'domcontentloaded'});

        const textbox = newChatComposer(page).getByRole('textbox');
        await expect(textbox).toBeEnabled({timeout: 30000});
        await textbox.fill('product draft that must stay in the product');
        await expect(textbox).toHaveValue('product draft that must stay in the product');

        // Client-side navigation to the Channels DM (no reload, so the draft is still in the store).
        await page.evaluate(([team, bot]) => {
            (window as any).WebappUtils.browserHistory.push(`/${team}/messages/@${bot}`);
        }, [teamName, AIMOCK_BOT_NAME]);

        const dmTextbox = page.getByTestId('channel_view').getByTestId('post_textbox');
        await expect(dmTextbox).toBeVisible({timeout: 30000});
        await expect(dmTextbox).toHaveValue('');
    });

    test('Enter sends from the thread reply composer', async ({page}) => {
        const rootId = await seedThread(ENTER_REPLY_CHAT);
        await login(page);
        await page.goto(threadUrl(rootId), {waitUntil: 'domcontentloaded'});

        const reply = replyComposer(page);
        await expect(reply).toBeEnabled({timeout: 30000});
        await reply.fill('reply sent with enter');
        await reply.press('Enter');
        await expect(page.getByTestId('agent-chat-thread').getByTestId('post-message-text').getByText('reply sent with enter')).toBeVisible({timeout: 30000});

        // Cleared and focused, ready for the next reply.
        await expect(reply).toHaveValue('');
        await expect(reply).toBeFocused();
    });

    test('the reply composer autocompletes @mentions and :emoji:, and Enter sends once the list is closed', async ({page}) => {
        const rootId = await seedThread(COMPLETE_CHAT);
        await login(page);
        await page.goto(threadUrl(rootId), {waitUntil: 'domcontentloaded'});

        const thread = page.getByTestId('agent-chat-thread');
        const reply = replyComposer(page);
        const suggestions = thread.locator('#suggestionList');
        await expect(reply).toBeEnabled({timeout: 30000});

        // "@" opens the mention list; Enter picks the user instead of sending.
        await reply.pressSequentially(`mention @${AIMOCK_BOT_NAME.slice(0, 3)}`);
        await expect(suggestions.getByText(`@${AIMOCK_BOT_NAME}`).first()).toBeVisible({timeout: 15000});
        await reply.press('Enter');
        await expect(reply).toHaveValue(`mention @${AIMOCK_BOT_NAME} `);
        await expect(suggestions).toHaveCount(0);

        // ":smi" offers emoji the same way.
        await reply.pressSequentially(':smi');
        await expect(suggestions).toBeVisible({timeout: 15000});
        await reply.press('Enter');
        // The host inserts the emoji itself (or its :name: where it can't).
        await expect(reply).toHaveValue(new RegExp(`^mention @${AIMOCK_BOT_NAME} (?!:smi )\\S+ $`, 'u'));
        await expect(suggestions).toHaveCount(0);

        // With the list closed, Enter sends.
        await reply.press('Enter');
        await expect(thread.getByTestId('post-message-text').filter({hasText: `mention @${AIMOCK_BOT_NAME}`})).toBeVisible({timeout: 30000});
        await expect(reply).toHaveValue('');
    });

    test('the composer grows with multi-line text up to a cap, then shrinks back', async ({page}) => {
        await login(page);
        await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents/chat/${botUserId}`, {waitUntil: 'domcontentloaded'});
        const textbox = newChatComposer(page).getByRole('textbox');
        await expect(textbox).toBeEnabled({timeout: 30000});

        const height = () => textbox.evaluate((el) => el.getBoundingClientRect().height);
        const single = await height();

        await textbox.fill('one\ntwo\nthree');
        await expect.poll(height).toBeGreaterThan(single);

        // Expanded, "+" and send sit on the row below the text.
        const buttonsBelow = () => textbox.evaluate((el) => {
            const text = el.getBoundingClientRect();
            const shell = el.closest('[data-testid="agent-chat-composer-pill"]') as HTMLElement;
            return ['Add to message', 'Send message'].every((name) => shell.querySelector(`button[aria-label="${name}"]`)!.getBoundingClientRect().top >= text.bottom);
        });
        await expect.poll(buttonsBelow).toBe(true);

        // One long line expands as soon as it wraps.
        await textbox.fill('word '.repeat(60).trim());
        await expect.poll(height).toBeGreaterThan(single);

        // Capped: twelve lines and twenty lines are the same height, and the rest scrolls.
        await textbox.fill(Array.from({length: 12}, (_v, i) => `line ${i}`).join('\n'));
        await expect.poll(height).toBeGreaterThan(single);
        const capped = await height();
        await textbox.fill(Array.from({length: 20}, (_v, i) => `line ${i}`).join('\n'));
        await expect.poll(height).toBe(capped);

        await textbox.fill('');
        await expect.poll(height).toBe(single);
    });

    test('while the agent replies, the composer\'s send button is Stop and stops it', async ({page}) => {
        const rootId = await seedThread(STOP_CHAT);
        await login(page);
        await page.goto(threadUrl(rootId), {waitUntil: 'domcontentloaded'});

        const thread = page.getByTestId('agent-chat-thread');
        const composer = thread.getByTestId('agent-chat-reply-composer');
        const reply = replyComposer(page);
        await expect(reply).toBeEnabled({timeout: 30000});
        await reply.fill(STOP_PROMPT);
        await reply.press('Enter');

        const stop = composer.getByRole('button', {name: 'Stop generating'});
        await expect(stop).toBeVisible({timeout: 30000});

        // The post leaves Stop to the composer.
        await expect(thread.getByTestId('llm-bot-post').getByTestId('stop-generating-button')).toHaveCount(0);

        await stop.click();
        await expect(composer.getByRole('button', {name: 'Send message'})).toBeVisible({timeout: 15000});
        const stoppedPost = thread.getByTestId('llm-bot-post').last();
        await expect(stoppedPost.getByTestId('llm-bot-generation-stopped')).toBeVisible({timeout: 15000});
        await expect(stoppedPost).not.toContainText(STOP_TAIL);
    });

    test('a file attached through the "+" menu uploads and goes out with the first message', async ({page}) => {
        await login(page);
        await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents/chat/${botUserId}`, {waitUntil: 'domcontentloaded'});

        const composer = newChatComposer(page);
        const textbox = composer.getByRole('textbox');
        await expect(textbox).toBeEnabled({timeout: 30000});

        await composer.getByRole('button', {name: 'Add to message'}).click();
        const chooser = page.waitForEvent('filechooser');
        await page.getByRole('menuitem', {name: 'Upload files'}).click();
        await (await chooser).setFiles({name: ATTACH_FILE, mimeType: 'text/plain', buffer: Buffer.from('attached from the product view')});

        // Uploaded (removable, no longer cancellable) before the message goes out.
        const card = composer.getByRole('list', {name: 'Attachments'}).getByRole('listitem');
        await expect(card).toHaveCount(1);
        await expect(card).toHaveAttribute('data-status', 'done', {timeout: 30000});
        await expect(card.getByRole('button', {name: `Remove ${ATTACH_FILE}`})).toBeVisible();

        await textbox.fill(ATTACH_PROMPT);
        await textbox.press('Enter');
        await expect(page).toHaveURL(new RegExp(`/agents/chat/${botUserId}/[a-z0-9]{26}$`), {timeout: 15000});

        // The user's root post carries the file.
        const thread = page.getByTestId('agent-chat-thread');
        const newRootId = new URL(page.url()).pathname.split('/').pop()!;
        const userPost = thread.locator(`#post_${newRootId}`);
        await expect(userPost.getByTestId('post-message-text').getByText(ATTACH_PROMPT, {exact: true})).toBeVisible({timeout: 30000});
        await expect(userPost.getByText(ATTACH_FILE)).toBeVisible({timeout: 30000});

        const client = await mattermost.getClient(username, password);
        const root = await client.getPost(newRootId);
        expect(root.file_ids).toHaveLength(1);
    });

    test('Enter sends from the new-chat composer', async ({page}) => {
        await login(page);
        await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents/chat/${botUserId}`, {waitUntil: 'domcontentloaded'});

        const textbox = newChatComposer(page).getByRole('textbox');
        await expect(textbox).toBeEnabled({timeout: 30000});
        await textbox.fill(ENTER_PROMPT);

        // Shift+Enter adds a newline instead of sending.
        await textbox.press('Shift+Enter');
        await expect(textbox).toHaveValue(`${ENTER_PROMPT}\n`);
        await expect(page).toHaveURL(new RegExp(`/agents/chat/${botUserId}$`));
        await textbox.press('Enter');

        await expect(page).toHaveURL(new RegExp(`/agents/chat/${botUserId}/[a-z0-9]{26}$`), {timeout: 15000});
        await expect(page.getByTestId('agent-chat-thread').getByTestId('post-message-text').getByText(ENTER_PROMPT, {exact: true})).toBeVisible({timeout: 30000});

        // The draft is cleared, so going back to a new chat starts empty.
        await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents/chat/${botUserId}`, {waitUntil: 'domcontentloaded'});
        await expect(newChatComposer(page).getByRole('textbox')).toHaveValue('', {timeout: 30000});
    });

    test('scrolled up while the agent answers, a jump button appears above the composer and scrolls back down', async ({page}) => {
        test.setTimeout(120000);
        await login(page);
        await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents/chat/${botUserId}`, {waitUntil: 'domcontentloaded'});
        const textbox = newChatComposer(page).getByRole('textbox');
        await expect(textbox).toBeEnabled({timeout: 30000});
        await textbox.fill(JUMP_PROMPT);
        await textbox.press('Enter');
        await expect(page).toHaveURL(new RegExp(`/agents/chat/${botUserId}/[a-z0-9]{26}$`), {timeout: 15000});

        const thread = page.getByTestId('agent-chat-thread');
        const scroller = thread.locator('#threadViewerScrollContainer');
        const jump = thread.getByTestId('jump-to-latest');
        const distance = () => scroller.evaluate((el) => el.scrollHeight - el.scrollTop - el.clientHeight);

        // Once the reply has outgrown the view, scroll to the top while it is still streaming.
        await expect.poll(() => scroller.evaluate((el) => el.scrollHeight - el.clientHeight), {timeout: 30000}).toBeGreaterThan(300);
        await scroller.evaluate((el) => {
            el.scrollTop = 0;
        });
        await expect(jump).toHaveAttribute('data-visible', 'true');
        await expect(jump.getByRole('button', {name: new RegExp(`${botDisplayName} is responding`)})).toBeVisible();
        await expect(jump).not.toContainText('responding');

        // It floats above the composer, not over it.
        const jumpBox = (await jump.boundingBox())!;
        const composerBox = (await thread.getByTestId('agent-chat-reply-composer').boundingBox())!;
        expect(jumpBox.y + jumpBox.height).toBeLessThanOrEqual(composerBox.y);

        // One click goes to the newest text and the button leaves.
        await jump.getByRole('button').click();
        await expect.poll(distance, {timeout: 10000}).toBeLessThan(120);
        await expect(jump).toHaveAttribute('data-visible', 'false');

        // Let the reply finish so later tests start from an idle agent.
        await expect(thread.getByText(JUMP_LAST)).toBeVisible({timeout: 60000});
    });

    test('a chat whose agent is answering shows the wave in the sidebar until the reply finishes', async ({page}) => {
        test.setTimeout(120000);
        await login(page);
        await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents/chat/${botUserId}`, {waitUntil: 'domcontentloaded'});
        const textbox = newChatComposer(page).getByRole('textbox');
        await expect(textbox).toBeEnabled({timeout: 30000});
        await textbox.fill(BUSY_PROMPT);
        await textbox.press('Enter');
        await expect(page).toHaveURL(new RegExp(`/agents/chat/${botUserId}/[a-z0-9]{26}$`), {timeout: 15000});
        const busyUrl = page.url();

        // The wave shows on the chat's row while its agent is answering, open or not.
        const indicator = sidebar(page).getByTestId('thread-generating');
        await expect(indicator).toHaveCount(1);

        // Leave for a new chat while the reply is still streaming: the busy chat's row still shows it.
        await sidebar(page).getByRole('button', {name: `New chat with ${botDisplayName}`}).click();
        await expect(page).toHaveURL(new RegExp(`/agents/chat/${botUserId}$`));
        await expect(indicator).toHaveCount(1);

        // It goes when the reply has finished.
        await expect(indicator).toHaveCount(0, {timeout: 60000});

        // Coming back shows the finished reply.
        await page.goto(busyUrl, {waitUntil: 'domcontentloaded'});
        await expect(page.getByTestId('agent-chat-thread').getByText(BUSY_TAIL)).toBeVisible({timeout: 30000});
    });

    test.describe('sidebar "+" menu', () => {
        const addButton = (page: Page) => sidebar(page).getByRole('button', {name: 'Add agent or chat'});
        const addMenu = (page: Page) => page.getByRole('menu', {name: 'Add agent or chat'});

        test('"Create new agent" opens the create form; Back and Escape return to the list', async ({page}) => {
            await new MattermostPage(page).login(mattermost.url(), 'admin', 'admin');
            await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents/all`, {waitUntil: 'domcontentloaded'});

            await addButton(page).click();
            await expect(addMenu(page).getByRole('menuitem')).toHaveText(['New chat', 'Create new agent']);
            await page.keyboard.press('Escape');

            for (const leave of ['back', 'escape'] as const) {
                await addButton(page).click();
                await addMenu(page).getByRole('menuitem', {name: 'Create new agent'}).click();
                await expect(page).toHaveURL(/\/plug\/mattermost-ai\/agents\/all\/new$/);
                const back = page.getByRole('button', {name: /Back/});
                await expect(back).toBeVisible({timeout: 15000});

                if (leave === 'back') {
                    await back.click();
                } else {
                    await page.keyboard.press('Escape');
                }
                await expect(page).toHaveURL(/\/plug\/mattermost-ai\/agents\/all$/);
                await expect(page.getByPlaceholder('Search agents...')).toBeVisible();
            }
        });

        test('"Create new agent" is hidden for a user who cannot create agents', async ({page}) => {
            // Regular users can create agents by default; take that away for this test only.
            await mattermost.revokeManageOwnAgentFromSystemUser();
            try {
                await login(page);
                await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents/all`, {waitUntil: 'domcontentloaded'});
                await addButton(page).click();
                const menu = addMenu(page);
                await expect(menu.getByRole('menuitem', {name: 'New chat'})).toBeVisible();
                await expect(menu.getByRole('menuitem', {name: 'Create new agent'})).toHaveCount(0);
            } finally {
                await mattermost.grantSelfServiceAgentPermissions();
            }
        });

        test('"New chat" lists the agents and choosing one opens its new-chat composer', async ({page}) => {
            await login(page);
            await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents/all`, {waitUntil: 'domcontentloaded'});
            await addButton(page).click();
            await addMenu(page).getByRole('menuitem', {name: 'New chat'}).click();
            const agents = page.getByRole('menu', {name: 'Choose an agent'});
            await agents.getByRole('menuitem', {name: botDisplayName}).click();
            await expect(page).toHaveURL(new RegExp(`/plug/mattermost-ai/agents/chat/${botUserId}$`));
            await expect(newChatComposer(page).getByRole('textbox')).toBeEnabled({timeout: 30000});
            await expect(addMenu(page)).toHaveCount(0);
        });

        test('the agents submenu shows loading and empty states', async ({page}) => {
            let release: () => void = () => undefined;
            const gate = new Promise<void>((resolve) => {
                release = resolve;
            });
            let emptyList = false;
            await page.route('**/plugins/mattermost-ai/ai_bots', async (route) => {
                await gate;
                const response = await route.fetch();
                const body = await response.json();
                await route.fulfill({response, json: emptyList ? {...body, bots: []} : body});
            });

            await login(page);
            await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents/all`, {waitUntil: 'domcontentloaded'});
            await addButton(page).click();
            await addMenu(page).getByRole('menuitem', {name: 'New chat'}).click();
            const agents = page.getByRole('menu', {name: 'Choose an agent'});
            await expect(agents.getByText('Loading agents…')).toBeVisible();

            emptyList = true;
            release();
            await expect(agents.getByRole('menuitem', {name: 'No agents available'})).toBeDisabled({timeout: 15000});
        });
    });
});
