// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// HTML artifacts: when the agent's CreateFile tool writes an .html file and
// `enableHTMLArtifacts` is on, the bot post renders it as a sandboxed,
// interactive artifact (inline card + fullscreen viewer). Artifact JS reaches
// user data only through the consent-gated window.mattermost broker and has no
// network access.

import fs from 'fs';
import path from 'path';

import {test, expect, type Locator, type Page} from '@playwright/test';

import {AIMockContainer, RunAIMockSidecar} from 'helpers/aimock-container';
import {buildCreateFileSequence, buildTitleFixture, mergeFixtureFiles} from 'helpers/aimock-fixtures';
import MattermostContainer from 'helpers/mmcontainer';
import {MattermostPage} from 'helpers/mm';
import {AIPlugin} from 'helpers/ai-plugin';
import {RunToolConfigAIMockContainer, setupRegularTestUser} from 'helpers/tool-config-container';
import {
    ONYX_THEME,
    SPRINT_DASHBOARD_FILE_NAME,
    SPRINT_DASHBOARD_HTML,
    SELF_NAVIGATING_FILE_NAME,
    SELF_NAVIGATING_HTML,
    SELF_NAVIGATION_IMPOSTOR_HTML,
    SELF_NAVIGATION_TARGET,
    REPLACEMENT_IMPOSTOR_HTML,
    REPLACEMENT_TARGET,
    REPLACING_FILE_NAME,
    REPLACING_HTML,
    TOKEN_PROBE_FILE_NAME,
    TOKEN_PROBE_HTML,
    artifactIframes,
    setHTMLArtifactsEnabled,
    setUserTheme,
} from 'helpers/html-artifact';

const username = 'regularuser';
const password = 'regularuser';
const firstName = 'Alex';
const lastName = 'Rivera';
const botName = 'toolbot';

const screenshotDir = path.join(__dirname, '..', '..', 'screenshots', 'html-artifacts');

async function shot(page: Page, name: string): Promise<void> {
    fs.mkdirSync(screenshotDir, {recursive: true});

    // Let fonts, iframe resize and transitions settle before capturing.
    await page.evaluate(() => document.fonts.ready);
    await page.waitForTimeout(600);
    await page.screenshot({path: path.join(screenshotDir, `${name}.png`)});
}

/** Scrolls so `el` starts at the top of its scroll container. */
async function scrollToTop(el: Locator): Promise<void> {
    await el.evaluate((node) => node.scrollIntoView({block: 'start'}));
}

/** Opens the DM with the bot in the center channel and returns the latest artifact card there. */
async function openArtifactInBotDM(page: Page, mattermost: MattermostContainer): Promise<{botPost: Locator; card: Locator; iframe: Locator}> {
    await page.goto(`${mattermost.url()}/test/messages/@${botName}`);
    const rhsClose = page.getByTestId('mattermost-ai-rhs').getByRole('button', {name: /close/i});
    if (await rhsClose.first().isVisible().catch(() => false)) {
        await rhsClose.first().click();
    }
    const botPost = page.locator('#postListContent [data-testid="llm-bot-post"]').last();
    const card = botPost.getByTestId('html-artifact-card');
    await expect(card).toBeVisible({timeout: 60000});
    const iframe = artifactIframes(card).first();
    await expect(iframe.contentFrame().getByRole('heading', {name: 'Sprint 42 dashboard'})).toBeVisible({timeout: 30000});
    await scrollToTop(botPost);
    return {botPost, card, iframe};
}

/** Sends `prompt` to the bot in the Agents RHS and returns the inline artifact iframe in the reply. */
async function askForArtifact(page: Page, mattermost: MattermostContainer, aimock: AIMockContainer, prompt: string): Promise<{botPost: Locator; iframe: Locator}> {
    await aimock.setFixtures(mergeFixtureFiles(
        {fixtures: [buildTitleFixture('Sprint dashboard')]},
        buildCreateFileSequence({
            userPrompt: prompt,
            fileName: SPRINT_DASHBOARD_FILE_NAME,
            fileContent: SPRINT_DASHBOARD_HTML,
            finalText: 'Here is your sprint dashboard.',
            toolCallId: `call_html_artifact_${Date.now()}`,
        }),
    ));

    const mmPage = new MattermostPage(page);
    const aiPlugin = new AIPlugin(page);
    await mmPage.login(mattermost.url(), username, password);
    await aiPlugin.openRHS();
    await aiPlugin.sendMessage(prompt);

    const rhs = page.getByTestId('mattermost-ai-rhs');
    const botPost = rhs.locator('[data-testid="llm-bot-post"]').last();
    await expect(botPost).toBeVisible({timeout: 90000});
    await expect(botPost.getByText('Here is your sprint dashboard.')).toBeVisible({timeout: 120000});

    const iframe = artifactIframes(botPost).first();
    await expect(iframe).toBeVisible({timeout: 60000});
    return {botPost, iframe};
}

