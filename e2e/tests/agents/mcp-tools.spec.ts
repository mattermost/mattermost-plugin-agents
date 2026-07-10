import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import {
    OpenAIMockContainer,
    RunOpenAIMocks,
    buildTextResponse,
    buildChatCompletionMockRule,
    buildTitleMockRule,
    buildToolCallResponse,
} from 'helpers/openai-mock';
import type {OpenAIChatCompletionRequest, OpenAIChatMessage} from 'helpers/openai-mock';
import {
    RunAgentContainer,
    agentAdminUsername, agentAdminPassword,
    agentRegularUsername, agentRegularPassword,
    mockServiceId,
} from 'helpers/agent-container';
import { AgentAPIHelper } from 'helpers/agent-api';
import { AgentPageHelper } from 'helpers/agent-page';
import { AIPlugin } from 'helpers/ai-plugin';

/** Matches mcp.EmbeddedClientKey — MCP server origin for the embedded Mattermost tools server. */
const embeddedMattermostOrigin = 'embedded://mattermost';

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

function isChatCompletionRequest(value: unknown): value is OpenAIChatCompletionRequest {
    if (typeof value !== 'object' || value === null || Array.isArray(value)) {
        return false;
    }

    const messages = (value as Record<string, unknown>).messages;
    return messages === undefined || Array.isArray(messages);
}

function hasExactUserMessage(request: OpenAIChatCompletionRequest, message: string): boolean {
    return request.messages?.some((candidate) => (
        candidate.role === 'user' && candidate.content === message
    )) ?? false;
}

function messageText(message: OpenAIChatMessage): string {
    if (typeof message.content === 'string') {
        return message.content;
    }
    if (Array.isArray(message.content)) {
        return message.content.map((part) => part.text ?? '').join('');
    }
    return '';
}

function toolResultMessage(
    request: OpenAIChatCompletionRequest,
    toolCallId: string,
): OpenAIChatMessage | undefined {
    return request.messages?.find((message) => (
        message.role === 'tool' && message.tool_call_id === toolCallId
    ));
}

function providerToolNames(request: OpenAIChatCompletionRequest): string[] {
    if (!Array.isArray(request.tools)) {
        return [];
    }

    return request.tools.flatMap((tool) => {
        if (typeof tool !== 'object' || tool === null || Array.isArray(tool)) {
            return [];
        }

        const definition = (tool as Record<string, unknown>).function;
        if (typeof definition !== 'object' || definition === null || Array.isArray(definition)) {
            return [];
        }

        const name = (definition as Record<string, unknown>).name;
        return typeof name === 'string' ? [name] : [];
    });
}

