// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Helpers for the HTML artifacts e2e spec: feature-flag toggling, a
// deterministic self-contained artifact document, and theme preferences.

import type {FrameLocator, Locator, Page} from '@playwright/test';

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

/** Inline artifact iframe(s): anything pointing at the plugin's artifact route. */
export function artifactIframes(scope: Page | Locator): Locator {
    return scope.locator('iframe[src*="/plugins/mattermost-ai/artifacts/"]');
}

export function artifactFrame(iframe: Locator): FrameLocator {
    return iframe.contentFrame();
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
