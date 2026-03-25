import { FullConfig } from '@playwright/test';
import {execFile} from 'child_process';
import fs from 'fs';
import path from 'path';
import {promisify} from 'util';

const execFileAsync = promisify(execFile);
const dockerPullRetries = 3;

async function pullDockerImage(image: string): Promise<void> {
  for (let attempt = 1; attempt <= dockerPullRetries; attempt++) {
    try {
      console.log(`Pre-pulling Docker image (${attempt}/${dockerPullRetries}): ${image}`);
      await execFileAsync('docker', ['pull', image], {
        maxBuffer: 20 * 1024 * 1024,
      });
      return;
    } catch (error) {
      if (attempt === dockerPullRetries) {
        throw error;
      }

      const backoffMs = attempt * 2000;
      console.log(`Docker pull failed for ${image}, retrying in ${backoffMs}ms`);
      await new Promise((resolve) => setTimeout(resolve, backoffMs));
    }
  }
}

async function globalSetup(config: FullConfig) {
  // Create directories for test artifacts
  const dirs = [
    path.join(__dirname, 'test-results'),
    path.join(__dirname, 'test-results/failures'),
    path.join(__dirname, 'test-results/visual'),
  ];
  
  for (const dir of dirs) {
    if (!fs.existsSync(dir)) {
      fs.mkdirSync(dir, { recursive: true });
    }
  }

  if (process.env.CI) {
    const images = [
      process.env.MM_IMAGE || 'mattermost/mattermost-enterprise-edition:11.5.1',
      'pgvector/pgvector:pg15',
      'thiht/smocker',
    ];

    for (const image of images) {
      await pullDockerImage(image);
    }
  }
  
  // You could add additional setup here like:
  // - Setting up test database
  // - Preloading necessary test data
  // - Setting environment variables
  console.log('Global setup complete');
}

export default globalSetup;
