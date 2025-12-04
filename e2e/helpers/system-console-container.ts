import fs from 'fs';
import path from 'path';
import MattermostContainer from './mmcontainer';

/**
 * Container setup for System Console tests
 * Uses mock configurations (no real API keys)
 */

export interface SystemConsolePluginConfig {
    allowPrivateChannels?: boolean;
    disableFunctionCalls?: boolean;
    enableLLMTrace?: boolean;
    enableUserRestrictions?: boolean;
    enableVectorIndex?: boolean;
    enableTokenUsageLogging?: boolean;
    defaultBotName?: string;
    allowedUpstreamHostnames?: string;
    allowUnsafeLinks?: boolean;
    services?: any[];
    bots?: any[];
}

const adminUsername = 'sysadmin';
const adminPassword = 'Sys@dmin-sample1';

/**
 * Find and return the plugin tar.gz file path
 */
function findPluginFile(): string {
    const distPath = path.join(__dirname, '..', '..', 'dist');
    let filename = "";
    fs.readdirSync(distPath).forEach(file => {
        if (file.endsWith(".tar.gz")) {
            filename = path.join(distPath, file);
        }
    });
    if (filename === "") {
        throw new Error("No tar.gz file found in dist folder");
    }
    return filename;
}

/**
 * Setup admin user with standard preferences
 */
async function setupAdminUser(mattermost: MattermostContainer): Promise<void> {
    // Create sysadmin user
    await mattermost.createAdmin('sysadmin@example.com', adminUsername, adminPassword);
    await mattermost.addUserToTeam(adminUsername, 'test');

    // Set up preferences for sysadmin
    const adminClient = await mattermost.getClient(adminUsername, adminPassword);
    const admin = await adminClient.getMe();
    await adminClient.savePreferences(admin.id, [
        { user_id: admin.id, category: 'tutorial_step', name: admin.id, value: '999' },
        { user_id: admin.id, category: 'onboarding_task_list', name: 'onboarding_task_list_show', value: 'false' },
        { user_id: admin.id, category: 'onboarding_task_list', name: 'onboarding_task_list_open', value: 'false' },
        {
            user_id: admin.id,
            category: 'drafts',
            name: 'drafts_tour_tip_showed',
            value: JSON.stringify({ drafts_tour_tip_showed: true }),
        },
        { user_id: admin.id, category: 'crt_thread_pane_step', name: admin.id, value: '999' },
    ]);
}

/**
 * Run a Mattermost container configured for System Console tests
 * @param config Plugin configuration (services, bots, etc.)
 * @returns Configured MattermostContainer
 */
export async function RunSystemConsoleContainer(config: SystemConsolePluginConfig): Promise<MattermostContainer> {
    const filename = findPluginFile();

    const pluginConfig = {
        config: {
            allowPrivateChannels: config.allowPrivateChannels ?? true,
            disableFunctionCalls: config.disableFunctionCalls ?? false,
            enableLLMTrace: config.enableLLMTrace ?? true,
            enableUserRestrictions: config.enableUserRestrictions ?? false,
            enableVectorIndex: config.enableVectorIndex ?? false,
            enableTokenUsageLogging: config.enableTokenUsageLogging,
            defaultBotName: config.defaultBotName,
            allowedUpstreamHostnames: config.allowedUpstreamHostnames,
            allowUnsafeLinks: config.allowUnsafeLinks,
            services: config.services ?? [],
            bots: config.bots ?? [],
        }
    };

    const mattermost = await new MattermostContainer()
        .withPlugin(filename, 'mattermost-ai', pluginConfig)
        .start();

    await setupAdminUser(mattermost);

    return mattermost;
}

export { adminUsername, adminPassword };
export default RunSystemConsoleContainer;

