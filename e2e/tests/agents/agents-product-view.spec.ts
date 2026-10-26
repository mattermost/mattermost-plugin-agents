// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Agents product view: the product-switcher entry lands in a new chat with the
// workspace default agent, a left sidebar lists agents/threads/Custom Prompts,
// and a thread renders in the host ThreadViewer under the product chrome. The
// same thread must stay visible in the Channels DM with the agent.

import fs from 'fs';
import path from 'path';

import {test, expect, type Page} from '@playwright/test';

import {AIMockContainer, RunAIMockSidecar} from 'helpers/aimock-container';
import {buildTextResponse, buildTitleFixture} from 'helpers/aimock-fixtures';
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
const SIDEBAR_ARCHIVE_PROMPT = 'product-view-sidebar-archive-002';
const EMPTY_TITLE_PROMPT = 'product-view-empty-title-003';

const replyFixtures = (prompts: string[]) => prompts.flatMap((prompt) => buildTextResponse({userMessage: prompt, content: `Reply to ${prompt}.`}).fixtures);
const baseFixtures = () => [
    ...buildTextResponse({userMessage: FIRST_PROMPT, content: FIRST_REPLY, title: THREAD_TITLE}).fixtures,
    ...replyFixtures([ENTER_PROMPT, SIDEBAR_ARCHIVE_PROMPT]),
];

// Rename/archive responses seen during the run, written next to the screenshots.
const conversationResponses: Array<{test: string; method: string; url: string; status: number; body: string}> = [];

