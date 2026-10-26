// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {doSetPostViewing} from './client';

// The server forgets a viewer 25s after the last report, so a heartbeat every
// 10s survives one lost request.
export const VIEWING_HEARTBEAT_MS = 10_000;

export type VisibilityEntry = {target: Element; isIntersecting: boolean};

export interface ViewingEnvironment {
    send: (postId: string, viewing: boolean, keepalive: boolean) => Promise<void>;
    isWindowActive: () => boolean;
    now: () => number;
    setInterval: (handler: () => void, ms: number) => unknown;
    clearInterval: (handle: unknown) => void;

    // Returns null when the browser cannot report element visibility; an
    // element then counts as on screen while it is mounted.
    createObserver: (onChange: (entries: VisibilityEntry[]) => void) => {
        observe: (element: Element) => void;
        unobserve: (element: Element) => void;
    } | null;
}

export interface ViewingRegistration {
    setActive: (active: boolean) => void;
    dispose: () => void;
}

interface Registration {
    postId: string;
    element: Element;
    onScreen: boolean;
    active: boolean;
}

interface PostViewing {
    registrations: Set<Registration>;
    decisions: number;

    // True from the moment viewing=true is sent until viewing=false is.
    reportedViewing: boolean;
    heartbeating: boolean;
    lastReportedAt: number;
}

// Tracks which agent posts this window has on screen. While a post is still
// being worked on, viewers report it to the server so it does not push, email
// or notify them about a response they watched arrive.
export class ViewingTracker {
    private readonly env: ViewingEnvironment;
    private readonly posts = new Map<string, PostViewing>();
    private readonly byElement = new Map<Element, Set<Registration>>();
    private observer: ReturnType<ViewingEnvironment['createObserver']> | undefined;
    private heartbeatHandle: unknown = null;
    private pageHidden = false;

    constructor(env: ViewingEnvironment) {
        this.env = env;
    }

    register(postId: string, element: Element): ViewingRegistration {
        const post = this.getOrCreate(postId);
        const observer = this.getObserver();
        const registration: Registration = {postId, element, onScreen: observer === null, active: false};
        post.registrations.add(registration);

        let forElement = this.byElement.get(element);
        if (!forElement) {
            forElement = new Set();
            this.byElement.set(element, forElement);
            observer?.observe(element);
        }
        forElement.add(registration);

        let disposed = false;
        return {
            setActive: (active: boolean) => {
                if (disposed || registration.active === active) {
                    return;
                }
                registration.active = active;
                this.evaluate(postId);
            },
            dispose: () => {
                if (disposed) {
                    return;
                }
                disposed = true;
                post.registrations.delete(registration);
                const remaining = this.byElement.get(element);
                remaining?.delete(registration);
                if (remaining?.size === 0) {
                    this.byElement.delete(element);
                    observer?.unobserve(element);
                }
                this.evaluate(postId);
            },
        };
    }

    // Keeps the post reported as viewed while a decision on it is submitted:
    // the server may notify the requester while handling the decision, so a
    // stale report is refreshed before the decision is sent.
    async whileDeciding<T>(postId: string, decide: () => Promise<T>): Promise<T> {
        const post = this.getOrCreate(postId);
        post.decisions++;
        try {
            if (this.isViewed(postId)) {
                const stale = !post.reportedViewing || this.env.now() - post.lastReportedAt >= VIEWING_HEARTBEAT_MS;
                post.heartbeating = true;
                if (stale) {
                    await this.reportViewing(postId);
                }
            }
            this.evaluate(postId);
            return await decide();
        } finally {
            post.decisions--;
            this.evaluate(postId);
        }
    }

    // Whether the post is on screen in this window while the window is in use.
    isViewed(postId: string): boolean {
        if (this.pageHidden || !this.env.isWindowActive()) {
            return false;
        }
        const post = this.posts.get(postId);
        if (!post) {
            return false;
        }
        return Array.from(post.registrations).some((registration) => registration.onScreen);
    }

    handleVisibilityChange = (entries: VisibilityEntry[]) => {
        const changed = new Set<string>();
        for (const entry of entries) {
            this.byElement.get(entry.target)?.forEach((registration) => {
                registration.onScreen = entry.isIntersecting;
                changed.add(registration.postId);
            });
        }
        changed.forEach((postId) => this.evaluate(postId));
    };

