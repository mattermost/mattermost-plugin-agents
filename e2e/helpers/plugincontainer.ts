import fs from 'fs';
import path from 'path';

import MattermostContainer from './mmcontainer';

export const AIMOCK_SERVICE_ID = 'aimock-service';
export const AIMOCK_BOT_ID = 'aimock-bot';
export const AIMOCK_BOT_NAME = 'aimock';

function findPluginTarball(): string {
  const distPath = path.join(__dirname, '..', '..', 'dist');
  const tarballs = fs.readdirSync(distPath)
    .filter((file) => file.endsWith('.tar.gz'))
    .sort();
  if (tarballs.length === 0) {
    throw new Error('No tar.gz file found in dist folder');
  }
  if (tarballs.length > 1) {
    throw new Error(`Expected exactly one plugin tarball in dist, found: ${tarballs.join(', ')}`);
  }
  return path.join(distPath, tarballs[0]);
}

async function setupStandardTestUsers(mattermost: MattermostContainer): Promise<void> {
  await mattermost.createUser('regularuser@sample.com', 'regularuser', 'regularuser');
  await mattermost.addUserToTeam('regularuser', 'test');
  await mattermost.createUser('seconduser@sample.com', 'seconduser', 'seconduser');
  await mattermost.addUserToTeam('seconduser', 'test');

  const userClient = await mattermost.getClient('regularuser', 'regularuser');
  const user = await userClient.getMe();
  await userClient.savePreferences(user.id, [
    { user_id: user.id, category: 'tutorial_step', name: user.id, value: '999' },
    { user_id: user.id, category: 'onboarding_task_list', name: 'onboarding_task_list_show', value: 'false' },
    { user_id: user.id, category: 'onboarding_task_list', name: 'onboarding_task_list_open', value: 'false' },
    {
      user_id: user.id,
      category: 'drafts',
      name: 'drafts_tour_tip_showed',
      value: JSON.stringify({ drafts_tour_tip_showed: true }),
    },
    { user_id: user.id, category: 'crt_thread_pane_step', name: user.id, value: '999' },
  ]);

  const adminClient = await mattermost.getAdminClient();
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

  await adminClient.completeSetup({
    organization: 'test',
    install_plugins: [],
  });

  await mattermost.grantSelfServiceAgentPermissions();
}

async function finalizeContainerSetup(mattermost: MattermostContainer): Promise<MattermostContainer> {
  try {
    await setupStandardTestUsers(mattermost);
    return mattermost;
  } catch (error) {
    await mattermost.stop().catch(() => undefined);
    throw error;
  }
}

export async function RunAIMockContainer(overrides?: {
  service?: Partial<Record<string, unknown>>;
  bot?: Partial<Record<string, unknown>>;
  mcp?: Record<string, unknown>;
  webSearch?: Record<string, unknown>;
  enableChannelMentionToolCalling?: boolean;
}): Promise<MattermostContainer> {
  const filename = findPluginTarball();

  const pluginConfig = {
    config: {
      allowPrivateChannels: true,
      disableFunctionCalls: false,
      enableUserRestrictions: false,
      allowUnsafeLinks: true,
      defaultBotName: AIMOCK_BOT_NAME,
      enableChannelMentionToolCalling: overrides?.enableChannelMentionToolCalling ?? false,
      mcp: overrides?.mcp ?? {
        embeddedServer: { enabled: false },
        enablePluginServer: false,
        enabled: false,
        idleTimeoutMinutes: 30,
        servers: [],
      },
      services: [
        {
          id: AIMOCK_SERVICE_ID,
          name: 'Aimock Service',
          type: 'openaicompatible',
          apiKey: 'mock',
          apiURL: 'http://openai:8080',
          defaultModel: 'gpt-mock',
          useResponsesAPI: false,
          ...overrides?.service,
        },
      ],
      bots: [
        {
          id: AIMOCK_BOT_ID,
          name: AIMOCK_BOT_NAME,
          displayName: 'Aimock Bot',
          customInstructions: '',
          serviceID: AIMOCK_SERVICE_ID,
          enabledNativeTools: [],
          reasoningEnabled: true,
          disableTools: false,
          ...overrides?.bot,
        },
      ],
      ...(overrides?.webSearch ? { webSearch: overrides.webSearch } : {}),
    },
  };

  const allowedConnections = overrides?.webSearch ? 'openai,websearch' : 'openai';
  const mattermost = await new MattermostContainer()
    .withEnv('MM_SERVICESETTINGS_ALLOWEDUNTRUSTEDINTERNALCONNECTIONS', allowedConnections)
    .withPlugin(filename, 'mattermost-ai', pluginConfig)
    .start();

  return finalizeContainerSetup(mattermost);
}

const RunContainer = async (): Promise<MattermostContainer> => {
  const filename = findPluginTarball();
  const pluginConfig = {
	  "config": {
		  "allowPrivateChannels": true,
		  "disableFunctionCalls": false,
		  "enableUserRestrictions": false,
		  "allowUnsafeLinks": true,
		  "defaultBotName": "mock",
		  "mcp": {
			  "embeddedServer": {
				  "enabled": true
			  },
			  "enablePluginServer": true,
			  "enabled": true,
			  "idleTimeoutMinutes": 30,
			  "servers": null
		  },
		  "services": [
			  {
				  "id": "mock-service",
				  "name": "Mock Service",
				  "type": "openaicompatible",
				  "apiKey": "mock",
				  "apiURL": "http://openai:8080",
				  "defaultModel": "gpt-mock",
				  "useResponsesAPI": false,
			  },
			  {
				  "id": "second-service",
				  "name": "Second Service",
				  "type": "openaicompatible",
				  "apiKey": "ohno",
				  "apiURL": "http://openai:8080/second",
				  "defaultModel": "gpt-mock",
				  "useResponsesAPI": false,
			  },
		  ],
		  "bots": [
			  {
				  "id": "y6fcxh0xc",
				  "name": "mock",
				  "displayName": "Mock Bot",
				  "customInstructions": "",
				  "serviceID": "mock-service",
				  "enabledNativeTools": [],
			  },
			  {
				  "id": "oawiejfoj",
				  "name": "second",
				  "displayName": "Second Bot",
				  "customInstructions": "",
				  "serviceID": "second-service",
				  "enabledNativeTools": [],
			  },
		  ],
		  "embeddingSearchConfig": {
			  "type": "composite",
			  "dimensions": 512,
			  "vectorStore": {
				  "type": "pgvector",
				  "parameters": {
					  "dimensions": 512
				  }
			  },
			  "embeddingProvider": {
				  "type": "mock",
				  "parameters": {}
			  },
			  "parameters": {},
			  "chunkingOptions": {
				  "chunkSize": 500,
				  "chunkOverlap": 100,
				  "chunkingStrategy": "sentences"
			  }
		  }
	  }
  }
  const mattermost = await new MattermostContainer()
        .withPlugin(filename, "mattermost-ai", pluginConfig)
        .start();
  return finalizeContainerSetup(mattermost);
}

export default RunContainer
