// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// HTML artifacts: when the agent's CreateFile tool writes an .html file and
// `enableHTMLArtifacts` is on, the bot post renders it as a sandboxed,
// interactive artifact (inline card + fullscreen viewer). Artifact JS reaches
// user data only through the consent-gated window.mattermost broker and has no
// network access. No viewer's page runs an artifact by itself, the
// requester's included: each viewer clicks Run (or opens it fullscreen). The
// host frames the sandboxed artifact inside a srcdoc wrapper whose CSP blocks
// any navigation of the artifact's frame away from the artifact, and an
// artifact takes clicks and keyboard focus only once the viewer activates it:
// the click on Run (or Open fullscreen) does, and so does clicking the
// artifact again after focus moved back to the page.

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
    ARTIFACT_CONTENT_URL,
    ARTIFACT_TOKEN_URL,
    EXFIL_HOST,
    EXFIL_URL,
    ExternalServer,
    FOCUS_THIEF_FILE_NAME,
    FOCUS_THIEF_HTML,
    ONYX_THEME,
    SECOND_VIEWER_PASSWORD,
    SECOND_VIEWER_USERNAME,
    SPRINT_DASHBOARD_FILE_NAME,
    SPRINT_DASHBOARD_HTML,
    STANDUP_NOTES_FILE_NAME,
    STANDUP_NOTES_HTML,
    SelfNavigationKind,
    TOKEN_PROBE_FILE_NAME,
    TOKEN_PROBE_HTML,
    artifactContentIframe,
    artifactFrame,
    artifactWrappers,
    engageArtifact,
    focusMessageBox,
    interactOverlay,
    runArtifact,
    selfNavigationHTML,
    setHTMLArtifactsEnabled,
    setUserTheme,
    setupSecondViewer,
    startExternalServer,
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

/**
 * Opens the DM with the bot in the center channel, without reloading the
 * page, closes the Agents RHS, and returns the latest artifact card there.
 * The viewer ran it in the RHS earlier in this page session, so it runs here
 * without asking again.
 */
async function openArtifactInBotDM(page: Page): Promise<{botPost: Locator; card: Locator; iframe: Locator}> {
    await page.evaluate((url) => {
        (window as unknown as {WebappUtils: {browserHistory: {push: (u: string) => void}}}).WebappUtils.browserHistory.push(url);
    }, `/test/messages/@${botName}`);
    await expect(page).toHaveURL(new RegExp(`/test/messages/@${botName}$`));
    await new AIPlugin(page).closeRHS();
    await expect(page.getByTestId('mattermost-ai-rhs')).toBeHidden();

    const botPost = page.locator('#postListContent [data-testid="llm-bot-post"]').last();
    const card = botPost.getByTestId('html-artifact-card');
    await expect(card).toBeVisible({timeout: 60000});
    await expect(card.getByTestId('html-artifact-run-placeholder')).toHaveCount(0);
    const iframe = artifactWrappers(card).first();
    await expect(artifactFrame(iframe).getByRole('heading', {name: 'Sprint 42 dashboard'})).toBeVisible({timeout: 30000});
    await scrollToTop(botPost);
    return {botPost, card, iframe};
}

/**
 * Asserts the card header layout: the action buttons share the file name's
 * row, and the safety notice is either on that row or entirely below it,
 * never wrapped in between. Returns whether the notice is on its own row.
 */
type Rect = {x: number; y: number; width: number; height: number};
type HeaderRects = {card: Rect; name: Rect; button: Rect; reload: Rect; notice: Rect};

/** Measures the card and its header parts in one atomic snapshot (no layout movement between reads). */
async function measureHeader(card: Locator): Promise<HeaderRects | null> {
    return card.evaluate((root) => {
        const rect = (el: Element | null) => {
            if (!el) {
                return null;
            }
            const r = el.getBoundingClientRect();
            return {x: Math.round(r.x), y: Math.round(r.y), width: Math.round(r.width), height: Math.round(r.height)};
        };
        const part = (id: string) => rect(root.querySelector(`[data-testid="${id}"]`));
        const name = part('html-artifact-file-name');
        const button = part('html-artifact-open-fullscreen');
        const reload = part('html-artifact-reload');
        const notice = part('html-artifact-notice');
        if (!name || !button || !reload || !notice) {
            return null;
        }
        return {card: rect(root)!, name, button, reload, notice};
    });
}

