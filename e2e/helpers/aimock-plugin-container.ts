import fs from 'fs';
import path from 'path';

import MattermostContainer from './mmcontainer';

const findPluginFile = (): string => {
	const distPath = path.join(__dirname, '../../dist/');
	let filename = '';
	fs.readdirSync(distPath).forEach((file) => {
		if (file.endsWith('.tar.gz')) {
			filename = path.join(distPath, file);
		}
	});
	if (filename === '') {
		throw new Error('No tar.gz file found in dist folder — run make dist first');
	}
	return filename;
};

/** Minimal Mattermost + plugin config for aimock sidecar spike specs. */
const RunAimockPluginContainer = async (): Promise<MattermostContainer> => {
	const filename = findPluginFile();
	const pluginConfig = {
		config: {
			allowPrivateChannels: true,
			disableFunctionCalls: false,
			enableUserRestrictions: false,
			allowUnsafeLinks: true,
			defaultBotName: 'aimock',
			mcp: {
				embeddedServer: {enabled: false},
				enablePluginServer: false,
				enabled: false,
				idleTimeoutMinutes: 30,
				servers: null,
			},
			services: [
				{
					id: 'aimock-service',
					name: 'Aimock Service',
					type: 'openaicompatible',
					apiKey: 'mock',
					apiURL: 'http://openai:8080',
					defaultModel: 'gpt-mock',
					useResponsesAPI: false,
				},
			],
			bots: [
				{
					id: 'aimock-bot',
					name: 'aimock',
					displayName: 'Aimock Bot',
					customInstructions: '',
					serviceID: 'aimock-service',
					enabledNativeTools: [],
				},
			],
			embeddingSearchConfig: {
				type: 'composite',
				dimensions: 512,
				vectorStore: {
					type: 'pgvector',
					parameters: {dimensions: 512},
				},
				embeddingProvider: {
					type: 'mock',
					parameters: {},
				},
				parameters: {},
				chunkingOptions: {
					chunkSize: 500,
					chunkOverlap: 100,
					chunkingStrategy: 'sentences',
				},
			},
		},
	};

	const mattermost = await new MattermostContainer()
		.withEnv('MM_SERVICESETTINGS_ALLOWEDUNTRUSTEDINTERNALCONNECTIONS', 'openai')
		.withPlugin(filename, 'mattermost-ai', pluginConfig)
		.start();

	await mattermost.createUser('regularuser@sample.com', 'regularuser', 'regularuser');
	await mattermost.addUserToTeam('regularuser', 'test');

	const userClient = await mattermost.getClient('regularuser', 'regularuser');
	const user = await userClient.getMe();
	await userClient.savePreferences(user.id, [
		{user_id: user.id, category: 'tutorial_step', name: user.id, value: '999'},
		{user_id: user.id, category: 'onboarding_task_list', name: 'onboarding_task_list_show', value: 'false'},
		{user_id: user.id, category: 'onboarding_task_list', name: 'onboarding_task_list_open', value: 'false'},
		{
			user_id: user.id,
			category: 'drafts',
			name: 'drafts_tour_tip_showed',
			value: JSON.stringify({drafts_tour_tip_showed: true}),
		},
		{user_id: user.id, category: 'crt_thread_pane_step', name: user.id, value: '999'},
	]);

	const adminClient = await mattermost.getAdminClient();
	const admin = await adminClient.getMe();
	await adminClient.savePreferences(admin.id, [
		{user_id: admin.id, category: 'tutorial_step', name: admin.id, value: '999'},
		{user_id: admin.id, category: 'onboarding_task_list', name: 'onboarding_task_list_show', value: 'false'},
		{user_id: admin.id, category: 'onboarding_task_list', name: 'onboarding_task_list_open', value: 'false'},
		{
			user_id: admin.id,
			category: 'drafts',
			name: 'drafts_tour_tip_showed',
			value: JSON.stringify({drafts_tour_tip_showed: true}),
		},
		{user_id: admin.id, category: 'crt_thread_pane_step', name: admin.id, value: '999'},
	]);
	await adminClient.completeSetup({
		organization: 'test',
		install_plugins: [],
	});

	await mattermost.grantSelfServiceAgentPermissions();
	return mattermost;
};

export default RunAimockPluginContainer;
