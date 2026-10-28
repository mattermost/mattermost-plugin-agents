/**
 * Rejects with an error naming `step` when `run` has not settled within `ms`.
 * Container fixtures run without a Playwright timeout, so each startup step
 * bounds itself; the abandoned work is left to settle (or be reaped) on its own.
 */
export async function withDeadline<T>(step: string, ms: number, run: () => Promise<T>): Promise<T> {
    let timer: NodeJS.Timeout | undefined;
    const expired = new Promise<never>((_, reject) => {
        timer = setTimeout(() => reject(new Error(`${step} did not finish within ${ms / 1000} s`)), ms);
    });
    try {
        return await Promise.race([run(), expired]);
    } finally {
        clearTimeout(timer);
    }
}