/** Waits until two consecutive snapshots ~100ms apart are identical (scroll/resize settled) and returns it. */
async function stableHeaderRects(card: Locator): Promise<HeaderRects> {
    let prev = '';
    let stable: HeaderRects | null = null;
    await expect.poll(async () => {
        const rects = await measureHeader(card);
        const key = JSON.stringify(rects);
        const settled = rects !== null && key === prev;
        prev = key;
        stable = settled ? rects : null;
        return settled;
    }, {timeout: 10000, intervals: [100]}).toBe(true);
    return stable!;
}

async function expectHeaderLayout(card: Locator): Promise<boolean> {
    const {name: nameBox, button: buttonBox, reload: reloadBox, notice: noticeBox} = await stableHeaderRects(card);
    const nameMid = nameBox.y + (nameBox.height / 2);
    expect(Math.abs((buttonBox.y + (buttonBox.height / 2)) - nameMid)).toBeLessThanOrEqual(4);
    expect(Math.abs((reloadBox.y + (reloadBox.height / 2)) - nameMid)).toBeLessThanOrEqual(4);
    const noticeMid = noticeBox.y + (noticeBox.height / 2);
    const inline = Math.abs(noticeMid - nameMid) <= 4;
    const below = noticeBox.y >= Math.max(nameBox.y + nameBox.height, buttonBox.y + buttonBox.height) - 1;
    expect(inline || below).toBe(true);
    if (inline) {
        // On the first row the notice sits between the name and the buttons.
        expect(noticeBox.x).toBeGreaterThanOrEqual(nameBox.x + nameBox.width - 1);
        expect(noticeBox.x + noticeBox.width).toBeLessThanOrEqual(buttonBox.x + 1);
    }
    return !inline;
}

/** Has the bot post `html` as `fileName` in the RHS and returns the bot post, not yet run. */
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
    await expect(botPost).toBeVisible({timeout: 90000});
    await expect(botPost.getByText(finalText)).toBeVisible({timeout: 120000});
    await expect(botPost.getByTestId('html-artifact-run-placeholder')).toBeVisible({timeout: 60000});
    return botPost;
}

/** Has the bot post the sprint dashboard in the RHS, runs it, and returns the inline artifact's wrapper frame. */
async function askForArtifact(page: Page, mattermost: MattermostContainer, aimock: AIMockContainer, prompt: string): Promise<{botPost: Locator; iframe: Locator}> {
    const botPost = await postArtifact(page, mattermost, aimock, SPRINT_DASHBOARD_FILE_NAME, SPRINT_DASHBOARD_HTML, `Here is your sprint dashboard (${prompt}).`);
    await runArtifact(botPost);
    const iframe = artifactWrappers(botPost).first();
    await expect(iframe).toBeVisible({timeout: 60000});
    return {botPost, iframe};
}

