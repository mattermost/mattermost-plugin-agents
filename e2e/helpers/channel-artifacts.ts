// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Helpers for the channel artifacts e2e spec: feature-flag toggling, channel
// admin setup, the Channel artifacts RHS, direct access to the library API,
// and a small self-contained artifact document.

import {expect, type Locator, type Page} from '@playwright/test';

import MattermostContainer from './mmcontainer';
import {MATTERMOST_AI_PLUGIN_ID, mattermostAIPluginRoutes, pluginAdminConfigApiFromClient} from './plugin-http';

/**
 * Flips the channel artifacts library on or off. The library needs HTML
 * artifacts as well (`EnableChannelArtifacts()` is the conjunction of both
 * flags on the server), so both top-level fields are set together.
 */
export async function setChannelArtifactsEnabled(mattermost: MattermostContainer, enabled: boolean): Promise<void> {
    const adminClient = await mattermost.getAdminClient();
    const api = pluginAdminConfigApiFromClient(adminClient, mattermost.url(), MATTERMOST_AI_PLUGIN_ID);
    const current = await api.get();
    await api.put({...current, enableHTMLArtifacts: enabled, enableChannelArtifacts: enabled}, {settleMs: 2000});
}

/** Makes `username` a channel admin of `channelName` on `teamName`, and returns the channel id. */
export async function makeChannelAdmin(mattermost: MattermostContainer, username: string, teamName: string, channelName: string): Promise<string> {
    const admin = await mattermost.getAdminClient();
    const team = await admin.getTeamByName(teamName);
    const channel = await admin.getChannelByName(team.id, channelName);
    const user = await admin.getUserByUsername(username);
    await admin.updateChannelMemberSchemeRoles(channel.id, user.id, true, true);
    return channel.id;
}

/** Matches the library artifact content route for one host load: `.../channel-artifacts/<id>?n=<nonce>`. */
export const CHANNEL_ARTIFACT_CONTENT_URL = /\/plugins\/mattermost-ai\/channel-artifacts\/([a-z0-9]{26})\?n=([A-Za-z0-9_-]{22})$/;

/** Matches the library bridge token route for one host load: `.../channel-artifacts/<id>/token?n=<nonce>`. */
export const CHANNEL_ARTIFACT_TOKEN_URL = /\/plugins\/mattermost-ai\/channel-artifacts\/([a-z0-9]{26})\/token\?n=([A-Za-z0-9_-]{22})$/;

/** Matches the create route, whose response carries the new entry. */
export const CHANNEL_ARTIFACTS_CREATE_URL = /\/plugins\/mattermost-ai\/channel-artifacts$/;

export type ChannelArtifactEntry = {
    id: string;
    channel_id: string;
    title: string;
    description?: string;
    source_post_id: string;
    source_file_id: string;
    source_file_name: string;
    source_agent_display_name?: string;
    create_at: number;
    update_at: number;
    version: number;
};

export type ChannelArtifactList = {
    entries: ChannelArtifactEntry[];
    can_manage: boolean;
};

/** The channel artifacts API as `username` sees it, over a bearer token (no CSRF header needed). */
export async function channelArtifactsApi(mattermost: MattermostContainer, username: string, password: string) {
    const client = await mattermost.getClient(username, password);
    const token = client.getToken();
    const routes = mattermostAIPluginRoutes(mattermost.url());
    return {
        list: (channelId: string) => routes.getJson(`channel-artifacts?channel_id=${encodeURIComponent(channelId)}`, token) as Promise<ChannelArtifactList>,
        patch: (id: string, body: {title?: string; description?: string}) => routes.request('PATCH', `channel-artifacts/${encodeURIComponent(id)}`, token, body),
        remove: (id: string) => routes.request('DELETE', `channel-artifacts/${encodeURIComponent(id)}`, token),
        request: (method: string, path: string, body?: unknown) => routes.request(method, path, token, body),
    };
}

/** The Channel artifacts RHS in `page`. */
export function channelArtifactsRHS(page: Page): Locator {
    return page.getByTestId('channel-artifacts-rhs');
}

/**
 * Opens the Channel artifacts RHS for the current channel from the channel
 * header menu (plugin actions live under "More actions"). Bot DMs do not
 * render that menu — call this from a public/private channel.
 */
