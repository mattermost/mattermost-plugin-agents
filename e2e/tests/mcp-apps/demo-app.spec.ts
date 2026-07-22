import {test, expect, request} from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import {MattermostPage} from 'helpers/mm';
import {
    OpenAIMockContainer,
    RunOpenAIMocks,
    buildTextResponse,
    buildChatCompletionMockRule,
    buildToolCallResponse,
} from 'helpers/openai-mock';
import {
    RunAgentContainer,
    agentAdminUsername,
    agentAdminPassword,
    agentRegularUsername,
    agentRegularPassword,
    mockServiceId,
} from 'helpers/agent-container';
import {AgentAPIHelper} from 'helpers/agent-api';

/** Matches mcp.EmbeddedClientKey — MCP server origin for the embedded Mattermost tools server. */
const embeddedMattermostOrigin = 'embedded://mattermost';

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.describe('MCP Apps demo app', () => {
    test.beforeAll(async () => {
        mattermost = await RunAgentContainer({
            mcp: {
                enabled: true,
                enablePluginServer: true,
                idleTimeoutMinutes: 30,
                servers: [],
                embeddedServer: {
                    enabled: true,
                    enableDemoApps: true,
                    // Replaces the vetted seed for THIS container only: preview_post
                    // auto-runs in DMs so the app renders without approval clicks (D3
                    // AutoApproved path).
                    tool_configs: [{name: 'preview_post', policy: 'auto_run_in_dm', enabled: true}],
                },
                apps: {enabled: true, allowInsecureSameOriginSandbox: true},
            },
        });
        openAIMock = await RunOpenAIMocks(mattermost.network);
    }, {timeout: 180000});

    test.afterAll(async () => {
        await openAIMock?.stop();
        await mattermost?.stop();
    });

    test('same-origin sandbox page is served', async () => {
        const api = await request.newContext();
        const res = await api.get(`${mattermost.url()}/plugins/mattermost-ai/mcp/apps/sandbox`);
        expect(res.status()).toBe(200);
        expect(res.headers()['content-type']).toContain('text/html');
        expect(await res.text()).toContain('sandbox-proxy-ready');
        await api.dispose();
    });

    test('demo app renders after tool success and responds to interaction', async ({page}) => {
        test.setTimeout(120000);

        const agentApi = new AgentAPIHelper(mattermost.url());
        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const token = adminClient.getToken();

        const teams = await adminClient.getMyTeams();
        const team = teams[0];
        const channels = await adminClient.getMyChannels(team.id);
        const townSquare = channels.find((c) => c.name === 'town-square');
        if (!townSquare) {
            throw new Error('town-square channel not found');
        }

        const agent = await agentApi.createTestAgent(token, {
            displayName: 'Preview App Agent',
            serviceID: mockServiceId,
            autoEnableNewMCPTools: false,
            enabledMCPTools: [{server_origin: embeddedMattermostOrigin, tool_name: 'preview_post'}],
            enabledNativeTools: [],
        });

        const seededPost = await adminClient.createPost({
            channel_id: townSquare.id,
            message: `MCP Apps demo seeded post ${Date.now()}`,
        });

        const agentSystemPrompt = `You are called ${agent.displayName} with the username ${agent.name}`;
        await openAIMock.addMocks([
            buildChatCompletionMockRule(buildTextResponse('Preview app title'), {
                bodyContains:
                    'Write a short title for the following request. Include only the title and nothing else, no quotations. Request:',
                times: 1,
            }),
            buildChatCompletionMockRule(
                buildToolCallResponse(
                    'call_preview_post_demo',
                    'preview_post',
                    JSON.stringify({post_id: seededPost.id}),
                ),
                {
                    bodyContains: agentSystemPrompt,
                    times: 1,
                },
            ),
            buildChatCompletionMockRule(buildTextResponse('The post preview is shown above.'), {
                bodyContains: 'call_preview_post_demo',
                times: 1,
            }),
        ]);

        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), agentRegularUsername, agentRegularPassword);
        await mmPage.createAndNavigateToDMWithBot(
            mattermost,
            agentRegularUsername,
            agentRegularPassword,
            agent.name,
        );

        await mmPage.sendChannelMessage(
            `Use the preview_post tool to preview post ${seededPost.id}. Call the tool now.`,
        );

        await expect(page.getByText('The post preview is shown above.')).toBeVisible({timeout: 60000});

        await expect(page.getByTestId('mcp-app-view')).toBeVisible({timeout: 30000});
        const outer = page.frameLocator('iframe[src*="/plugins/mattermost-ai/mcp/apps/sandbox"]');
        const inner = outer.frameLocator('iframe');
        await expect(inner.getByTestId('preview-post-message')).toContainText('MCP Apps demo seeded post', {timeout: 30000});
        await expect(inner.getByTestId('preview-post-raw')).not.toBeVisible();
        await inner.getByTestId('preview-post-toggle').click();
        await expect(inner.getByTestId('preview-post-raw')).toBeVisible();
    });
});
