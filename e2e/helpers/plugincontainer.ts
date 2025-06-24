import {test, expect } from '@playwright/test';
import fs from 'fs';

import MattermostContainer from './mmcontainer';

const RunContainer = async (): Promise<MattermostContainer> => {
  let filename = "";
  fs.readdirSync("../dist/").forEach(file => {
      if (file.endsWith(".tar.gz")) {
          filename = "../dist/"+file
      }
  })
  if (filename === "") {
      throw("No tar.gz file found in dist folder")
  }
  const pluginConfig = {
	  "config": {
		  "allowPrivateChannels": true,
		  "disableFunctionCalls": false,
		  "enableLLMTrace": true,
		  "enableUserRestrictions": false,
		  "defaultBotName": "mock",
		  "bots": [
			  {
				  "id": "y6fcxh0xc",
				  "name": "mock",
				  "displayName": "Mock Bot",
				  "customInstructions": "",
				  "service": {
					  "type": "openaicompatible",
					  "apiKey": "mock",
					  "apiURL": "http://openai:8080",
				  },
			  },
			  {
				  "id": "oawiejfoj",
				  "name": "second",
				  "displayName": "Second Bot",
				  "customInstructions": "",
				  "service": {
					  "type": "openaicompatible",
					  "apiKey": "ohno",
					  "apiURL": "http://openai:8080/second",
				  },
			  },
		  ],
	  }
  }
  const mattermost = await new MattermostContainer()
        .withPlugin(filename, "mattermost-ai", pluginConfig)
        .start();
  await mattermost.createUser("regularuser@sample.com", "regularuser", "regularuser");
  await mattermost.addUserToTeam("regularuser", "test");
  await mattermost.createUser("seconduser@sample.com", "seconduser", "seconduser");
  await mattermost.addUserToTeam("seconduser", "test");
  const userClient = await mattermost.getClient("regularuser", "regularuser")
  const user = await userClient.getMe()
  await userClient.savePreferences(user.id,[
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

  const adminClient = await mattermost.getAdminClient()
  const admin = await adminClient.getMe()
  await adminClient.savePreferences(admin.id,[
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
      organization: "test",
      install_plugins: [],
  });

  await new Promise(resolve => setTimeout(resolve, 1000))

  return mattermost;
}

export default RunContainer