export async function openChannelArtifactsRHS(page: Page): Promise<Locator> {
    const rhs = channelArtifactsRHS(page);
    if (await rhs.isVisible()) {
        return rhs;
    }
    // Bot DM headers reuse this id as a non-interactive div (no menu).
    const menuButton = page.locator('button#channelHeaderDropdownButton');
    await expect(menuButton).toBeVisible({timeout: 15000});
    await menuButton.click();
    const item = page.getByRole('menuitem', {name: 'Channel artifacts'});
    const moreActions = page.locator('#moreActions');
    if (await moreActions.isVisible().catch(() => false)) {
        await moreActions.hover();
        if (!(await item.isVisible().catch(() => false))) {
            await moreActions.click();
        }
    }
    await expect(item).toBeVisible({timeout: 10000});
    await item.click();
    await expect(rhs).toBeVisible({timeout: 15000});
    return rhs;
}

/**
 * Opens Channel artifacts from the Channel Info RHS menu (requires a Mattermost
 * build that exposes registerChannelInfoMenuAction).
 */
export async function openChannelArtifactsFromInfo(page: Page): Promise<Locator> {
    const rhs = channelArtifactsRHS(page);
    if (await rhs.isVisible()) {
        return rhs;
    }
    const infoButton = page.locator('#channel-info-btn');
    await expect(infoButton).toBeVisible({timeout: 15000});
    await infoButton.click();
    await expect(page.getByTestId('channel_info_rhs-menu')).toBeVisible({timeout: 15000});
    const item = page.getByRole('button', {name: 'Channel artifacts'});
    await expect(item).toBeVisible({timeout: 10000});
    await item.click();
    await expect(rhs).toBeVisible({timeout: 15000});
    return rhs;
}

/** The saved entry card for `id` inside the RHS. */
export function channelArtifactCard(rhs: Locator, id: string): Locator {
    return rhs.getByTestId(`channel-artifact-${id}`);
}

/** The RHS's empty state, shown once the channel has no saved artifact. */
export function channelArtifactsEmptyState(rhs: Locator): Locator {
    return rhs.getByTestId('channel-artifacts-empty');
}

/**
 * Opens the Save to channel artifacts modal from the card in `scope`, optionally
 * sets a title or picks an existing entry to update, confirms, and returns the
 * entry the server wrote. A replace keeps the same id and bumps `version`.
 */
export async function saveArtifactToChannel(
    page: Page,
    scope: Locator,
    options?: {title?: string; replaceId?: string},
): Promise<ChannelArtifactEntry> {
    const card = scope.getByTestId('html-artifact-card').first();
    const save = card.getByTestId('html-artifact-save-to-channel');
    await expect(save).toBeVisible();
    await save.click();

    const modal = page.getByTestId('channel-artifact-save-modal');
    await expect(modal).toBeVisible();
    if (options?.title !== undefined) {
        await modal.getByTestId('channel-artifact-title-input').fill(options.title);
    }
    if (options?.replaceId) {
        await modal.getByTestId('channel-artifact-replace-select').selectOption(options.replaceId);
    } else if (options?.title !== undefined) {
        // Opening for a new file may auto-suggest an existing title match — force create.
        const replace = modal.getByTestId('channel-artifact-replace-select');
        if (await replace.isVisible().catch(() => false)) {
            await replace.selectOption('');
            await modal.getByTestId('channel-artifact-title-input').fill(options.title);
        }
    }

    const expectedStatus = options?.replaceId ? 200 : 201;
    const created = page.waitForResponse(
        (r) => r.request().method() === 'POST' && CHANNEL_ARTIFACTS_CREATE_URL.test(r.url()),
        {timeout: 30000},
    );
    await modal.getByTestId('channel-artifact-save-confirm').click();
    const response = await created;
    expect(response.status(), 'POST /channel-artifacts').toBe(expectedStatus);
    await expect(modal).toHaveCount(0);
    await expect(save).toHaveAttribute('aria-label', /channel artifact/i);
    return (await response.json()) as ChannelArtifactEntry;
}

