import {describe, it} from 'node:test';

import tsParser from '@typescript-eslint/parser';
import {RuleTester} from 'eslint';

import plugin from './index.mjs';

RuleTester.describe = describe;
RuleTester.it = it;
RuleTester.itOnly = it.only;

const tester = new RuleTester({languageOptions: {parser: tsParser}});

tester.run('scoped-tool-label', plugin.rules['scoped-tool-label'], {
    valid: [
        'await expect(toolCard(post, createPostLabel)).toBeVisible();',
        'await expect(post.locator(TOOL_CARD_SELECTOR).getByText(createPostLabel, {exact: true})).toBeVisible();',
        'const card = post.locator(TOOL_CARD_SELECTOR).first(); await card.getByText(createPostLabel).click();',
        'const card = post.locator(TOOL_CARD_SELECTOR) as Locator; await card.getByText(createPostLabel).click();',
        'await toolCard(rounds, readPostLabel).getByText(readPostLabel, {exact: true}).click();',
        'await expect(post.getByText(searchToolsLabel, {exact: true})).toHaveCount(0);',
        'await expect(post.getByText(continuationMarker)).toBeVisible();',
        'await expect(rhs.getByRole("button", {name: /accept/i})).toBeVisible();',
    ],
    invalid: [
        {code: 'await expect(post.getByText(createPostLabel, {exact: true})).toBeVisible();', errors: [{messageId: 'unscoped'}]},
        {code: 'const rounds = await expandToolActivity(post); await rounds.getByText(readPostLabel).click();', errors: [{messageId: 'unscoped'}]},
        {code: 'await expect(post.getByRole("button", {name: createPostToolLabel})).toBeVisible();', errors: [{messageId: 'unscoped'}]},
        {code: 'await expect(post.locator("div").filter({hasText: getChannelInfoLabel})).toBeVisible();', errors: [{messageId: 'unscoped'}]},
        {code: 'await expect(post.getByText(loadToolLabel)).toHaveCount(1);', errors: [{messageId: 'unscoped'}]},
    ],
});

tester.run('container-exec-exit-code', plugin.rules['container-exec-exit-code'], {
    valid: [
        'const {output, exitCode} = await this.container.exec(["mmctl", "--local", "system", "version"]); if (exitCode !== 0) { throw new Error(output); }',
        'const {exitCode: code} = await mattermost.container.exec(["true"]); expect(code).toBe(0);',
        'if ((await container.exec(["true"])).exitCode !== 0) { throw new Error("failed"); }',
        'const [, id] = ARTIFACT_URL.exec(src);',
        'const {output, exitCode} = await withDeadline("mmctl", 60000, () => this.container.exec(["mmctl"])); if (exitCode !== 0) { throw new Error(output); }',
    ],
    invalid: [
        {code: 'await mattermost.container.exec(["mmctl", "--local", "config", "set", "A", "b"]);', errors: [{messageId: 'unchecked'}]},
        {code: 'const {output} = await this.container.exec(["mmctl", "--local", "logs"]);', errors: [{messageId: 'unchecked'}]},
        {code: 'const result = await pgContainer.exec(["psql"]);', errors: [{messageId: 'unchecked'}]},
        {code: 'const {exitCode} = await mattermost.container.exec(["true"]);', errors: [{messageId: 'unchecked'}]},
        {code: 'if (await container.exec(["true"]).exitCode !== 0) { throw new Error("failed"); }', errors: [{messageId: 'unchecked'}]},
        {code: 'const {exitCode} = container.exec(["true"]); if (exitCode !== 0) { throw new Error("failed"); }', errors: [{messageId: 'unchecked'}]},
        {code: 'await withDeadline("mmctl", 60000, () => this.container.exec(["mmctl"]));', errors: [{messageId: 'unchecked'}]},
        {code: 'const {output} = await withDeadline("mmctl", 60000, () => this.container.exec(["mmctl"]));', errors: [{messageId: 'unchecked'}]},
    ],
});

tester.run('container-start-in-fixture', plugin.rules['container-start-in-fixture'], {
    valid: [
        'const test = containerTest(async (own) => ({mattermost: own(await RunContainer())}));',
        'const stackTest = (fixtures) => containerTest(async (own) => { const mattermost = own(await RunContainer()); return {mattermost, aimock: own(await RunAIMockSidecar(mattermost.network, fixtures))}; });',
        'async function setupMattermost() { return RunSystemConsoleContainer({}); }',
        'test.beforeAll(async ({containers}) => { await startExternalServer(containers.mattermost.url()); });',
    ],
    invalid: [
        {code: 'test.beforeAll(async () => { mattermost = await RunContainer(); });', errors: [{messageId: 'inHook'}]},
        {code: 'test("x", async ({page}) => { const mm = await RunSystemConsoleContainer({}); });', errors: [{messageId: 'inHook'}]},
        {code: 'fooTest.skip("x", async () => { await RunOpenAIMocks(network); });', errors: [{messageId: 'inHook'}]},
        {code: 'test.describe("x", () => { test.beforeAll(async () => { await Promise.all([1].map(() => startAIMockCitationStack())); }); });', errors: [{messageId: 'inHook'}]},
    ],
});
