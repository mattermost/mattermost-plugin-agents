import type {BrowserContext, Page} from '@playwright/test';

import type {DesktopRecord} from './types';

const BINDING = '__notificationLabRecord';

type RawNotification = Omit<DesktopRecord, 'seq'>;

/**
 * Installed in every page of the context before any app script runs. It
 * replaces `window.Notification` with a recorder that reports each
 * `new Notification(title, options)` to Node through the exposed binding and
 * reports permission as granted. It must stay self-contained so Playwright can
 * serialize it.
 */
function installNotificationRecorder(binding: string) {
    const report = (window as unknown as Record<string, (n: unknown) => void>)[binding];

    class RecordingNotification {
        static permission: NotificationPermission = 'granted';
        static maxActions = 0;

        static requestPermission(callback?: (permission: NotificationPermission) => void) {
            callback?.('granted');
            return Promise.resolve<NotificationPermission>('granted');
        }

        onclick: ((event: Event) => void) | null = null;
        onclose: ((event: Event) => void) | null = null;
        onerror: ((event: Event) => void) | null = null;
        onshow: ((event: Event) => void) | null = null;
        readonly title: string;
        readonly body: string;
        readonly tag: string;

        constructor(title: string, options: NotificationOptions = {}) {
            this.title = title;
            this.body = options.body ?? '';
            this.tag = options.tag ?? '';
            report({
                at: Date.now(),
                title,
                body: this.body,
                tag: this.tag,
                silent: Boolean(options.silent),
            });
        }

        close() {
            // Nothing to dismiss: the notification was never shown by the browser.
        }

        addEventListener() {
            // The app only uses the on* handlers.
        }

        removeEventListener() {
            // See addEventListener.
        }

        dispatchEvent() {
            return true;
        }
    }

    Object.defineProperty(window, 'Notification', {
        value: RecordingNotification,
        configurable: true,
        writable: true,
    });
}

/** Records the desktop (browser) notifications one user's browser context would have shown. */
export class DesktopRecorder {
    private readonly records: DesktopRecord[] = [];

    /** Must be called before the first navigation of the context. */
    static async install(context: BrowserContext): Promise<DesktopRecorder> {
        const recorder = new DesktopRecorder();
        await context.exposeBinding(BINDING, (_source, raw: RawNotification) => {
            recorder.records.push({seq: recorder.records.length, ...raw});
        });
        await context.addInitScript(installNotificationRecorder, BINDING);
        await context.grantPermissions(['notifications']);
        return recorder;
    }

    /** Number of notifications recorded so far; use as a cursor for `since()`. */
    count(): number {
        return this.records.length;
    }

    since(cursor: number): DesktopRecord[] {
        return this.records.slice(cursor);
    }
}

/**
 * Makes the page behave as the focused window. The webapp decides whether to
 * suppress desktop notifications from `window` focus/blur events
 * (`views.browser.focused`, `window.isActive`), so firing the events is what a
 * real focus change does.
 */
export async function focusWindow(page: Page): Promise<void> {
    await page.bringToFront();
    await page.evaluate(() => window.dispatchEvent(new Event('focus')));
}

/** Makes the page behave as a window the user has switched away from. */
export async function blurWindow(page: Page): Promise<void> {
    await page.evaluate(() => window.dispatchEvent(new Event('blur')));
}

/** The webapp's own view of whether the window is active (`window.isActive`). */
export async function isWindowActive(page: Page): Promise<boolean> {
    return page.evaluate(() => Boolean((window as unknown as {isActive?: boolean}).isActive));
}