/** Pins a library artifact from Bookmarks → Add channel artifact (header or bar + menu). */
export async function pinArtifactFromBookmarksMenu(page: Page, entry: {id: string; title: string}): Promise<void> {
    // Prefer the bar + control when the bookmarks bar is already visible.
    const barPlus = page.locator('#channelBookmarksPlusMenuButton, .channelBookmarksBarMenuButton').first();
    if (await barPlus.isVisible().catch(() => false)) {
        await barPlus.click();
    } else {
        const menuButton = page.locator('button#channelHeaderDropdownButton');
        await expect(menuButton).toBeVisible({timeout: 15000});
        await menuButton.click();
        const bookmarksSub = page.getByRole('menuitem', {name: 'Bookmarks Bar'});
        await expect(bookmarksSub).toBeVisible({timeout: 10000});
        await bookmarksSub.hover();
        if (!(await page.getByRole('menuitem', {name: 'Add channel artifact'}).isVisible().catch(() => false))) {
            await bookmarksSub.click();
        }
    }
    const addArtifact = page.getByRole('menuitem', {name: 'Add channel artifact'});
    await expect(addArtifact).toBeVisible({timeout: 10000});
    await addArtifact.click();

    const picker = page.getByTestId('channel-artifact-bookmark-picker');
    await expect(picker).toBeVisible({timeout: 15000});
    const bookmarkCreated = page.waitForResponse(
        (r) => r.request().method() === 'POST' && /\/api\/v4\/channels\/[^/]+\/bookmarks$/.test(r.url()),
        {timeout: 30000},
    );
    await picker.getByTestId(`channel-artifact-bookmark-pick-${entry.id}`).click();
    expect((await bookmarkCreated).status(), 'POST /bookmarks').toBe(201);
    await expect(picker).toHaveCount(0);
}

/** Accepts the next native confirm() dialog (used by the RHS delete action). Register before the click. */
export function acceptNextConfirm(page: Page): void {
    page.once('dialog', (dialog) => dialog.accept());
}

export const RELEASE_NOTES_FILE_NAME = 'release-notes.html';
export const RELEASE_NOTES_TITLE = 'release-notes';

/**
 * A small, self-contained "Release notes" page: a heading, a version badge
 * read by the tests, and a toggle that proves scripts run in the library
 * frame too.
 */
export function releaseNotesHTML(version: string): string {
    return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Release notes</title>
<style>
  :root {
    --bg: var(--mm-center-channel-bg, #ffffff);
    --fg: var(--mm-center-channel-color, #3f4350);
    --accent: var(--mm-button-bg, #1c58d9);
    --accent-fg: var(--mm-button-color, #ffffff);
    --line: color-mix(in srgb, var(--fg) 14%, transparent);
    --tint: color-mix(in srgb, var(--fg) 4%, var(--bg));
  }
  * { box-sizing: border-box; }
  html, body { margin: 0; background: var(--bg); color: var(--fg);
    font: 14px/1.45 "Open Sans", system-ui, -apple-system, "Segoe UI", sans-serif; }
  main { padding: 20px 22px 22px; }
  header { display: flex; align-items: center; gap: 10px; }
  h1 { margin: 0; font-size: 18px; font-weight: 700; }
  .badge { font-size: 11px; border-radius: 10px; padding: 1px 8px; background: var(--accent); color: var(--accent-fg); }
  ul { margin: 12px 0 0; padding: 0; list-style: none; }
  li { padding: 7px 0; border-bottom: 1px solid var(--line); }
  .btn { margin-top: 14px; background: var(--tint); color: var(--fg); border: 1px solid var(--line); border-radius: 4px;
    padding: 6px 12px; font: inherit; font-weight: 600; cursor: pointer; }
</style>
</head>
<body>
<main>
  <header>
    <h1>Release notes</h1>
    <span class="badge" data-testid="version">${version}</span>
  </header>
  <ul>
    <li>Channel artifacts: save an agent-built page to the channel library</li>
    <li>Channel admins can rename and remove saved artifacts</li>
    <li>Everyone in the channel can open them from the Channel artifacts panel</li>
  </ul>
  <button class="btn" id="toggle">Show details</button>
  <p id="details" data-testid="details" hidden>Built by the Artifact Bot for ${version}.</p>
</main>
<script>
  var details = document.getElementById('details');
  document.getElementById('toggle').addEventListener('click', function () {
    details.hidden = !details.hidden;
  });
</script>
</body>
</html>
`;
}
