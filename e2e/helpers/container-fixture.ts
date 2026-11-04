import { test as base, expect } from '@playwright/test';

import { withDeadline } from './deadline';

export { expect };

type Stoppable = { stop: () => Promise<unknown> };

/** Registers a started container or stack for teardown and returns it unchanged. */
export type Own = <T extends Stoppable>(started: T) => T;

const STOP_DEADLINE_MS = 120000;

/**
 * Returns a `test` whose worker-scoped `containers` fixture holds what `start`
 * resolves to. The fixture has its own unlimited timeout, so container startup
 * and teardown are never charged to a hook or test; every startup step bounds
 * itself instead. Everything passed to `own` is stopped in reverse order when
 * the worker shuts down, including after a failed start.
 *
 * Each call registers a distinct fixture, and Playwright runs tests with
 * distinct worker fixtures in separate workers. Call it once per spec file, or
 * once per describe or test that needs its own containers; containers and
 * server state are never shared across calls.
 */
export function containerTest<T>(start: (own: Own) => Promise<T>) {
    return base.extend<{}, { containers: T }>({
        containers: [async ({}, use) => {
            const owned: Stoppable[] = [];
            try {
                await use(await start((started) => {
                    owned.push(started);
                    return started;
                }));
            } finally {
                await stopAll(owned.reverse());
            }
        }, { scope: 'worker', timeout: 0 }],
    });
}

async function stopAll(stack: Stoppable[]): Promise<void> {
    const errors: unknown[] = [];
    for (const started of stack) {
        try {
            await withDeadline('Stopping container', STOP_DEADLINE_MS, () => started.stop());
        } catch (error) {
            errors.push(error);
        }
    }
    if (errors.length > 0) {
        throw errors[0];
    }
}