test.describe('Agent MCP Tools', () => {
    test.beforeAll(async () => {
        mattermost = await RunAgentContainer();
        openAIMock = await RunOpenAIMocks(mattermost.network);
    }, { timeout: 180000 });

    test.afterAll(async () => {
        await openAIMock?.stop();
        await mattermost?.stop();
    });

    test('agent with no enabledMCPTools gets no MCP tools', async ({ page }) => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const token = adminClient.getToken();

        const noToolsAgent = await agentApi.createTestAgent(token, {
            displayName: 'No Tools Agent',
            username: 'notoolsagent',
            serviceID: mockServiceId,
            autoEnableNewMCPTools: false,
            enabledMCPTools: [],
            enabledNativeTools: [],
        });

        // Prove enabledMCPTools=[] reaches the LLM without read_post in the completion payload: first rule
        // would match if "read_post" were sent; second rule is the catch-all success response.
        await openAIMock.addMocks([
            buildChatCompletionMockRule(
                buildTextResponse('WRONG: read_post in completion request when enabledMCPTools is empty'),
                { bodyContains: 'read_post' },
            ),
            buildChatCompletionMockRule(buildTextResponse('I have no tools available.')),
        ]);

        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);
        await mmPage.login(mattermost.url(), agentRegularUsername, agentRegularPassword);
        await page.goto(`${mattermost.url()}/test/channels/town-square`);
        await page.getByTestId('channel_view').waitFor({ state: 'visible', timeout: 60000 });

        await aiPlugin.openRHS();
        await aiPlugin.switchBotWhenListed(noToolsAgent.displayName);
        await aiPlugin.sendMessage('Hello');
        await aiPlugin.waitForBotResponse('I have no tools available.');
    });

    test('MCPs tab shows embedded server and tool affordances in config view', async ({ page }) => {
        test.setTimeout(60000);
        const mmPage = new MattermostPage(page);
        const agentPage = new AgentPageHelper(page);

        await mmPage.login(mattermost.url(), agentAdminUsername, agentAdminPassword);
        await agentPage.navigateToAgents(mattermost.url());

        await agentPage.getCreateButton().click();
        await agentPage.waitForModal();

        await agentPage.getModalTab('MCPs').click();

        await expect(agentPage.getMCPSearchInput()).toBeVisible({ timeout: 15000 });
        // Provider name from GET /mcp/tools (mcp.EmbeddedServerName)
        await expect(page.getByText('Mattermost', {exact: true})).toBeVisible({ timeout: 15000 });
    });

    // MM-69185 regression: visiting the MCPs tab on an agent whose persisted
    // enabledMCPTools list contains entries that are no longer present in the
    // live MCP catalog (orphans) used to silently mark the form dirty. Clicking
    // Cancel then incorrectly raised the "Discard changes?" confirmation modal.
    test('Cancel on MCPs tab with orphaned saved tools does not prompt to discard (MM-69185)', async ({ page }) => {
        test.setTimeout(60000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const token = adminClient.getToken();

        const orphanAgent = await agentApi.createTestAgent(token, {
            displayName: 'Orphan Tool Agent',
            username: 'orphantoolagent',
            serviceID: mockServiceId,
            autoEnableNewMCPTools: false,
            // The "ghost_tool" name does not exist on the embedded MCP server, so
            // the live MCP catalog will report it as an orphan when the editor
            // mounts the MCPs tab.
            enabledMCPTools: [
                { server_origin: embeddedMattermostOrigin, tool_name: 'ghost_tool' },
            ],
            enabledNativeTools: [],
        });

        const mmPage = new MattermostPage(page);
        const agentPage = new AgentPageHelper(page);

        await mmPage.login(mattermost.url(), agentAdminUsername, agentAdminPassword);
        await agentPage.navigateToAgents(mattermost.url());
        await agentPage.openAgentActions(orphanAgent.displayName);
        await agentPage.clickEditAction(orphanAgent.displayName);
        await agentPage.waitForModal();

        await agentPage.getModalTab('MCPs').click();

        // Wait for the live MCP catalog to render — at this point orphan
        // reconciliation has run.
        await expect(page.getByText('Mattermost', { exact: true })).toBeVisible({ timeout: 15000 });

        await agentPage.getModalCancelButton().click();

        // The Cancel must exit immediately without the Discard confirmation modal,
        // because the user did not make any edits.
        await expect(agentPage.getDiscardChangesDialog()).not.toBeVisible();
        await agentPage.waitForModalClosed();
    });

    test('persists a specific MCP tool from the agent builder and exposes only that tool at runtime', async ({ page }) => {
        test.setTimeout(180000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const token = adminClient.getToken();

        const displayName = 'Builder MCP Persistence Agent';
        const username = 'buildermcppersistence';
        const selectedTool = 'read_post';
        const comparisonTool = 'get_channel_info';
        const customInstructions = 'Preserve this instruction while MCP settings change.';
        const model = 'gpt-mock';
        const maxToolTurns = 17;
        const enabledMCPTools = [
            {server_origin: embeddedMattermostOrigin, tool_name: selectedTool},
        ];
        const selectiveAgent = await agentApi.createTestAgent(token, {
            displayName,
            username,
            serviceID: mockServiceId,
            customInstructions,
            enabledMCPTools: [],
            autoEnableNewMCPTools: true,
            mcpDynamicToolLoading: true,
            enabledNativeTools: [],
            model,
            enableVision: false,
            disableTools: false,
            reasoningEnabled: false,
            reasoningEffort: 'low',
            thinkingBudget: 321,
            structuredOutputEnabled: false,
            maxToolTurns,
        });
        const baseline = await agentApi.getAgent(token, selectiveAgent.id);
        expect(baseline.autoEnableNewMCPTools).toBe(true);
        expect(baseline.mcpDynamicToolLoading).toBe(true);
        expect(baseline.enabledMCPTools ?? []).toEqual([]);

        const runtimeMarker = Date.now().toString(36);
        const teams = await adminClient.getMyTeams();
        const defaultTeam = teams[0];
        const channels = await adminClient.getMyChannels(defaultTeam.id);
        const townSquare = channels.find((channel) => channel.name === 'town-square');
        if (!townSquare) {
            throw new Error('town-square channel not found');
        }
        const seededPostText = `Builder MCP resolver marker ${runtimeMarker}`;
        const seededPost = await adminClient.createPost({
            channel_id: townSquare.id,
            message: seededPostText,
        });
        const toolCallId = `call_builder_read_post_${runtimeMarker}`;
        const providerSelectedTool = `mattermost__${selectedTool}`;
        const toolPrompt =
            `Use ${providerSelectedTool} to read post ${seededPost.id}. ` +
            `The post contains marker ${runtimeMarker}. Call the tool now.`;
        const runtimeResponse = `Builder MCP tool continuation ${runtimeMarker}.`;

        const mmPage = new MattermostPage(page);
        const agentPage = new AgentPageHelper(page);
        await mmPage.login(mattermost.url(), agentAdminUsername, agentAdminPassword);
        await agentPage.navigateToAgents(mattermost.url());
        await agentPage.openAgentActions(displayName);
        await agentPage.clickEditAction(displayName);
        await agentPage.waitForModal();
        await agentPage.getModalTab('MCPs').click();

        const dynamicToolLoading = agentPage.getMCPDynamicToolLoadingCheckbox();
        const autoEnableAll = agentPage.getMCPAutoEnableAllCheckbox();
        await expect(dynamicToolLoading).toBeChecked();
        await expect(autoEnableAll).toBeChecked();
        await dynamicToolLoading.uncheck();
        await autoEnableAll.uncheck();
        await expect(dynamicToolLoading).not.toBeChecked();
        await expect(autoEnableAll).not.toBeChecked();

        const serverHeader = agentPage.getMCPServerHeader('Mattermost');
        const serverAllToolsToggle = agentPage.getMCPServerAllToolsToggle('Mattermost');
        await expect(serverHeader).toHaveAttribute(
            'aria-label',
            /^Mattermost, \d+ tools available\. Press to expand or collapse tools\.$/,
        );
        await expect(serverHeader).toHaveAttribute('aria-expanded', 'false');
        await expect(serverAllToolsToggle).toHaveAttribute('aria-checked', 'false');
        await serverHeader.click();
        await expect(serverHeader).toHaveAttribute('aria-expanded', 'true');
        await expect(agentPage.getMCPServerToolsRegion('Mattermost')).toBeVisible();

        const selectedToolToggle = agentPage.getMCPToolToggle('Mattermost', selectedTool);
        const comparisonToolToggle = agentPage.getMCPToolToggle('Mattermost', comparisonTool);
        await expect(selectedToolToggle).toHaveAccessibleName(`Enable tool ${selectedTool} on Mattermost`);
        await expect(comparisonToolToggle).toHaveAccessibleName(`Enable tool ${comparisonTool} on Mattermost`);
        await selectedToolToggle.click();
        await expect(selectedToolToggle).toHaveAccessibleName(`Disable tool ${selectedTool} on Mattermost`);
        await expect(comparisonToolToggle).toHaveAccessibleName(`Enable tool ${comparisonTool} on Mattermost`);
        await expect(serverHeader).toHaveAttribute(
            'aria-label',
            /^Mattermost, 1 of \d+ tools enabled\. Press to expand or collapse tools\.$/,
        );
        await expect(serverAllToolsToggle).toHaveAttribute('aria-checked', 'false');

        const updateResponse = page.waitForResponse((response) => {
            const url = new URL(response.url());
            return url.pathname === `/plugins/mattermost-ai/agents/${selectiveAgent.id}` &&
                response.request().method() === 'PUT';
        });
        await agentPage.getModalSaveButton().click();
        expect((await updateResponse).status()).toBe(200);
        await agentPage.waitForModalClosed();

        const saved = await agentApi.getAgent(token, selectiveAgent.id);
        expect({
            enabledMCPTools: saved.enabledMCPTools,
            autoEnableNewMCPTools: saved.autoEnableNewMCPTools,
            mcpDynamicToolLoading: saved.mcpDynamicToolLoading,
        }).toEqual({
            enabledMCPTools,
            autoEnableNewMCPTools: false,
            mcpDynamicToolLoading: false,
        });
        expect(saved).toMatchObject({
            customInstructions,
            maxToolTurns,
            model,
            enableVision: false,
            disableTools: false,
            reasoningEnabled: false,
            reasoningEffort: 'low',
            thinkingBudget: 321,
            structuredOutputEnabled: false,
            serviceID: mockServiceId,
        });
        expect(saved.enabledNativeTools ?? []).toEqual([]);
        expect(saved.updateAt).toEqual(expect.any(Number));
        expect(saved.updateAt).toBeGreaterThan(baseline.updateAt ?? 0);

        await page.reload();
        await agentPage.waitForAgentsLoaded();
        await agentPage.openAgentActions(displayName);
        await agentPage.clickEditAction(displayName);
        await agentPage.waitForModal();
        await agentPage.getModalTab('MCPs').click();
        await expect(agentPage.getMCPDynamicToolLoadingCheckbox()).not.toBeChecked();
        await expect(agentPage.getMCPAutoEnableAllCheckbox()).not.toBeChecked();
        await expect(agentPage.getMCPServerHeader('Mattermost')).toHaveAttribute(
            'aria-label',
            /^Mattermost, 1 of \d+ tools enabled\. Press to expand or collapse tools\.$/,
        );
        await expect(agentPage.getMCPServerHeader('Mattermost')).toHaveAttribute('aria-expanded', 'false');
        await agentPage.getMCPServerHeader('Mattermost').click();
        await expect(agentPage.getMCPServerHeader('Mattermost')).toHaveAttribute('aria-expanded', 'true');
        await expect(agentPage.getMCPServerToolsRegion('Mattermost')).toBeVisible();
        await expect(agentPage.getMCPToolToggle('Mattermost', selectedTool)).
            toHaveAccessibleName(`Disable tool ${selectedTool} on Mattermost`);
        await expect(agentPage.getMCPToolToggle('Mattermost', comparisonTool)).
            toHaveAccessibleName(`Enable tool ${comparisonTool} on Mattermost`);
        await expect(agentPage.getMCPServerAllToolsToggle('Mattermost')).toHaveAttribute('aria-checked', 'false');
        await agentPage.getModalCancelButton().click();
        await agentPage.waitForModalClosed();

        await openAIMock.addMocks([
            // Smocker gives the last matching rule priority. Register the prompt-only main rule
            // first, the tool-result continuation second, and the title-specific rule last.
            buildChatCompletionMockRule(
                buildToolCallResponse(
                    toolCallId,
                    providerSelectedTool,
                    JSON.stringify({post_id: seededPost.id}),
                ),
                {
                    bodyContains: toolPrompt,
                    times: 1,
                },
            ),
            buildChatCompletionMockRule(buildTextResponse(runtimeResponse), {
                bodyMatches: `(?s)${toolCallId}.*${seededPostText}`,
                times: 1,
            }),
            buildTitleMockRule(`Builder MCP title ${runtimeMarker}`, toolPrompt),
        ]);

        const aiPlugin = new AIPlugin(page);
        await page.context().clearCookies();
        await mmPage.login(mattermost.url(), agentRegularUsername, agentRegularPassword);
        await page.goto(`${mattermost.url()}/test/channels/town-square`);
        await page.getByTestId('channel_view').waitFor({ state: 'visible', timeout: 60000 });

        await aiPlugin.openRHS();
        await aiPlugin.switchBotWhenListed(selectiveAgent.displayName);
        await aiPlugin.sendMessage(toolPrompt);
        await aiPlugin.waitForBotResponse(runtimeResponse);

        let runtimeProviderRequests: OpenAIChatCompletionRequest[] = [];
        await expect.poll(async () => {
            runtimeProviderRequests = (await openAIMock.getHistory()).flatMap((entry) => {
                if (!isChatCompletionRequest(entry.request.body)) {
                    return [];
                }
                if (!hasExactUserMessage(entry.request.body, toolPrompt)) {
                    return [];
                }
                return [entry.request.body];
            });
            return runtimeProviderRequests.length;
        }, {
            message: 'provider did not receive the initial and tool-result requests for the builder-configured agent',
            timeout: 15000,
        }).toBe(2);

        const initialProviderRequests = runtimeProviderRequests.filter(
            (request) => !toolResultMessage(request, toolCallId),
        );
        const followUpProviderRequests = runtimeProviderRequests.filter(
            (request) => toolResultMessage(request, toolCallId),
        );
        expect(initialProviderRequests).toHaveLength(1);
        expect(followUpProviderRequests).toHaveLength(1);

        const exposedToolNames = providerToolNames(initialProviderRequests[0]);
        expect(exposedToolNames.filter((name) => name.startsWith('mattermost__'))).
            toEqual([providerSelectedTool]);
        expect(exposedToolNames).not.toContain(`mattermost__${comparisonTool}`);
        expect(exposedToolNames).not.toContain('search_tools');
        expect(exposedToolNames).not.toContain('load_tool');

        const actualToolResult = toolResultMessage(followUpProviderRequests[0], toolCallId);
        if (!actualToolResult) {
            throw new Error(`provider follow-up did not include tool result ${toolCallId}`);
        }
        expect(messageText(actualToolResult)).toContain(seededPost.id);
        expect(messageText(actualToolResult)).toContain(seededPostText);
    });

    // RHS Tool Providers popover filters by server (provider): when the active bot has
    // autoEnableNewMCPTools=false it shows only servers whose origins appear in enabledMCPTools.
    // Individual tools are not listed here — the MCPs tab / server policy covers tool-level affordances.
    test('RHS Tools popover shows no providers when agent has empty enabledMCPTools', async ({ page }) => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const token = adminClient.getToken();

        const agent = await agentApi.createTestAgent(token, {
            displayName: 'RHS Empty MCP Agent',
            serviceID: mockServiceId,
            autoEnableNewMCPTools: false,
            enabledMCPTools: [],
            enabledNativeTools: [],
        });

        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);

        await mmPage.login(mattermost.url(), agentRegularUsername, agentRegularPassword);
        await mmPage.createAndNavigateToDMWithBot(
            mattermost, agentRegularUsername, agentRegularPassword, agent.name,
        );

        await aiPlugin.openRHS();
        await expect(page.getByTestId('bot-selector-rhs')).toBeVisible({ timeout: 15000 });
        await aiPlugin.switchBotWhenListed(agent.displayName);

        const menu = await aiPlugin.openRhsToolProvidersMenu();
        await expect(menu.getByText('Tool Providers', { exact: true })).toBeVisible();
        await expect(menu.getByText('No tool providers available')).toBeVisible({ timeout: 20000 });
    });

    test('RHS Tools popover shows Mattermost provider when only embedded tools are enabled', async ({ page }) => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const token = adminClient.getToken();

        const agent = await agentApi.createTestAgent(token, {
            displayName: 'RHS Embedded Only Agent',
            serviceID: mockServiceId,
            autoEnableNewMCPTools: false,
            enabledMCPTools: [
                { server_origin: embeddedMattermostOrigin, tool_name: 'read_post' },
            ],
            enabledNativeTools: [],
        });

        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);

        await mmPage.login(mattermost.url(), agentRegularUsername, agentRegularPassword);
        await mmPage.createAndNavigateToDMWithBot(
            mattermost, agentRegularUsername, agentRegularPassword, agent.name,
        );

        await aiPlugin.openRHS();
        await expect(page.getByTestId('bot-selector-rhs')).toBeVisible({ timeout: 15000 });
        await aiPlugin.switchBotWhenListed(agent.displayName);

        const menu = await aiPlugin.openRhsToolProvidersMenu();
        await expect(menu.getByText('Tool Providers', { exact: true })).toBeVisible();
        await expect(menu.getByText('Mattermost')).toBeVisible({ timeout: 20000 });
        await expect(menu.getByText('No tool providers available')).not.toBeVisible();
    });

    test('editing an auto-enable-all agent preserves MCP provider access', async ({ page }) => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const token = adminClient.getToken();

        const agent = await agentApi.createTestAgent(token, {
            displayName: 'Implicit All Agent',
            serviceID: mockServiceId,
            enabledNativeTools: [],
            // createTestAgent defaults autoEnableNewMCPTools=true.
        });

        let fetched = await agentApi.getAgent(token, agent.id);
        expect(fetched.autoEnableNewMCPTools).toBe(true);

        const mmPage = new MattermostPage(page);
        const agentPage = new AgentPageHelper(page);
        const aiPlugin = new AIPlugin(page);

        await mmPage.login(mattermost.url(), agentAdminUsername, agentAdminPassword);
        await agentPage.navigateToAgents(mattermost.url());
        await agentPage.openAgentActions('Implicit All Agent');
        await agentPage.clickEditAction('Implicit All Agent');
        await agentPage.waitForModal();

        await agentPage.getDisplayNameInput().clear();
        await agentPage.getDisplayNameInput().fill('Implicit All Agent Updated');
        await agentPage.getModalSaveButton().click();
        await agentPage.waitForModalClosed();

        fetched = await agentApi.getAgent(token, agent.id);
        expect(fetched.autoEnableNewMCPTools).toBe(true);

        await page.context().clearCookies();
        await mmPage.login(mattermost.url(), agentRegularUsername, agentRegularPassword);
        await mmPage.createAndNavigateToDMWithBot(
            mattermost, agentRegularUsername, agentRegularPassword, agent.name,
        );

        await aiPlugin.openRHS();
        await expect(page.getByTestId('bot-selector-rhs')).toBeVisible({ timeout: 15000 });
        await aiPlugin.switchBotWhenListed('Implicit All Agent Updated');

        const menu = await aiPlugin.openRhsToolProvidersMenu();
        await expect(menu.getByText('Tool Providers', { exact: true })).toBeVisible();
        await expect(menu.getByText('Mattermost')).toBeVisible({ timeout: 20000 });
        await expect(menu.getByText('No tool providers available')).not.toBeVisible();
    });

    test('RHS Tools popover lists Mattermost for default Mock Bot (no per-agent MCP restriction)', async ({
        page,
    }) => {
        test.setTimeout(90000);
        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);

        await mmPage.login(mattermost.url(), agentRegularUsername, agentRegularPassword);
        await page.goto(`${mattermost.url()}/test/channels/town-square`);
        await page.getByTestId('channel_view').waitFor({ state: 'visible', timeout: 60000 });

        await aiPlugin.openRHS();
        await expect(page.getByTestId('bot-selector-rhs')).toBeVisible({ timeout: 15000 });
        await aiPlugin.switchBotWhenListed('Mock Bot');

        const menu = await aiPlugin.openRhsToolProvidersMenu();
        await expect(menu.getByText('Mattermost')).toBeVisible({ timeout: 20000 });
    });
});
