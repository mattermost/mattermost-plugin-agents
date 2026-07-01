import { defineConfig, devices } from '@playwright/test';

/** Local-only aimock baseline smoke; excluded from ci-test-groups coverage. */
export default defineConfig({
    testDir: './smoke',
    fullyParallel: false,
    forbidOnly: !!process.env.CI,
    retries: 0,
    workers: 1,
    reporter: [['list']],
    timeout: 180000,
    globalSetup: require.resolve('./global-setup'),
    globalTeardown: require.resolve('./global-teardown'),
    use: {
        trace: 'off',
        screenshot: 'only-on-failure',
    },
    projects: [
        {
            name: 'chromium',
            use: { ...devices['Desktop Chrome'] },
        },
    ],
});
