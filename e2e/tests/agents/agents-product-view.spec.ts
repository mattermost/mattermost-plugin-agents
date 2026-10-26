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
import {buildTextResponse} from 'helpers/aimock-fixtures';
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
                fixtures: buildTextResponse({userMessage: FIRST_PROMPT, content: FIRST_REPLY, title: THREAD_TITLE}),
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
        await newChatComposer(page).getByTestId('SendMessageButton').click();

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
        await expect(threadList.getByRole('button', {name: THREAD_TITLE})).toBeVisible({timeout: 60000});
        await expect(threadList.getByRole('button', {name: THREAD_TITLE})).toHaveAttribute('aria-current', 'page');

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
        const threadRow = nav.getByRole('button', {name: THREAD_TITLE});
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

    test('Enter sends from the new-chat composer', async ({page}) => {
        await login(page);
        await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents/chat/${botUserId}`, {waitUntil: 'domcontentloaded'});

        const textbox = newChatComposer(page).getByRole('textbox');
        await expect(textbox).toBeEnabled({timeout: 30000});
        await textbox.fill('new chat sent with enter');

        // The host editor swallows Enter for 500ms after it mounts for a new root
        // post (CHANNEL_SWITCH_IGNORE_ENTER_THRESHOLD_MS in post_utils.ts), to avoid
        // sending into a channel the user just switched to. A human cannot type a
        // message that fast; a script can, so wait the guard out.
        await page.waitForTimeout(600);
        await textbox.press('Enter');

        await expect(page).toHaveURL(new RegExp(`/agents/chat/${botUserId}/[a-z0-9]{26}$`), {timeout: 15000});
        await expect(page.getByTestId('agent-chat-thread').getByTestId('post-message-text').getByText('new chat sent with enter')).toBeVisible({timeout: 30000});

        // The draft is cleared, so going back to a new chat starts empty.
        await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents/chat/${botUserId}`, {waitUntil: 'domcontentloaded'});
        await expect(newChatComposer(page).getByRole('textbox')).toHaveValue('', {timeout: 30000});
    });
});