test.describe('HTML artifacts', () => {
    test.describe.configure({mode: 'serial'});
    // Granting notifications keeps the browser-permission announcement bar out of screenshots.
    test.use({viewport: {width: 1440, height: 900}, permissions: ['notifications']});

    let mattermost: MattermostContainer;
    let aimock: AIMockContainer;
    let externalServer: ExternalServer;

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
        await setupSecondViewer(mattermost);
        aimock = await RunAIMockSidecar(mattermost.network, {
            fixtures: {fixtures: [buildTitleFixture('HTML artifact bootstrap')]},
        });
        externalServer = await startExternalServer(mattermost.url());
    });

    test.afterAll(async () => {
        await externalServer?.close();
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
        // In the narrow RHS card the full warning moves to its own row, and the
        // file name keeps enough room to be read.
        const notice = rhsCard.getByTestId('html-artifact-notice');
        await expect(notice).toBeVisible();
        await expect(notice).toHaveText("AI-generated · Don't enter passwords");
        const fileName = rhsCard.getByTestId('html-artifact-file-name');
        await expect(fileName).toHaveAttribute('title', SPRINT_DASHBOARD_FILE_NAME);
        const nameFits = await fileName.evaluate((el) => el.scrollWidth <= el.clientWidth);
        expect(nameFits).toBe(true);

        // The host's frame is an unsandboxed wrapper whose CSP lets its child
        // frame load only the artifact; the artifact's own frame has scripts
        // only, never same-origin.
        await expect(iframe).toHaveAttribute('title', `Interactive artifact: ${SPRINT_DASHBOARD_FILE_NAME}`);
        expect(await iframe.getAttribute('sandbox')).toBeNull();
        const content = artifactContentIframe(iframe);
        await expect(content).toHaveAttribute('src', ARTIFACT_CONTENT_URL);
        expect((await content.getAttribute('sandbox'))?.split(/\s+/).sort()).toEqual(['allow-scripts']);
        const [, fileId] = ARTIFACT_CONTENT_URL.exec((await content.getAttribute('src'))!)!;
        await expect(iframe.contentFrame().locator('meta[http-equiv="Content-Security-Policy"]')).toHaveAttribute(
            'content',
            `default-src 'none'; style-src 'unsafe-inline'; frame-src ${new URL(mattermost.url()).origin}/plugins/mattermost-ai/artifacts/${fileId}`,
        );

        // Content renders and is interactive.
        const rhsFrame = artifactFrame(iframe);
        await expect(rhsFrame.getByRole('heading', {name: 'Sprint 42 dashboard'})).toBeVisible({timeout: 30000});
        await expect(rhsFrame.getByTestId('stat-completed')).toHaveText('34');
        expect(await expectHeaderLayout(rhsCard)).toBe(true);
        await scrollToTop(rhsCard);
        await shot(page, '01-inline-card-rhs');

        // The same conversation is a DM with the bot; the card renders in the
        // center channel too, already running since it ran in the RHS.
        const center = await openArtifactInBotDM(page);
        const frame = artifactFrame(center.iframe);

        // Rendered again without a click, the artifact takes clicks (and
        // keyboard focus) only once the viewer activates it.
        await expect(center.card.getByTestId('html-artifact-frame')).toHaveAttribute('data-engaged', 'false');
        await expect(center.card.getByTestId('html-artifact-interact')).toBeVisible();
        await expect(center.card.getByTestId('html-artifact-interact-hint')).toHaveText('Click to interact');
        await expect(center.iframe).toHaveAttribute('tabindex', '-1');
        await engageArtifact(center.card);
        await expect(center.card.getByTestId('html-artifact-interact')).toHaveCount(0);
        await frame.getByRole('button', {name: /Kudos/}).click();
        await expect(frame.locator('#kudos-count')).toHaveText('1');
        await expect(center.card.getByTestId('html-artifact-notice')).toContainText("Don't enter passwords");

        // Inline height follows the content: no inner scrollbar.
        await expect.poll(async () => frame.locator('html').evaluate((el) => el.scrollHeight - el.clientHeight), {timeout: 10000}).toBeLessThanOrEqual(1);
        expect(await expectHeaderLayout(center.card)).toBe(false);
        await shot(page, '01-inline-card');

        // Intermediate widths (e.g. a narrow window or the RHS open): the
        // header never wraps the notice in between the name and the buttons.
        for (const width of [1100, 1000, 900, 820, 760]) {
            await page.setViewportSize({width, height: 900});
            await expectHeaderLayout(center.card);
        }
        await page.setViewportSize({width: 860, height: 900});
        const midWidth = (await stableHeaderRects(center.card)).card.width;
        expect(midWidth).toBeGreaterThan(400);
        expect(midWidth).toBeLessThan(640);
        expect(await expectHeaderLayout(center.card)).toBe(true);
        await scrollToTop(center.botPost);
        await shot(page, '07-inline-card-mid');
        await page.setViewportSize({width: 1440, height: 900});
        await scrollToTop(center.botPost);

        // getCurrentUser → host-side consent prompt (not a browser dialog).
        await engageArtifact(center.card);
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

        // Fullscreen viewer: separate frame instance, same structure.
        await center.card.getByRole('button', {name: /fullscreen/i}).click();
        const viewer = page.getByTestId('html-artifact-fullscreen');
        await expect(viewer).toBeVisible({timeout: 15000});
        const fullIframe = artifactWrappers(viewer).first();
        expect(await fullIframe.getAttribute('sandbox')).toBeNull();
        expect((await artifactContentIframe(fullIframe).getAttribute('sandbox'))?.split(/\s+/)).toEqual(['allow-scripts']);
        const fullFrame = artifactFrame(fullIframe);
        await expect(fullFrame.getByRole('heading', {name: 'Sprint 42 dashboard'})).toBeVisible({timeout: 30000});
        await expect(viewer.getByText('Interactive')).toBeVisible();
        await expect(viewer.getByTestId('html-artifact-notice')).toContainText("Don't enter passwords");

        // The click that opened fullscreen engages the fullscreen frame, not
        // the inline one under it. Focus inside the artifact shows up on this
        // page as the wrapper frame, which is now the viewer's tab stop.
        await expect(viewer.getByTestId('html-artifact-frame')).toHaveAttribute('data-engaged', 'true');
        await expect(viewer.getByTestId('html-artifact-interact')).toHaveCount(0);
        await expect(center.card.getByTestId('html-artifact-frame')).toHaveAttribute('data-engaged', 'false');
        await expect.poll(() => page.evaluate(() => document.activeElement?.getAttribute('data-testid'))).toBe('html-artifact-wrapper');
        expect(await fullIframe.getAttribute('tabindex')).toBeNull();

        // The display mode reaches the artifact: fullscreen-only content shows.
        await expect(fullFrame.getByTestId('fullscreen-details')).toBeVisible({timeout: 15000});
        await expect(frame.getByTestId('fullscreen-details')).toBeHidden();
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
        const netResult = artifactFrame(iframe).getByTestId('net-result');
        // The probe is visually hidden; its text content is still asserted.
        await expect(netResult).toContainText('network: blocked', {timeout: 30000});
        await expect(netResult).not.toContainText('LEAKED');
    });

    // A sandboxed frame may navigate itself, carrying data away in the URL.
    // The wrapper's frame-src blocks every such navigation before a request
    // goes out. Chromium then commits an error page into the frame (a second
    // load) and Firefox fires an error on it; either way the host stops the
    // artifact. A navigation before the artifact's first load leaves a frame
    // that never got ready, which the host unloads as a failed load.
    const selfNavigationCases: Array<{kind: SelfNavigationKind; target: 'exfil host' | 'external server'; outcome: 'stopped' | 'load error'}> = [
        {kind: 'location.href', target: 'exfil host', outcome: 'stopped'},
        {kind: 'meta refresh', target: 'exfil host', outcome: 'stopped'},
        {kind: 'location.replace before load', target: 'exfil host', outcome: 'load error'},
        {kind: 'link click', target: 'external server', outcome: 'stopped'},
    ];
    for (const {kind, target, outcome} of selfNavigationCases) {
        test(`self-navigation by ${kind} sends no request to the ${target}`, async ({page}) => {
            test.setTimeout(300000);
            const context = page.context();
            const exfilRoutes: string[] = [];
            await context.route((url) => url.hostname === EXFIL_HOST, (route) => {
                exfilRoutes.push(route.request().url());
                return route.fulfill({contentType: 'text/html', body: '<p>exfiltrated</p>'});
            });
            const outbound: string[] = [];
            let contentLoads = 0;
            context.on('request', (req) => {
                const url = req.url();
                if (url.startsWith(`https://${EXFIL_HOST}`) || url.startsWith(externalServer.url)) {
                    outbound.push(url);
                }
                if (ARTIFACT_CONTENT_URL.test(url)) {
                    contentLoads++;
                }
            });
            const serverRequestsBefore = externalServer.requests.length;
            const destination = target === 'exfil host' ? EXFIL_URL : `${externalServer.url}?d=secret`;
            const slug = kind.replace(/[^a-z]+/g, '-');

            const botPost = await postArtifact(page, mattermost, aimock, `navigator-${slug}.html`, selfNavigationHTML(kind, destination), `Here is the ${kind} navigator.`);
            await runArtifact(botPost);

            const card = botPost.getByTestId('html-artifact-card');
            const stopped = card.getByTestId('html-artifact-navigation-stopped');
            if (outcome === 'stopped') {
                await expect(stopped).toBeVisible({timeout: 30000});
                await expect(stopped).toHaveAttribute('data-stop-reason', 'navigation');
                await expect(stopped).toContainText('tried to navigate away');
            } else {
                await expect(card.getByText('This artifact could not be loaded.')).toBeVisible({timeout: 30000});
                await expect(stopped).toHaveCount(0);
            }
            await expect(artifactWrappers(card)).toHaveCount(0);

            // The host does not remount the frame (which would run the navigation again).
            await page.waitForTimeout(2000);
            expect(outbound).toEqual([]);
            expect(exfilRoutes).toEqual([]);
            expect(externalServer.requests.length).toBe(serverRequestsBefore);
            expect(contentLoads).toBe(1);
            await expect(page.getByTestId('html-artifact-consent')).toHaveCount(0);
        });
    }

    test('artifact code cannot read the bridge token or intercept its messages', async ({page}) => {
        test.setTimeout(300000);
        const botPost = await postArtifact(page, mattermost, aimock, TOKEN_PROBE_FILE_NAME, TOKEN_PROBE_HTML, 'Here is the probe.');
        const contentResponse = page.waitForResponse((r) => ARTIFACT_CONTENT_URL.test(r.url()), {timeout: 60000});
        await runArtifact(botPost);
        const iframe = artifactWrappers(botPost).first();
        const frame = artifactFrame(iframe);

        // The served artifact denies itself connections and powerful features.
        const headers = await (await contentResponse).allHeaders();
        expect(headers['connection-allowlist']).toBe('()');
        expect(headers['permissions-policy']).toContain('camera=()');
        expect(headers['content-security-policy']).toContain("default-src 'none'");

        // The host still talks to the genuine bridge: the frame becomes ready (visible).
        await expect(frame.getByRole('heading', {name: 'Token probe'})).toBeVisible({timeout: 30000});
        await expect(frame.getByTestId('probe-recorded')).not.toHaveText('', {timeout: 15000});

        // The token is bound to this viewer, file and frame load (nonce), so
        // asking for it with the frame's nonce returns the frame's token.
        const [, fileId, nonce] = ARTIFACT_CONTENT_URL.exec((await artifactContentIframe(iframe).getAttribute('src'))!)!;
        const tokenURL = `${mattermost.url()}/plugins/mattermost-ai/artifacts/${fileId}/token`;
        const res = await page.request.get(`${tokenURL}?n=${nonce}`, {headers: {'X-Requested-With': 'XMLHttpRequest'}});
        expect(res.ok()).toBe(true);
        const {token} = await res.json();
        expect(typeof token).toBe('string');
        expect(token.length).toBeGreaterThan(20);

        // Both routes require exactly one well-formed nonce.
        for (const query of ['', '?n=short', `?n=${nonce}&n=${nonce}`, `?n=${nonce}&x=1`]) {
            const tokenRes = await page.request.get(`${tokenURL}${query}`, {headers: {'X-Requested-With': 'XMLHttpRequest'}});
            expect(tokenRes.status(), `token${query}`).toBe(400);
        }

        // The shadowed window.parent only ever saw the artifact's own message.
        const recorded = await frame.getByTestId('probe-recorded').textContent();
        expect(JSON.parse(recorded!)).toEqual([{probe: 1}]);
        expect(recorded).not.toContain(token);

        const dom = await frame.getByTestId('probe-dom').textContent();
        expect(dom).toContain('Token probe');
        expect(dom).not.toContain(token);

        // Opening the artifact URL top-level (outside the host's frame), even
        // with the frame's own nonce, shows a static interstitial, never the
        // artifact itself.
        await page.goto(`${mattermost.url()}/plugins/mattermost-ai/artifacts/${fileId}?n=${nonce}`);
        await expect(page.getByText('This AI-generated artifact can only be viewed inside Mattermost.')).toBeVisible();
        await expect(page.getByText('Token probe')).toHaveCount(0);
        expect(await page.content()).not.toContain(token);
    });

    // A sandboxed frame can still call focus() on itself, and keystrokes then
    // go into the artifact. Running it gives it focus; once the viewer moves
    // on, one that takes focus back is unloaded at once, and focus goes back
    // to the page.
    test('an artifact that grabs keyboard focus is stopped and the message box keeps what is typed', async ({page}) => {
        test.setTimeout(300000);
        let exfiltrations = 0;
        await page.context().route((url) => url.hostname === EXFIL_HOST, (route) => {
            exfiltrations++;
            return route.fulfill({contentType: 'text/plain', body: ''});
        });

        const botPost = await postArtifact(page, mattermost, aimock, FOCUS_THIEF_FILE_NAME, FOCUS_THIEF_HTML, 'Here is the quarterly dashboard.');
        await runArtifact(botPost);
        const stopped = botPost.getByTestId('html-artifact-navigation-stopped');

        // The viewer ran it, so it may hold focus for now.
        await expect(artifactFrame(artifactWrappers(botPost).first()).getByRole('heading', {name: 'Quarterly dashboard'})).toBeVisible({timeout: 60000});
        await expect(botPost.getByTestId('html-artifact-frame')).toHaveAttribute('data-engaged', 'true');
        await page.waitForTimeout(1000);
        await expect(stopped).toHaveCount(0);

        const composer = new MattermostPage(page).postTextbox;
        await composer.click();
        await expect(stopped).toBeVisible({timeout: 15000});
        await expect(stopped).toHaveAttribute('data-stop-reason', 'focus');
        await expect(stopped).toContainText('tried to take keyboard focus');
        await expect(artifactWrappers(botPost)).toHaveCount(0);
        expect(await page.evaluate(() => document.activeElement?.tagName)).not.toBe('IFRAME');

        const typed = 'the merger with Acme closes Friday';
        await composer.click();
        await page.keyboard.type(typed, {delay: 40});
        await expect(composer).toHaveValue(typed);
        await page.waitForTimeout(1000);
        await expect(composer).toHaveValue(typed);
        await expect(stopped).toBeVisible();
        expect(exfiltrations).toBe(0);
    });

    // The click on Run is the viewer's first interaction with the artifact:
    // keys go to it straight away. Once focus moves back to the page, the
    // overlay returns with its hint showing, scrolling over it nudges the
    // hint, and a click hands the keyboard back to the artifact.
    test('Run hands the artifact the keyboard; after the viewer moves on, the hint nudges on scroll and a click re-engages', async ({page}) => {
        test.setTimeout(300000);
        const botPost = await postArtifact(page, mattermost, aimock, STANDUP_NOTES_FILE_NAME, STANDUP_NOTES_HTML, 'Here are the standup notes.');
        await runArtifact(botPost);
        const area = botPost.getByTestId('html-artifact-frame');
        const frame = artifactFrame(artifactWrappers(botPost).first());
        const typed = frame.getByTestId('typed');
        await expect(frame.getByRole('heading', {name: 'Standup notes'})).toBeVisible({timeout: 60000});
        await expect(area).toHaveAttribute('data-engaged', 'true');
        await expect(interactOverlay(botPost)).toHaveCount(0);
        await expect.poll(() => page.evaluate(() => document.activeElement?.getAttribute('data-testid'))).toBe('html-artifact-wrapper');

        await page.keyboard.type('Ship the hint', {delay: 30});
        await expect(typed).toHaveText('Ship the hint');
        await page.keyboard.press('Enter');
        await expect(frame.getByTestId('notes').locator('li').last()).toHaveText('Ship the hint');

        const composer = await focusMessageBox(page);
        await expect(area).toHaveAttribute('data-engaged', 'false');
        const overlay = interactOverlay(botPost);
        const hint = overlay.getByTestId('html-artifact-interact-hint');
        await expect(overlay).toBeVisible();
        await expect(hint).toBeVisible();
        await expect(hint).toHaveText('Click to interact');
        await expect(overlay).toHaveAttribute('data-nudge', 'false');

        // Keys typed now stay in the message box.
        await page.keyboard.type('draft', {delay: 30});
        await expect(composer).toHaveValue('draft');
        await expect(typed).toHaveText('');

        const box = (await overlay.boundingBox())!;
        await page.mouse.move(box.x + (box.width / 2), box.y + Math.min(box.height / 2, 60));
        await page.mouse.wheel(0, 120);
        await expect(overlay).toHaveAttribute('data-nudge', 'true');
        await expect(hint).toHaveText('Click to scroll and interact');
        await expect(overlay).toHaveAttribute('data-nudge', 'false', {timeout: 5000});
        await expect(hint).toHaveText('Click to interact');

        await overlay.click();
        await expect(area).toHaveAttribute('data-engaged', 'true');
        await expect(overlay).toHaveCount(0);
        await page.keyboard.type('Retro on Friday', {delay: 30});
        await expect(typed).toHaveText('Retro on Friday');
        await expect(composer).toHaveValue('draft');
        await composer.fill('');
    });

    // Loading an artifact runs agent-written code for whoever sees the post
    // (view tracking, prompt-injected exfiltration), so no viewer's page runs
    // it by itself, not even the requester's.
    test('every viewer, the requester included, sees Run and no frame until they run it', async ({page, browser}) => {
        test.setTimeout(300000);
        const prompt = `shared dashboard ${Date.now()}`;
        const finalText = `Here is the shared dashboard ${Date.now()}.`;
        await aimock.setFixtures(mergeFixtureFiles(
            {fixtures: [buildTitleFixture('Shared dashboard')]},
            buildCreateFileSequence({
                userPrompt: prompt,
                fileName: SPRINT_DASHBOARD_FILE_NAME,
                fileContent: SPRINT_DASHBOARD_HTML,
                finalText,
                toolCallId: `call_html_artifact_${Date.now()}`,
            }),
        ));

        // The requester asks in a channel the other viewer can read.
        const channelURL = `${mattermost.url()}/test/channels/off-topic`;
        const requesterArtifactRequests: string[] = [];
        page.on('request', (req) => {
            if (req.url().includes('/plugins/mattermost-ai/artifacts/')) {
                requesterArtifactRequests.push(req.url());
            }
        });
        const requester = new MattermostPage(page);
        await requester.login(mattermost.url(), username, password);
        await page.goto(channelURL);
        await requester.mentionBot(botName, prompt);

        const requesterPost = page.locator('#postListContent [data-testid="llm-bot-post"]').filter({hasText: finalText});
        await expect(requesterPost).toBeVisible({timeout: 120000});
        const requesterCard = requesterPost.getByTestId('html-artifact-card');
        const requesterPlaceholder = requesterCard.getByTestId('html-artifact-run-placeholder');
        await expect(requesterPlaceholder).toBeVisible({timeout: 60000});
        await expect(requesterPlaceholder).toContainText('runs code only when you choose');
        await expect(requesterCard.getByTestId('html-artifact-reload')).toHaveCount(0);

        // Still nothing a while later: the placeholder is not a loading state.
        await page.waitForTimeout(3000);
        await expect(requesterCard.getByTestId('html-artifact-frame')).toHaveCount(0);
        expect(requesterArtifactRequests).toEqual([]);

        await runArtifact(requesterPost);
        await expect(artifactFrame(artifactWrappers(requesterCard).first()).getByRole('heading', {name: 'Sprint 42 dashboard'})).toBeVisible({timeout: 60000});

        // One host load: a token and the document, under the same nonce.
        const tokenLoads = requesterArtifactRequests.map((u) => ARTIFACT_TOKEN_URL.exec(u)).filter((m) => m !== null);
        const contentLoads = requesterArtifactRequests.map((u) => ARTIFACT_CONTENT_URL.exec(u)).filter((m) => m !== null);
        expect(tokenLoads).toHaveLength(1);
        expect(contentLoads).toHaveLength(1);
        expect(contentLoads[0]![1]).toBe(tokenLoads[0]![1]);
        expect(contentLoads[0]![2]).toBe(tokenLoads[0]![2]);

        // The click on Run is the viewer's first interaction with it.
        await expect(requesterCard.getByTestId('html-artifact-frame')).toHaveAttribute('data-engaged', 'true');
        await expect(requesterCard.getByTestId('html-artifact-interact')).toHaveCount(0);

        const viewerContext = await browser.newContext({viewport: {width: 1440, height: 900}, permissions: ['notifications']});
        try {
            const viewerPage = await viewerContext.newPage();
            let artifactRequests = 0;
            viewerPage.on('request', (req) => {
                if (req.url().includes('/plugins/mattermost-ai/artifacts/')) {
                    artifactRequests++;
                }
            });
            await new MattermostPage(viewerPage).login(mattermost.url(), SECOND_VIEWER_USERNAME, SECOND_VIEWER_PASSWORD);
            await viewerPage.goto(channelURL);

            const viewerCard = viewerPage.locator('#postListContent [data-testid="llm-bot-post"]').filter({hasText: finalText}).getByTestId('html-artifact-card');
            const placeholder = viewerCard.getByTestId('html-artifact-run-placeholder');
            await expect(placeholder).toBeVisible({timeout: 60000});
            await expect(placeholder).toContainText('runs code only when you choose');
            await expect(viewerCard.getByTestId('html-artifact-file-name')).toHaveText(SPRINT_DASHBOARD_FILE_NAME);
            await expect(viewerCard.getByTestId('html-artifact-notice')).toBeVisible();
            await expect(viewerCard.getByTestId('html-artifact-reload')).toHaveCount(0);

            await viewerPage.waitForTimeout(3000);
            await expect(viewerCard.getByTestId('html-artifact-frame')).toHaveCount(0);
            expect(artifactRequests).toBe(0);

            // Opening fullscreen also runs it, inline as well, where the
            // viewer has not clicked it yet.
            await viewerCard.getByTestId('html-artifact-open-fullscreen').click();
            const viewer = viewerPage.getByTestId('html-artifact-fullscreen');
            await expect(viewer).toBeVisible({timeout: 15000});
            await expect(artifactFrame(artifactWrappers(viewer).first()).getByRole('heading', {name: 'Sprint 42 dashboard'})).toBeVisible({timeout: 30000});
            await expect(viewer.getByTestId('html-artifact-frame')).toHaveAttribute('data-engaged', 'true');
            await viewer.getByTestId('html-artifact-fullscreen-close').click();
            await expect(viewer).not.toBeVisible();

            const viewerFrame = artifactFrame(artifactWrappers(viewerCard).first());
            await expect(viewerFrame.getByRole('heading', {name: 'Sprint 42 dashboard'})).toBeVisible({timeout: 30000});
            await expect(placeholder).toHaveCount(0);
            await expect(viewerCard.getByTestId('html-artifact-frame')).toHaveAttribute('data-engaged', 'false');
            await engageArtifact(viewerCard);
            await viewerFrame.getByRole('button', {name: /Kudos/}).click();
            await expect(viewerFrame.locator('#kudos-count')).toHaveText('1');
        } finally {
            await viewerContext.close();
        }
    });

    test('inline card in a dark theme', async ({page}) => {
        test.setTimeout(300000);
        await setUserTheme(mattermost, username, password, ONYX_THEME);
        try {
            await askForArtifact(page, mattermost, aimock, `dark dashboard ${Date.now()}`);
            const {card, iframe} = await openArtifactInBotDM(page);
            const frame = artifactFrame(iframe);
            await expect(frame.getByRole('heading', {name: 'Sprint 42 dashboard'})).toBeVisible({timeout: 30000});
            // Theme variables reach the artifact via the context message.
            await expect.poll(async () => frame.locator('html').evaluate(
                (el) => getComputedStyle(el).getPropertyValue('--mm-center-channel-bg').trim().toLowerCase(),
            ), {timeout: 15000}).toBe(ONYX_THEME.centerChannelBg);
            await shot(page, '05-inline-card-dark');

            await card.getByTestId('html-artifact-open-fullscreen').click();
            const viewer = page.getByTestId('html-artifact-fullscreen');
            await expect(viewer).toBeVisible({timeout: 15000});
            const fullFrame = artifactFrame(artifactWrappers(viewer).first());
            await expect(fullFrame.getByTestId('fullscreen-details')).toBeVisible({timeout: 30000});
            await shot(page, '06-fullscreen-dark');
            await viewer.getByTestId('html-artifact-fullscreen-close').click();
            await expect(viewer).not.toBeVisible();
        } finally {
            await setUserTheme(mattermost, username, password, null);
        }
    });
});
