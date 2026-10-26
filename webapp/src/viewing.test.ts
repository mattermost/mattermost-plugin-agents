// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {VIEWING_HEARTBEAT_MS, ViewingEnvironment, ViewingTracker, VisibilityEntry} from './viewing';

type Report = {postId: string; viewing: boolean; keepalive: boolean};

function makeEnvironment(options: {observer?: boolean} = {}) {
    const reports: Report[] = [];
    let windowActive = true;
    let now = 1_000;
    let heartbeat: (() => void) | null = null;
    let onVisibility: ((entries: VisibilityEntry[]) => void) | null = null;
    const observed = new Set<Element>();

    const env: ViewingEnvironment = {
        send: async (postId, viewing, keepalive) => {
            reports.push({postId, viewing, keepalive});
        },
        isWindowActive: () => windowActive,
        now: () => now,
        setInterval: (handler) => {
            heartbeat = handler;
            return 1;
        },
        clearInterval: () => {
            heartbeat = null;
        },
        createObserver: (onChange) => {
            if (options.observer === false) {
                return null;
            }
            onVisibility = onChange;
            return {
                observe: (element) => observed.add(element),
                unobserve: (element) => observed.delete(element),
            };
        },
    };

    return {
        env,
        reports,
        observed,
        setWindowActive: (active: boolean) => {
            windowActive = active;
        },
        advance: (ms: number) => {
            now += ms;
        },
        tick: () => {
            heartbeat?.();
        },
        heartbeatRunning: () => heartbeat !== null,
        setOnScreen: (element: Element, isIntersecting: boolean) => {
            onVisibility?.([{target: element, isIntersecting}]);
        },
    };
}

const viewing = (postId: string, keepalive = false): Report => ({postId, viewing: true, keepalive});
const notViewing = (postId: string, keepalive = false): Report => ({postId, viewing: false, keepalive});

