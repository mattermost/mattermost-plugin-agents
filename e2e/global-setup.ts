import { FullConfig } from '@playwright/test';
import {execFile} from 'node:child_process';
import fs from 'fs';
import path from 'path';
import {promisify} from 'node:util';

const execFileAsync = promisify(execFile);

const defaultMattermostImage = 'mattermost/mattermost-enterprise-edition:11.5.1';
const ciPrepullImages = [
    'pgvector/pgvector:pg15',
    'thiht/smocker',
];

async function pullDockerImage(image: string): Promise<void> {
    console.log(`Pre-pulling Docker image: ${image}`);
    await execFileAsync('docker', ['pull', image], {
        maxBuffer: 20 * 1024 * 1024,
    });
}

async function warmCiDockerImages(): Promise<void> {
    if (!process.env.CI) {
        return;
    }

    const images = new Set([
        process.env.MM_IMAGE || defaultMattermostImage,
        ...ciPrepullImages,
    ]);

    for (const image of images) {
        await pullDockerImage(image);
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

  // CI runners can spend most of the default hook budget pulling images during
  // the first suite startup. Warm the shared images here so per-suite setup only
  // pays for container boot and plugin configuration.
  await warmCiDockerImages();
  
  // You could add additional setup here like:
  // - Setting up test database
  // - Preloading necessary test data
  // - Setting environment variables
  console.log('Global setup complete');
}

export default globalSetup;