/** Has the bot post `html` as `fileName` in the RHS and returns the bot post. */
async function postArtifact(page: Page, mattermost: MattermostContainer, aimock: AIMockContainer, fileName: string, html: string, finalText: string): Promise<Locator> {
    const prompt = `${fileName} ${Date.now()}`;
    await aimock.setFixtures(mergeFixtureFiles(
        {fixtures: [buildTitleFixture(fileName)]},
        buildCreateFileSequence({
            userPrompt: prompt,
            fileName,
            fileContent: html,
            finalText,
            toolCallId: `call_html_artifact_${Date.now()}`,
        }),
    ));
    await new MattermostPage(page).login(mattermost.url(), username, password);
    const aiPlugin = new AIPlugin(page);
    await aiPlugin.openRHS();
    await aiPlugin.sendMessage(prompt);
    const botPost = page.getByTestId('mattermost-ai-rhs').locator('[data-testid="llm-bot-post"]').last();
    await expect(botPost.getByText(finalText)).toBeVisible({timeout: 120000});
    return botPost;
}

test.describe('HTML artifacts', () => {
    test.describe.configure({mode: 'serial'});
    // Granting notifications keeps the browser-permission announcement bar out of screenshots.
    test.use({viewport: {width: 1440, height: 900}, permissions: ['notifications']});

    let mattermost: MattermostContainer;
    let aimock: AIMockContainer;

    test.beforeAll(async () => {
        test.setTimeout(240000);
        mattermost = await RunToolConfigAIMockContainer({
            customInstructions: 'When asked to create a file, use the CreateFile tool.',
            defaultBotName: botName,
            botId: 'html-artifact-bot',
            botDisplayName: 'Artifact Bot',
        });
        await setHTMLArtifactsEnabled(mattermost, true);
        const admin = await mattermost.getAdminClient();
        await admin.patchConfig({
            ServiceSettings: {CollapsedThreads: 'default_off'},

            // No "Preview Mode" announcement bar in screenshots.
            EmailSettings: {EnablePreviewModeBanner: false},
        } as any);
        await setupRegularTestUser(mattermost);
        const client = await mattermost.getClient(username, password);
        await client.patchMe({first_name: firstName, last_name: lastName});

        // Show bot replies inline in the DM (center channel) rather than only in threads.
        const me = await client.getMe();
        await client.savePreferences(me.id, [{user_id: me.id, category: 'display_settings', name: 'collapsed_reply_threads', value: 'off'}]);
        aimock = await RunAIMockSidecar(mattermost.network, {
            fixtures: {fixtures: [buildTitleFixture('HTML artifact bootstrap')]},
        });
    });

    test.afterAll(async () => {
        await aimock?.stop();
        await mattermost?.stop();
    });

    test('renders inline, brokers getCurrentUser with consent, and opens fullscreen', async ({page}) => {
        test.setTimeout(300000);
        const {botPost, iframe} = await askForArtifact(page, mattermost, aimock, `make a sprint dashboard ${Date.now()}`);

        // Card header shows the file name.
        const rhsCard = botPost.getByTestId('html-artifact-card');
        await expect(rhsCard.getByText(SPRINT_DASHBOARD_FILE_NAME)).toBeVisible();

        // The host-drawn safety notice is always present outside the frame.
        await expect(rhsCard.getByTestId('html-artifact-notice')).toHaveAttribute('aria-label', /Never enter passwords/);

        // Sandbox: scripts only, never same-origin.
        const sandbox = await iframe.getAttribute('sandbox');
        expect(sandbox?.split(/\s+/).sort()).toEqual(['allow-scripts']);

        // Content renders and is interactive.
        const rhsFrame = iframe.contentFrame();
        await expect(rhsFrame.getByRole('heading', {name: 'Sprint 42 dashboard'})).toBeVisible({timeout: 30000});
        await expect(rhsFrame.getByTestId('stat-completed')).toHaveText('34');
        await scrollToTop(rhsCard);
        await shot(page, '01-inline-card-rhs');

        // The same conversation is a DM with the bot; the card renders in the center channel too.
        const center = await openArtifactInBotDM(page, mattermost);
        const frame = center.iframe.contentFrame();
        await frame.getByRole('button', {name: /Kudos/}).click();
        await expect(frame.locator('#kudos-count')).toHaveText('1');
        await expect(center.card.getByTestId('html-artifact-notice')).toContainText("Don't enter passwords");

        // Inline height follows the content: no inner scrollbar.
        await expect.poll(async () => frame.locator('html').evaluate((el) => el.scrollHeight - el.clientHeight), {timeout: 10000}).toBeLessThanOrEqual(1);
        await shot(page, '01-inline-card');

        // getCurrentUser → host-side consent prompt (not a browser dialog).
        await frame.getByRole('button', {name: 'Say hello'}).click();
        const consent = center.card.getByTestId('html-artifact-consent');
        await expect(consent).toBeVisible({timeout: 15000});
        await expect(consent.getByText(/wants to read your name .* and username/i)).toBeVisible();

        // Answers count only once the prompt has been on screen briefly (anti-clickjacking).
        await expect(consent.locator('[data-armed="true"]')).toHaveCount(1, {timeout: 5000});
        await shot(page, '02-consent-prompt');

        await consent.getByRole('button', {name: /^allow$/i}).click();
        await expect(consent).not.toBeVisible();
        const greeting = frame.locator('#greeting');
        await expect(greeting).toHaveText(new RegExp(`^Hello, (${firstName} ${lastName}|${firstName}|${username})!$`), {timeout: 15000});
        await shot(page, '03-greeting-after-allow');

        // Fullscreen viewer: separate iframe instance.
        await center.card.getByRole('button', {name: /fullscreen/i}).click();
        const viewer = page.getByTestId('html-artifact-fullscreen');
        await expect(viewer).toBeVisible({timeout: 15000});
        const fullIframe = artifactIframes(viewer).first();
        expect((await fullIframe.getAttribute('sandbox'))?.split(/\s+/)).toEqual(['allow-scripts']);
        const fullFrame = fullIframe.contentFrame();
        await expect(fullFrame.getByRole('heading', {name: 'Sprint 42 dashboard'})).toBeVisible({timeout: 30000});
        await expect(viewer.getByText('Interactive')).toBeVisible();
        await expect(viewer.getByTestId('html-artifact-notice')).toContainText("Don't enter passwords");

        // The display mode reaches the artifact: fullscreen-only content shows.
        await expect(fullFrame.getByTestId('fullscreen-details')).toBeVisible({timeout: 15000});
        await expect(center.iframe.contentFrame().getByTestId('fullscreen-details')).toBeHidden();
        await fullFrame.getByRole('tab', {name: 'Team'}).click();
        await expect(fullFrame.getByText('Consent broker')).toBeVisible();

        // The consent decision is remembered per file: no second prompt.
        await fullFrame.getByRole('button', {name: 'Say hello'}).click();
        await expect(fullFrame.locator('#greeting')).toHaveText(/^Hello, /, {timeout: 15000});
        await expect(viewer.getByTestId('html-artifact-consent')).toHaveCount(0);
        await shot(page, '04-fullscreen');

        // Escape closes the viewer even while focus is inside the artifact.
        // Split press: the viewer closes on keydown, and a keyup aimed at the
        // removed (out-of-process) artifact frame can stall the CDP input queue.
        await page.keyboard.down('Escape');
        await expect(viewer).not.toBeVisible({timeout: 10000});
        await page.keyboard.up('Escape');
        await expect(center.iframe).toBeVisible();
    });

    test('artifact has no network access to the Mattermost API', async ({page}) => {
        test.setTimeout(300000);
        const {iframe} = await askForArtifact(page, mattermost, aimock, `dashboard network probe ${Date.now()}`);
        const netResult = iframe.contentFrame().getByTestId('net-result');
        // The probe is visually hidden; its text content is still asserted.
        await expect(netResult).toContainText('network: blocked', {timeout: 30000});
        await expect(netResult).not.toContainText('LEAKED');
    });

    // Self-navigation of a sandboxed frame cannot be blocked (CSP and the
    // Navigation API do not cover opaque-origin frames), so the request does
    // go out. What the host guarantees: it does not remount (which would
    // re-run the navigation), and the replacement page gets no broker access.
    test('self-navigating artifact is unloaded and its replacement gets no access', async ({page}) => {
        test.setTimeout(300000);
        await page.context().route(`${SELF_NAVIGATION_TARGET}**`, (route) => {
            return route.fulfill({contentType: 'text/html', body: SELF_NAVIGATION_IMPOSTOR_HTML});
        });
        let artifactRequests = 0;
        page.on('request', (req) => {
            if (req.url().includes('/plugins/mattermost-ai/artifacts/') && !req.url().endsWith('/token')) {
                artifactRequests++;
            }
        });
        const navigated = page.waitForRequest((req) => req.url().startsWith(SELF_NAVIGATION_TARGET), {timeout: 180000});

        const botPost = await postArtifact(page, mattermost, aimock, SELF_NAVIGATING_FILE_NAME, SELF_NAVIGATING_HTML, 'Here is the navigator.');
        await navigated;

        const stopped = botPost.getByTestId('html-artifact-navigation-stopped');
        await expect(stopped).toBeVisible({timeout: 30000});
        await expect(artifactIframes(botPost)).toHaveCount(0);
        expect(artifactRequests).toBe(1);
        await expect(page.getByTestId('html-artifact-consent')).toHaveCount(0);
        await expect(stopped).toBeVisible();
    });

    // A parser-blocking location.replace() swaps the artifact for another
    // page before the first load event, so the frame-load guard cannot tell.
    // The replacement does not know the bridge token, so it gets nothing.
    test('a document that replaces the artifact before load gets no broker access', async ({page}) => {
        test.setTimeout(300000);
        await page.context().route(`${REPLACEMENT_TARGET}**`, (route) => {
            return route.fulfill({contentType: 'text/html', body: REPLACEMENT_IMPOSTOR_HTML});
        });
        const botPost = await postArtifact(page, mattermost, aimock, REPLACING_FILE_NAME, REPLACING_HTML, 'Here is the replacer.');

        const iframe = artifactIframes(botPost).first();
        const frame = iframe.contentFrame();
        await expect(frame.getByTestId('impostor-sent')).toHaveText('sent', {timeout: 30000});

        // The host never accepts a ready, so it ends in the load error state.
        await expect(botPost.getByTestId('html-artifact-card').getByText('This artifact could not be loaded.')).toBeVisible({timeout: 30000});
        await expect(page.getByTestId('html-artifact-consent')).toHaveCount(0);
        await expect(frame.getByTestId('impostor-received')).toHaveText('');
    });

    test('artifact code cannot read the bridge token or intercept its messages', async ({page}) => {
        test.setTimeout(300000);
        const botPost = await postArtifact(page, mattermost, aimock, TOKEN_PROBE_FILE_NAME, TOKEN_PROBE_HTML, 'Here is the probe.');
        const iframe = artifactIframes(botPost).first();
        const frame = iframe.contentFrame();

        // The host still talks to the genuine bridge: the frame becomes ready (visible).
        await expect(frame.getByRole('heading', {name: 'Token probe'})).toBeVisible({timeout: 30000});
        await expect(frame.getByTestId('probe-recorded')).not.toHaveText('', {timeout: 15000});

        const src = await iframe.getAttribute('src');
        const fileId = src!.split('/artifacts/')[1].split(/[/?]/)[0];
        const res = await page.request.get(`${mattermost.url()}/plugins/mattermost-ai/artifacts/${fileId}/token`, {
            headers: {'X-Requested-With': 'XMLHttpRequest'},
        });
        expect(res.ok()).toBe(true);
        const {token} = await res.json();
        expect(typeof token).toBe('string');
        expect(token.length).toBeGreaterThan(20);

        // The shadowed window.parent only ever saw the artifact's own message.
        const recorded = await frame.getByTestId('probe-recorded').textContent();
        expect(JSON.parse(recorded!)).toEqual([{probe: 1}]);
        expect(recorded).not.toContain(token);

        const dom = await frame.getByTestId('probe-dom').textContent();
        expect(dom).toContain('Token probe');
        expect(dom).not.toContain(token);
    });

    test('inline card in a dark theme', async ({page}) => {
        test.setTimeout(300000);
        await setUserTheme(mattermost, username, password, ONYX_THEME);
        try {
            await askForArtifact(page, mattermost, aimock, `dark dashboard ${Date.now()}`);
            const {iframe} = await openArtifactInBotDM(page, mattermost);
            const frame = iframe.contentFrame();
            await expect(frame.getByRole('heading', {name: 'Sprint 42 dashboard'})).toBeVisible({timeout: 30000});
            // Theme variables reach the artifact via the context message.
            await expect.poll(async () => frame.locator('html').evaluate(
                (el) => getComputedStyle(el).getPropertyValue('--mm-center-channel-bg').trim().toLowerCase(),
            ), {timeout: 15000}).toBe(ONYX_THEME.centerChannelBg);
            await shot(page, '05-inline-card-dark');

            await page.getByTestId('html-artifact-open-fullscreen').last().click();
            const viewer = page.getByTestId('html-artifact-fullscreen');
            await expect(viewer).toBeVisible({timeout: 15000});
            const fullFrame = artifactIframes(viewer).first().contentFrame();
            await expect(fullFrame.getByTestId('fullscreen-details')).toBeVisible({timeout: 30000});
            await shot(page, '06-fullscreen-dark');
            await viewer.getByTestId('html-artifact-fullscreen-close').click();
            await expect(viewer).not.toBeVisible();
        } finally {
            await setUserTheme(mattermost, username, password, null);
        }
    });
});