describe('ViewingTracker', () => {
    test('reports a response streaming on screen, heartbeats, and refreshes when it ends', () => {
        const fake = makeEnvironment();
        const tracker = new ViewingTracker(fake.env);
        const element = document.createElement('div');

        const registration = tracker.register('post', element);
        fake.setOnScreen(element, true);
        expect(fake.reports).toEqual([]);

        registration.setActive(true);
        expect(fake.reports).toEqual([viewing('post')]);
        expect(fake.heartbeatRunning()).toBe(true);

        fake.tick();
        fake.tick();
        expect(fake.reports).toEqual([viewing('post'), viewing('post'), viewing('post')]);

        registration.setActive(false);
        expect(fake.reports).toEqual([viewing('post'), viewing('post'), viewing('post'), viewing('post')]);
        expect(fake.heartbeatRunning()).toBe(false);
    });

    test.each([
        {
            name: 'scrolling the response off screen',
            leave: (fake: ReturnType<typeof makeEnvironment>, tracker: ViewingTracker, element: Element) => fake.setOnScreen(element, false),
            back: (fake: ReturnType<typeof makeEnvironment>, tracker: ViewingTracker, element: Element) => fake.setOnScreen(element, true),
        },
        {
            name: 'leaving the window',
            leave: (fake: ReturnType<typeof makeEnvironment>, tracker: ViewingTracker) => {
                fake.setWindowActive(false);
                tracker.handleWindowChange();
            },
            back: (fake: ReturnType<typeof makeEnvironment>, tracker: ViewingTracker) => {
                fake.setWindowActive(true);
                tracker.handleWindowChange();
            },
        },
    ])('$name mid-stream withdraws the report until the user is back', ({leave, back}) => {
        const fake = makeEnvironment();
        const tracker = new ViewingTracker(fake.env);
        const element = document.createElement('div');
        const registration = tracker.register('post', element);
        fake.setOnScreen(element, true);
        registration.setActive(true);

        leave(fake, tracker, element);
        expect(fake.reports).toEqual([viewing('post'), notViewing('post')]);
        expect(fake.heartbeatRunning()).toBe(false);

        back(fake, tracker, element);
        expect(fake.reports).toEqual([viewing('post'), notViewing('post'), viewing('post')]);
        expect(fake.heartbeatRunning()).toBe(true);
    });

    test('a response that is not on screen is never reported', () => {
        const fake = makeEnvironment();
        const tracker = new ViewingTracker(fake.env);
        const element = document.createElement('div');
        const registration = tracker.register('post', element);

        registration.setActive(true);
        fake.tick();
        registration.setActive(false);
        registration.dispose();

        expect(fake.reports).toEqual([]);
        expect(tracker.isViewed('post')).toBe(false);
    });

    test('a finished response on screen is not reported, but counts as viewed', () => {
        const fake = makeEnvironment();
        const tracker = new ViewingTracker(fake.env);
        const element = document.createElement('div');
        tracker.register('post', element);
        fake.setOnScreen(element, true);

        expect(fake.reports).toEqual([]);
        expect(tracker.isViewed('post')).toBe(true);
    });

    test('unmounting a reported response withdraws the report once', () => {
        const fake = makeEnvironment();
        const tracker = new ViewingTracker(fake.env);
        const element = document.createElement('div');
        const registration = tracker.register('post', element);
        fake.setOnScreen(element, true);
        registration.setActive(true);

        registration.dispose();
        registration.dispose();

        expect(fake.reports).toEqual([viewing('post'), notViewing('post')]);
        expect(fake.observed.has(element)).toBe(false);
        expect(tracker.isViewed('post')).toBe(false);
    });

    test('the response stays viewed while any rendering of it is on screen', () => {
        const fake = makeEnvironment();
        const tracker = new ViewingTracker(fake.env);
        const center = document.createElement('div');
        const rhs = document.createElement('div');
        const centerRegistration = tracker.register('post', center);
        const rhsRegistration = tracker.register('post', rhs);
        fake.setOnScreen(rhs, true);
        centerRegistration.setActive(true);
        rhsRegistration.setActive(true);

        centerRegistration.dispose();

        expect(tracker.isViewed('post')).toBe(true);
        expect(fake.reports).toEqual([viewing('post')]);
    });

    test('leaving the page withdraws every report with keepalive and stops heartbeating', () => {
        const fake = makeEnvironment();
        const tracker = new ViewingTracker(fake.env);
        const element = document.createElement('div');
        const registration = tracker.register('post', element);
        fake.setOnScreen(element, true);
        registration.setActive(true);

        tracker.handlePageHide();

        expect(fake.reports).toEqual([viewing('post'), notViewing('post', true)]);
        expect(fake.heartbeatRunning()).toBe(false);
        expect(tracker.isViewed('post')).toBe(false);

        tracker.handlePageShow();
        expect(fake.reports).toEqual([viewing('post'), notViewing('post', true), viewing('post')]);
    });

    test('without IntersectionObserver a mounted response counts as on screen', () => {
        const fake = makeEnvironment({observer: false});
        const tracker = new ViewingTracker(fake.env);
        const registration = tracker.register('post', document.createElement('div'));

        registration.setActive(true);

        expect(tracker.isViewed('post')).toBe(true);
        expect(fake.reports).toEqual([viewing('post')]);
    });

    describe('whileDeciding', () => {
        test.each([
            {name: 'never reported', reportedAgo: null, expectRefresh: true},
            {name: 'reported too long ago', reportedAgo: VIEWING_HEARTBEAT_MS, expectRefresh: true},
            {name: 'reported recently', reportedAgo: VIEWING_HEARTBEAT_MS - 1, expectRefresh: false},
        ])('refreshes the report before deciding when $name', async ({reportedAgo, expectRefresh}) => {
            const fake = makeEnvironment();
            const tracker = new ViewingTracker(fake.env);
            const element = document.createElement('div');
            const registration = tracker.register('post', element);
            fake.setOnScreen(element, true);
            if (reportedAgo !== null) {
                registration.setActive(true);
                registration.setActive(false);
                fake.reports.length = 0;
                fake.advance(reportedAgo);
            }

            let reportsWhenDeciding: Report[] = [];
            const result = await tracker.whileDeciding('post', async () => {
                reportsWhenDeciding = [...fake.reports];
                expect(fake.heartbeatRunning()).toBe(true);
                return 'decided';
            });

            expect(result).toBe('decided');
            expect(reportsWhenDeciding).toEqual(expectRefresh ? [viewing('post')] : []);
            expect(fake.heartbeatRunning()).toBe(false);
        });

        test('does not report a decision made off screen', async () => {
            const fake = makeEnvironment();
            const tracker = new ViewingTracker(fake.env);
            tracker.register('post', document.createElement('div'));

            await tracker.whileDeciding('post', async () => 'decided');

            expect(fake.reports).toEqual([]);
        });

        test('ends the decision when it fails', async () => {
            const fake = makeEnvironment();
            const tracker = new ViewingTracker(fake.env);
            const element = document.createElement('div');
            tracker.register('post', element);
            fake.setOnScreen(element, true);

            await expect(tracker.whileDeciding('post', async () => {
                throw new Error('rejected');
            })).rejects.toThrow('rejected');

            expect(fake.heartbeatRunning()).toBe(false);
        });
    });

    test('a failed report does not throw', async () => {
        const fake = makeEnvironment();
        fake.env.send = () => Promise.reject(new Error('network'));
        const tracker = new ViewingTracker(fake.env);
        const element = document.createElement('div');
        const registration = tracker.register('post', element);
        fake.setOnScreen(element, true);

        expect(() => registration.setActive(true)).not.toThrow();
        await Promise.resolve();
    });
});