    // Window focus or tab visibility changed.
    handleWindowChange = () => {
        this.evaluateAll();
    };

    handlePageHide = () => {
        this.pageHidden = true;
        for (const [postId, post] of this.posts) {
            post.heartbeating = false;
            if (post.reportedViewing) {
                post.reportedViewing = false;
                this.send(postId, false, true);
            }
        }
        this.updateHeartbeat();
    };

    handlePageShow = () => {
        this.pageHidden = false;
        this.evaluateAll();
    };

    private evaluateAll() {
        Array.from(this.posts.keys()).forEach((postId) => this.evaluate(postId));
    }

    private evaluate(postId: string) {
        const post = this.posts.get(postId);
        if (!post) {
            return;
        }

        const viewed = this.isViewed(postId);
        const active = post.decisions > 0 || Array.from(post.registrations).some((registration) => registration.active);

        if (viewed && active) {
            if (!post.heartbeating) {
                post.heartbeating = true;
                this.reportViewing(postId);
            }
        } else if (post.heartbeating) {
            post.heartbeating = false;

            // The server notifies once the run ends; refresh the report so a
            // response that finished on screen still counts as seen.
            if (viewed) {
                this.reportViewing(postId);
            }
        }

        if (!viewed && post.reportedViewing) {
            post.reportedViewing = false;
            this.send(postId, false, false);
        }

        if (post.registrations.size === 0 && post.decisions === 0 && !post.reportedViewing) {
            this.posts.delete(postId);
        }
        this.updateHeartbeat();
    }

    private reportViewing(postId: string): Promise<void> {
        const post = this.posts.get(postId);
        if (post) {
            post.reportedViewing = true;
            post.lastReportedAt = this.env.now();
        }
        return this.send(postId, true, false);
    }

    private send(postId: string, viewing: boolean, keepalive: boolean): Promise<void> {
        return this.env.send(postId, viewing, keepalive).catch(() => {
            // Best effort: a lost report only means the viewer may be notified.
        });
    }

    private heartbeat = () => {
        for (const [postId, post] of this.posts) {
            if (post.heartbeating) {
                this.reportViewing(postId);
            }
        }
    };

    private updateHeartbeat() {
        const needed = Array.from(this.posts.values()).some((post) => post.heartbeating);
        if (needed && this.heartbeatHandle === null) {
            this.heartbeatHandle = this.env.setInterval(this.heartbeat, VIEWING_HEARTBEAT_MS);
        } else if (!needed && this.heartbeatHandle !== null) {
            this.env.clearInterval(this.heartbeatHandle);
            this.heartbeatHandle = null;
        }
    }

    private getOrCreate(postId: string): PostViewing {
        let post = this.posts.get(postId);
        if (!post) {
            post = {registrations: new Set(), decisions: 0, reportedViewing: false, heartbeating: false, lastReportedAt: 0};
            this.posts.set(postId, post);
        }
        return post;
    }

    private getObserver() {
        if (this.observer === undefined) { // eslint-disable-line no-undefined
            this.observer = this.env.createObserver(this.handleVisibilityChange);
        }
        return this.observer;
    }
}

function browserEnvironment(): ViewingEnvironment {
    return {
        send: doSetPostViewing,
        isWindowActive: () => document.visibilityState === 'visible' && document.hasFocus(),
        now: () => Date.now(),
        setInterval: (handler, ms) => window.setInterval(handler, ms),
        clearInterval: (handle) => window.clearInterval(handle as number),
        createObserver: (onChange) => {
            if (typeof IntersectionObserver === 'undefined') {
                return null;
            }
            return new IntersectionObserver((entries) => onChange(entries));
        },
    };
}

let tracker: ViewingTracker | null = null;

export function getViewingTracker(): ViewingTracker {
    if (!tracker) {
        tracker = new ViewingTracker(browserEnvironment());
        window.addEventListener('focus', tracker.handleWindowChange);
        window.addEventListener('blur', tracker.handleWindowChange);
        document.addEventListener('visibilitychange', tracker.handleWindowChange);
        window.addEventListener('pagehide', tracker.handlePageHide);
        window.addEventListener('pageshow', tracker.handlePageShow);
    }
    return tracker;
}