function recordConversationResponses(page: Page, testName: string): void {
    page.on('response', async (response) => {
        const url = response.url();
        if (!(/\/conversations\/[^/]+\/(archive|title)$/).test(url)) {
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

// Set by the first-message test and reused by later tests in this serial file.
let rootId = '';

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

async function openAgentsFromSwitcher(page: Page): Promise<void> {
    await page.getByRole('button', {name: 'Open product menu'}).click();
    await page.getByRole('menuitem', {name: 'Agents'}).click();
    await expect(page).toHaveURL(/\/plug\/mattermost-ai\/agents/);
}

test.describe('Agents product view', () => {
    test.describe.configure({mode: 'serial'});

    test.beforeAll(async () => {
        test.setTimeout(240000);
        mattermost = await RunAIMockContainer();
        try {
            aimock = await RunAIMockSidecar(mattermost.network, {
                fixtures: baseFixtures(),
            });
        } catch (error) {
            await mattermost.stop().catch(() => undefined);
            throw error;
        }

        const client = await mattermost.getClient(username, password);
        const me = await client.getMe();
        const bot = await client.getUserByUsername(AIMOCK_BOT_NAME);
        botUserId = bot.id;
        await client.createDirectChannel([me.id, botUserId]);
    });

    test.afterAll(async () => {
        fs.mkdirSync(SCREENSHOT_DIR, {recursive: true});
        fs.writeFileSync(path.join(SCREENSHOT_DIR, 'conversation_responses.json'), JSON.stringify(conversationResponses, null, 2));
        await aimock?.stop();
        await mattermost?.stop();
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
        rootId = new URL(page.url()).pathname.split('/').pop()!;

        const thread = page.getByTestId('agent-chat-thread');
        await expect(thread.getByText(FIRST_PROMPT)).toBeVisible({timeout: 30000});
        await expect(thread.getByText(FIRST_REPLY)).toBeVisible({timeout: 60000});

        // Composer is pinned below the messages (not centered any more).
        const reply = thread.getByRole('textbox');
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
        await login(page);
        await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents/chat/${botUserId}/${rootId}`, {waitUntil: 'domcontentloaded'});

        const nav = sidebar(page);
        const threadRow = nav.getByRole('button', {name: THREAD_TITLE, exact: true});
        await expect(threadRow).toBeVisible({timeout: 30000});
        await expect(page.getByTestId('agent-chat-thread').getByText(FIRST_REPLY)).toBeVisible({timeout: 30000});

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
        await expect(page.getByTestId('agent-chat-thread').getByText(FIRST_REPLY)).toBeVisible();

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

    test('thread renders as chat bubbles in a centered column, own messages differentiated', async ({page}) => {
        await login(page);
        await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents/chat/${botUserId}/${rootId}`, {waitUntil: 'domcontentloaded'});
        const thread = page.getByTestId('agent-chat-thread');
        await expect(thread.getByText(FIRST_REPLY)).toBeVisible({timeout: 30000});

        // Read the bubble (the post body column) of the post that contains each text.
        const bubbleStyle = (text: string) => thread.getByTestId('postContent').filter({hasText: text}).first().evaluate((content) => {
            const bubble = content.querySelector(':scope > .post__img + div') as HTMLElement;
            const style = getComputedStyle(bubble);
            const post = content.closest('.post') as HTMLElement;
            return {background: style.backgroundColor, color: style.color, left: bubble.getBoundingClientRect().left, postWidth: post.getBoundingClientRect().width};
        });
        const buttonBg = await page.evaluate(() => {
            const probe = document.createElement('div');
            probe.style.backgroundColor = 'var(--button-bg)';
            document.body.appendChild(probe);
            const value = getComputedStyle(probe).backgroundColor;
            probe.remove();
            return value;
        });

        const own = await bubbleStyle(FIRST_PROMPT);
        const agent = await bubbleStyle(FIRST_REPLY);
        expect(own.background).toBe(buttonBg);
        expect(agent.background).not.toBe(buttonBg);
        expect(agent.background).not.toBe('rgba(0, 0, 0, 0)');

        // Own bubble sits to the right of the agent's (row-reversed).
        expect(own.left).toBeGreaterThan(agent.left);

        // Messages are limited to the prototype's 800px column.
        expect(own.postWidth).toBeLessThanOrEqual(800);
        expect(agent.postWidth).toBeLessThanOrEqual(800);
        const composerBox = (await thread.getByTestId('comment-create').boundingBox())!;
        expect(composerBox.width).toBeLessThanOrEqual(800);

        // Hover: only the bubble reacts, and the toolbar shows only over the bubble.
        const agentPost = thread.locator('.post').filter({hasText: FIRST_REPLY}).first();
        const agentBubble = agentPost.locator('.post__content > .post__img + div');
        const toolbar = agentPost.locator('[data-testid^="post-menu-"]');
        const state = () => agentPost.evaluate((post) => {
            const bubble = post.querySelector('.post__content > .post__img + div') as HTMLElement;
            const menu = post.querySelector('[data-testid^="post-menu-"]') as HTMLElement | null;
            return {
                row: getComputedStyle(post).backgroundColor,
                bubble: getComputedStyle(bubble).backgroundColor,
                toolbar: menu ? getComputedStyle(menu).opacity : 'absent',
            };
        });
        await page.mouse.move(0, 0);
        const idle = await state();

        // Empty row space: the avatar column left of the agent bubble, below the avatar.
        const rowBox = (await agentPost.boundingBox())!;
        const bubbleBox = (await agentBubble.boundingBox())!;
        await page.mouse.move(rowBox.x + 4, bubbleBox.y + bubbleBox.height - 4);
        await expect.poll(state).toMatchObject({row: 'rgba(0, 0, 0, 0)', bubble: idle.bubble});
        expect(['0', 'absent']).toContain((await state()).toolbar);

        await agentBubble.hover();
        await expect.poll(async () => (await state()).toolbar).toBe('1');
        // The bubble tint transitions, so wait for it to settle.
        await expect.poll(async () => (await state()).bubble).not.toBe(idle.bubble);
        expect((await state()).row).toBe('rgba(0, 0, 0, 0)');
        await expect(toolbar).toBeVisible();

        // The toolbar sits on the bubble's top edge, within its horizontal extent.
        const toolbarBox = (await toolbar.boundingBox())!;
        expect(toolbarBox.x + toolbarBox.width).toBeLessThanOrEqual(bubbleBox.x + bubbleBox.width + 1);
        expect(toolbarBox.y).toBeLessThan(bubbleBox.y + 4);

        // Moving onto the toolbar keeps it shown.
        await toolbar.hover();
        expect((await state()).toolbar).toBe('1');

        // Keyboard: focus inside the bubble shows the toolbar without the pointer.
        await page.mouse.move(0, 0);
        await agentPost.getByRole('button', {name: 'Regenerate'}).focus();
        await expect.poll(async () => (await state()).toolbar).toBe('1');
        expect((await state()).row).toBe('rgba(0, 0, 0, 0)');
    });

    test('header menu: agent name opens New chat, Rename chat and Archive chat', async ({page}) => {
        recordConversationResponses(page, 'header rename');
        await login(page);
        await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents/chat/${botUserId}/${rootId}`, {waitUntil: 'domcontentloaded'});
        const chat = page.getByRole('region', {name: `Chat with ${botDisplayName}`});
        await expect(chat.getByTestId('agent-chat-thread').getByText(FIRST_REPLY)).toBeVisible({timeout: 30000});

        // The compass ChannelHeader (DM variant) is a <header> named after the agent, showing the chat title.
        const header = chat.locator(`header[aria-label="${botDisplayName}"]`);
        await expect(header).toBeVisible();
        await expect(header.getByText(THREAD_TITLE, {exact: true})).toBeVisible({timeout: 15000});

        // The agent name is the h1 and the menu button (the open menu's focus manager hides the rest).
        await expect(chat.getByRole('heading', {level: 1}).getByRole('button', {name: botDisplayName})).toBeVisible();
        const trigger = page.getByTestId('agent-chat-menu-button');
        await trigger.click();
        await expect(trigger).toHaveAttribute('aria-expanded', 'true');
        const menu = page.getByRole('menu', {name: `${botDisplayName} options`});
        await expect(menu.getByRole('menuitem')).toHaveText(['New chat', 'Rename chat', 'Archive chat']);

        // A regular user cannot edit the agent, so no Agent settings.
        await expect(menu.getByRole('menuitem', {name: 'Agent settings'})).toHaveCount(0);

        // Rename updates the sidebar title.
        await menu.getByRole('menuitem', {name: 'Rename chat'}).click();
        const name = page.getByLabel('Chat name');
        await expect(name).toHaveValue(THREAD_TITLE, {timeout: 10000});
        await name.fill(RENAMED_TITLE);
        const renameResponse = page.waitForResponse((r) => r.url().endsWith('/title') && r.request().method() === 'PUT');
        await page.getByRole('button', {name: 'Save'}).click();
        expect((await renameResponse).status()).toBe(200);
        await expect(name).toHaveCount(0);
        await expect(sidebar(page).getByRole('button', {name: RENAMED_TITLE, exact: true})).toBeVisible({timeout: 15000});
        await expect(header.getByText(RENAMED_TITLE, {exact: true})).toBeVisible();

        // New chat from the menu.
        await trigger.click();
        await page.getByRole('menu', {name: `${botDisplayName} options`}).getByRole('menuitem', {name: 'New chat'}).click();
        await expect(page).toHaveURL(new RegExp(`/agents/chat/${botUserId}$`));

        // The new-chat state has no chat to rename or archive.
        await trigger.click();
        await expect(page.getByRole('menu', {name: `${botDisplayName} options`}).getByRole('menuitem')).toHaveText(['New chat']);
        await page.keyboard.press('Escape');
    });

    // Starts a chat through the new-chat composer and waits for the agent's reply.
    async function startChat(page: Page, prompt: string): Promise<string> {
        await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents/chat/${botUserId}`, {waitUntil: 'domcontentloaded'});
        const textbox = newChatComposer(page).getByRole('textbox');
        await expect(textbox).toBeEnabled({timeout: 30000});
        await textbox.fill(prompt);
        await textbox.press('Enter');
        await expect(page).toHaveURL(new RegExp(`/agents/chat/${botUserId}/[a-z0-9]{26}$`), {timeout: 15000});
        await expect(page.getByTestId('agent-chat-thread').getByText(`Reply to ${prompt}.`)).toBeVisible({timeout: 60000});
        return new URL(page.url()).pathname.split('/').pop()!;
    }

    // Opens a sidebar row's actions menu, picks Archive chat, and checks the
    // confirmation sits above the product chrome before confirming.
    async function archiveFromSidebar(page: Page, rowTitle: string): Promise<void> {
        const list = sidebar(page).getByRole('list', {name: `${botDisplayName} chats`});
        const row = list.getByRole('listitem').filter({has: page.getByRole('button', {name: rowTitle, exact: true})});
        await expect(row).toHaveCount(1, {timeout: 60000});
        await row.hover();
        await row.getByRole('button', {name: `Actions for ${rowTitle}`}).click();
        // The sidebar's menu items are plain compass MenuItem buttons (no menuitem role).
        await page.locator('[data-open-popup]').getByRole('button', {name: 'Archive chat'}).click();

        const dialog = page.getByRole('dialog', {name: 'Archive chat'});
        await expect(dialog).toBeVisible();
        const box = (await dialog.boundingBox())!;
        const onTop = await page.evaluate(({x, y}) => {
            const hit = document.elementFromPoint(x, y);
            const header = document.getElementById('global-header');
            const headerBox = header?.getBoundingClientRect();
            const overHeader = headerBox ? document.elementFromPoint(headerBox.left + (headerBox.width / 2), headerBox.top + (headerBox.height / 2)) : null;
            return {
                dialogHit: Boolean(hit?.closest('[role="dialog"]')),
                headerCovered: header ? !header.contains(overHeader) : null,
            };
        }, {x: box.x + (box.width / 2), y: box.y + (box.height / 2)});
        expect(onTop.dialogHit).toBe(true);
        if (onTop.headerCovered !== null) {
            expect(onTop.headerCovered).toBe(true);
        }

        const archiveResponse = page.waitForResponse((r) => r.url().endsWith('/archive'));
        await dialog.getByRole('button', {name: 'Archive'}).click();
        const response = await archiveResponse;
        expect(response.status(), await response.text()).toBe(200);
        await expect(dialog).toBeHidden();
        await expect(row).toHaveCount(0, {timeout: 15000});
    }

    test('sidebar archive of the open chat moves to a new chat', async ({page}) => {
        recordConversationResponses(page, 'sidebar archive (open chat)');
        await login(page);
        await startChat(page, SIDEBAR_ARCHIVE_PROMPT);

        // The first chat was renamed above, so this is the only row with the generated title.
        await archiveFromSidebar(page, THREAD_TITLE);
        await expect(page).toHaveURL(new RegExp(`/agents/chat/${botUserId}$`), {timeout: 15000});
        await expect(newChatComposer(page).getByRole('textbox')).toBeVisible();
    });

    test('sidebar archive of a chat with an empty title ("New chat" row)', async ({page}) => {
        recordConversationResponses(page, 'sidebar archive (empty title)');
        await login(page);

        // Only for this chat: the title generator answers with nothing, so the row keeps the "New chat" placeholder.
        // A blank title (the sidebar trims titles); the generic title fixture must come before the reply fixture,
        // which would otherwise also match the title request that quotes the prompt.
        await aimock.setFixtures([buildTitleFixture(' '), ...replyFixtures([EMPTY_TITLE_PROMPT])]);
        let emptyRootId: string;
        try {
            emptyRootId = await startChat(page, EMPTY_TITLE_PROMPT);
            await expect(sidebar(page).getByRole('button', {name: 'New chat', exact: true})).toHaveCount(1, {timeout: 30000});
        } finally {
            await aimock.setFixtures(baseFixtures());
        }

        // Open another chat so archiving this one does not navigate.
        await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents/chat/${botUserId}/${rootId}`, {waitUntil: 'domcontentloaded'});
        await expect(page.getByTestId('agent-chat-thread').getByText(FIRST_REPLY)).toBeVisible({timeout: 30000});
        await archiveFromSidebar(page, 'New chat');
        await expect(page).toHaveURL(new RegExp(`/agents/chat/${botUserId}/${rootId}$`));

        const client = await mattermost.getClient(username, password);
        const threads = await (await fetch(`${mattermost.url()}/plugins/mattermost-ai/ai_threads`, {
            headers: {Authorization: `Bearer ${client.getToken()}`},
        })).json();
        expect(threads.some((t: {root_post_id: string}) => t.root_post_id === emptyRootId)).toBe(false);
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
            const count = await menu.getByRole('menuitem', {name: 'Agent settings'}).count();
            await page.keyboard.press('Escape');
            await expect(menu).toHaveCount(0);
            return count;
        }, {timeout: 15000}).toBe(1);

        await trigger.click();
        await menu.getByRole('menuitem', {name: 'Agent settings'}).click();
        await expect(page.getByRole('button', {name: /Back/})).toBeVisible({timeout: 15000});
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
        await login(page);
        await page.goto(`${mattermost.url()}/${teamName}/messages/@${AIMOCK_BOT_NAME}`, {waitUntil: 'domcontentloaded'});

        const channelView = page.getByTestId('channel_view');
        const messages = channelView.getByTestId('post-message-text');
        await expect(messages.getByText(FIRST_PROMPT)).toBeVisible({timeout: 30000});

        // With collapsed reply threads the DM shows the root; the bot reply is in its thread.
        await channelView.getByRole('button', {name: /1 reply/i}).first().click();
        await expect(page.locator('#rhsContainer').getByText(FIRST_REPLY)).toBeVisible({timeout: 15000});
        await expect(page.locator('#rhsContainer').getByText(FIRST_PROMPT)).toBeVisible();

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
        await login(page);
        await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents/chat/${botUserId}/${rootId}`, {waitUntil: 'domcontentloaded'});

        const reply = page.getByTestId('agent-chat-thread').getByRole('textbox');
        await expect(reply).toBeEnabled({timeout: 30000});
        await reply.fill('reply sent with enter');
        await reply.press('Enter');
        await expect(page.getByTestId('agent-chat-thread').getByTestId('post-message-text').getByText('reply sent with enter')).toBeVisible({timeout: 30000});
    });

    test('Enter sends from the new-chat composer, then the header archives it', async ({page}) => {
        recordConversationResponses(page, 'header archive');
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
        await expect(page.getByTestId('agent-chat-thread').getByTestId('post-message-text').getByText(ENTER_PROMPT)).toBeVisible({timeout: 30000});
        const newRootId = new URL(page.url()).pathname.split('/').pop()!;

        // Archive it from the header once the agent's reply (which carries the conversation id) is in.
        const chat = page.getByRole('region', {name: `Chat with ${botDisplayName}`});
        const trigger = page.getByTestId('agent-chat-menu-button');
        const menu = page.getByRole('menu', {name: `${botDisplayName} options`});
        await expect.poll(async () => {
            await trigger.click();
            const count = await menu.getByRole('menuitem', {name: 'Archive chat'}).count();
            if (count === 0) {
                await page.keyboard.press('Escape');
                await expect(menu).toHaveCount(0);
            }
            return count;
        }, {timeout: 60000}).toBe(1);
        await menu.getByRole('menuitem', {name: 'Archive chat'}).click();
        const archiveResponse = page.waitForResponse((r) => r.url().endsWith('/archive'));
        await page.getByRole('dialog', {name: 'Archive chat'}).getByRole('button', {name: 'Archive'}).click();
        expect((await archiveResponse).status()).toBe(200);
        await expect(page).toHaveURL(new RegExp(`/agents/chat/${botUserId}$`), {timeout: 15000});
        const client = await mattermost.getClient(username, password);
        await expect.poll(async () => {
            const threads = await (await fetch(`${mattermost.url()}/plugins/mattermost-ai/ai_threads`, {
                headers: {Authorization: `Bearer ${client.getToken()}`},
            })).json();
            return threads.some((t: {root_post_id: string}) => t.root_post_id === newRootId);
        }, {timeout: 15000}).toBe(false);

        // The draft is cleared, so going back to a new chat starts empty.
        await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents/chat/${botUserId}`, {waitUntil: 'domcontentloaded'});
        await expect(newChatComposer(page).getByRole('textbox')).toHaveValue('', {timeout: 30000});
    });
});
