import { test, expect } from '@playwright/test';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { mattermostAIAdminConfigApiFromClient, mattermostAIPluginRoutes } from 'helpers/plugin-http';

let mattermost: MattermostContainer;

test.beforeAll(async () => {
    mattermost = await RunContainer();
});

test.afterAll(async () => {
    await mattermost.stop();
});

type JobStatus = { status: string; error?: string };
type HealthCheck = {
    status: string;
    model_compatible: boolean;
    model_needs_reindex: boolean;
    stored_model_name?: string;
};
type AIBots = { searchEnabled: boolean };

async function waitForReindexComplete(
    routes: ReturnType<typeof mattermostAIPluginRoutes>,
    token: string,
): Promise<JobStatus> {
    const deadline = Date.now() + 60000;
    for (;;) {
        const status = await routes.getJson('admin/reindex/status', token) as JobStatus;
        if (status.status === 'completed') {
            return status;
        }
        if (status.status === 'failed' || status.status === 'canceled') {
            throw new Error(`Reindex did not complete: ${status.status} (${status.error ?? 'no error'})`);
        }
        if (Date.now() > deadline) {
            throw new Error(`Timed out waiting for reindex to complete, last status: ${status.status}`);
        }
        await new Promise((resolve) => setTimeout(resolve, 1000));
    }
}

test.describe('Reindex after embedding model change', () => {
    // Regression test for MM-69713: changing the embedding model must not lock
    // the admin out of the reindex that is required to recover. Previously the
    // backend disabled the whole search service (indexer included) when the
    // model became incompatible, so POST /admin/reindex returned 500 and search
    // stayed disabled permanently.
    test('admin can start a full reindex after changing the embedding model, which re-enables search', async () => {
        const admin = await mattermost.getAdminClient();
        const token = admin.getToken();
        const routes = mattermostAIPluginRoutes(mattermost.url());
        const configApi = mattermostAIAdminConfigApiFromClient(admin, mattermost.url());

        // Index a real post so there is content in the index.
        const userClient = await mattermost.getClient('regularuser', 'regularuser');
        const team = await userClient.getTeamByName('test');
        const channel = await userClient.getChannelByName(team.id, 'town-square');
        await userClient.createPost({
            channel_id: channel.id,
            message: 'The Q4 budget report shows a 15% increase in marketing spend',
        } as any);

        // 1. Initial full reindex. This records the model the index was built
        //    with (mock provider, no model name, 512 dimensions).
        await routes.postJson('admin/reindex', token, { clearIndex: true });
        await waitForReindexComplete(routes, token);

        const botsBefore = await routes.getJson('ai_bots', token) as AIBots;
        expect(botsBefore.searchEnabled).toBe(true);

        // 2. Change the embedding model (same provider and dimensions, different
        //    model name) so the configured model no longer matches the index.
        const config = await configApi.get();
        const embeddingSearchConfig = config.embeddingSearchConfig as Record<string, any>;
        embeddingSearchConfig.embeddingProvider = {
            ...embeddingSearchConfig.embeddingProvider,
            parameters: { embeddingModel: 'changed-embedding-model' },
        };
        await configApi.put({ ...config, embeddingSearchConfig });

        // 3. The index now needs a reindex and query search is disabled.
        const healthAfterChange = await routes.getJson('admin/reindex/health-check', token) as HealthCheck;
        expect(healthAfterChange.model_needs_reindex).toBe(true);
        expect(healthAfterChange.model_compatible).toBe(false);

        const botsAfterChange = await routes.getJson('ai_bots', token) as AIBots;
        expect(botsAfterChange.searchEnabled).toBe(false);

        // 4. The core assertion: the recovery reindex can still be started.
        //    Before the fix this returned HTTP 500 ("search functionality is
        //    not configured"), which postJson surfaces as a thrown error.
        await routes.postJson('admin/reindex', token, { clearIndex: true });
        await waitForReindexComplete(routes, token);

        // 5. After the recovery reindex the model matches again and query
        //    search is re-enabled automatically.
        const healthAfterReindex = await routes.getJson('admin/reindex/health-check', token) as HealthCheck;
        expect(healthAfterReindex.model_compatible).toBe(true);
        expect(healthAfterReindex.model_needs_reindex).toBe(false);
        expect(healthAfterReindex.stored_model_name).toBe('changed-embedding-model');

        const botsAfterReindex = await routes.getJson('ai_bots', token) as AIBots;
        expect(botsAfterReindex.searchEnabled).toBe(true);
    });
});
