import { FullConfig } from '@playwright/test';
import { execFile } from 'child_process';
import fs from 'fs';
import path from 'path';
import { promisify } from 'util';

import { AIMOCK_IMAGE } from './helpers/aimock-container';
import { POSTGRES_IMAGE, mattermostImage } from './helpers/mmcontainer';
import { SMOCKER_IMAGE } from './helpers/openai-mock';

const execFileAsync = promisify(execFile);

// Pull shared images up front so the first spec in a run does not spend its
// beforeAll timeout downloading them. Failures are non-fatal: testcontainers
// still pulls on demand.
async function prePullImages() {
  const images = [mattermostImage(), POSTGRES_IMAGE, SMOCKER_IMAGE, AIMOCK_IMAGE];
  const startedAt = Date.now();
  await Promise.all(images.map(async (image) => {
    try {
      await execFileAsync('docker', ['pull', '--quiet', image], { timeout: 10 * 60 * 1000 });
    } catch (error) {
      console.log(`Could not pre-pull ${image}: ${(error as Error).message}`);
    }
  }));
  console.log(`Pre-pulled container images in ${Math.round((Date.now() - startedAt) / 1000)}s`);
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

  await prePullImages();

  console.log('Global setup complete');
}

export default globalSetup;
