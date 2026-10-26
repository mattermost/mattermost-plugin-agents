// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, render, screen, waitFor} from '@testing-library/react';

import {handlePostedWebsocket} from '@/posted_events';

import Rhs from './rhs';

const mockGetAIThreads = jest.fn();
const mockGetUserMCPTools = jest.fn();
const mockGetUserToolPreferences = jest.fn();
const mockUpdateRead = jest.fn();

jest.mock('@/client', () => ({
    getAIThreads: () => mockGetAIThreads(),
    getUserMCPTools: () => mockGetUserMCPTools(),
    getUserToolPreferences: () => mockGetUserToolPreferences(),
    updateRead: (userId: string, teamId: string, postId: string, timestamp: number) => (
        mockUpdateRead(userId, teamId, postId, timestamp)
    ),
}));

type SelectorFn = (state: unknown) => unknown;
const mockUseSelector = jest.fn<unknown, [SelectorFn]>();
const mockDispatch = jest.fn();

jest.mock('react-redux', () => ({
    useSelector: (selector: SelectorFn) => mockUseSelector(selector),
    useDispatch: () => mockDispatch,
}));

const mockViewedPosts = new Set<string>();

jest.mock('@/viewing', () => ({
    getViewingTracker: () => ({isViewed: (postId: string) => mockViewedPosts.has(postId)}),
}));

const mockUseBotlist = jest.fn();

jest.mock('@/bots', () => ({
    useBotlist: () => mockUseBotlist(),
}));

jest.mock('react-intl', () => {
    const ReactActual = jest.requireActual<typeof import('react')>('react');

    return {
        IntlProvider: ({children}: {children: React.ReactNode}) => ReactActual.createElement(ReactActual.Fragment, null, children),
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => ReactActual.createElement(ReactActual.Fragment, null, defaultMessage),
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
        }),
    };
});

const mockThreadViewer = jest.fn();

jest.mock('@/mm_webapp', () => {
    const ReactActual = jest.requireActual<typeof import('react')>('react');

    return {
        ThreadViewer: (props: Record<string, unknown>) => {
            mockThreadViewer(props);
            return ReactActual.createElement('div', {'data-testid': 'rhs-thread-viewer'});
        },
    };
});

jest.mock('./rhs_header', () => {
    const ReactActual = jest.requireActual<typeof import('react')>('react');

    return {
        __esModule: true,
        default: () => ReactActual.createElement('div', {'data-testid': 'rhs-header'}),
    };
});

jest.mock('./rhs_new_tab', () => {
    const ReactActual = jest.requireActual<typeof import('react')>('react');

    return {
        __esModule: true,
        default: () => ReactActual.createElement('div', {'data-testid': 'rhs-new-tab'}),
    };
});

jest.mock('./thread_item', () => {
    const ReactActual = jest.requireActual<typeof import('react')>('react');

    return {
        __esModule: true,
        default: () => ReactActual.createElement('div', {'data-testid': 'rhs-thread-item'}),
    };
});

const activeBot = {
    id: 'bot-id',
    displayName: 'Agents',
    username: 'ai',
    lastIconUpdate: 0,
    dmChannelID: 'dm-channel-id',
    channelAccessLevel: 'all',
    channelIDs: [],
    userAccessLevel: 'all',
    userIDs: [],
    teamIDs: [],
    enabledMCPTools: [],
    autoEnableNewMCPTools: false,
};

const baseState = {
    'plugins-mattermost-ai': {
        selectedPostId: 'post-id',
        deferredNotifications: false as boolean | null,
    },
    entities: {
        users: {
            currentUserId: 'user-id',
        },
        teams: {
            currentTeamId: 'team-id',
        },
        posts: {
            posts: {
                'post-id': {
                    id: 'post-id',
                    props: {
                        conversation_id: 'conversation-id',
                    },
                },
            },
            postsInThread: {
                'post-id': [],
            },
        },
    },
};

function renderRHS() {
    return render(
        <Rhs/>,
    );
}

describe('RHS', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockGetUserMCPTools.mockResolvedValue({servers: []});
        mockGetUserToolPreferences.mockResolvedValue({disabled_servers: []});
        mockUpdateRead.mockImplementation(() => Promise.resolve());
        mockUseBotlist.mockReturnValue({
            bots: [activeBot],
            activeBot,
            setActiveBot: jest.fn(),
        });
        mockUseSelector.mockImplementation((selector) => selector(baseState));
    });

    test('renders the thread viewer when the read marker rejects for missing thread membership', async () => {
        const error = new Error('User thread membership doesn\'t exist');
        const warn = jest.spyOn(console, 'warn').mockImplementation(() => null);
        mockUpdateRead.mockRejectedValue(error);

        const {unmount} = renderRHS();

        expect(screen.getByTestId('rhs-thread-viewer')).toBeTruthy();
        await waitFor(() => {
            expect(mockUpdateRead).toHaveBeenCalledWith('user-id', 'team-id', 'post-id', expect.any(Number));
        });
        await waitFor(() => {
            expect(warn).toHaveBeenCalledWith(
                'Skipping AI thread read marker because thread membership is missing.',
                error,
            );
        });
        expect(mockThreadViewer).toHaveBeenCalledWith(expect.objectContaining({rootPostId: 'post-id'}));

        await act(async () => {
            unmount();
        });
        await act(async () => {
            await Promise.resolve();
        });
        warn.mockRestore();
    });

    test('logs generic read marker rejections', async () => {
        const error = new Error('Unable to update read marker');
        const errorLog = jest.spyOn(console, 'error').mockImplementation(() => null);
        mockUpdateRead.mockRejectedValue(error);

        const {unmount} = renderRHS();

        expect(screen.getByTestId('rhs-thread-viewer')).toBeTruthy();
        await waitFor(() => {
            expect(mockUpdateRead).toHaveBeenCalledWith('user-id', 'team-id', 'post-id', expect.any(Number));
        });
        await waitFor(() => {
            expect(errorLog).toHaveBeenCalledWith(
                'Failed to update AI thread read marker:',
                error,
            );
        });
        expect(mockThreadViewer).toHaveBeenCalledWith(expect.objectContaining({rootPostId: 'post-id'}));

        await act(async () => {
            unmount();
        });
        await act(async () => {
            await Promise.resolve();
        });
        errorLog.mockRestore();
    });

    describe('read marker', () => {
        const now = 1_000_000;
        const dayInMS = 24 * 60 * 60 * 1000;

        function stateWith(deferredNotifications: boolean | null) {
            return {
                ...baseState,
                'plugins-mattermost-ai': {...baseState['plugins-mattermost-ai'], deferredNotifications},
            };
        }

        function deliver(post: Record<string, unknown>) {
            act(() => {
                handlePostedWebsocket({data: {post: JSON.stringify(post)}});
            });
        }

        beforeEach(() => {
            jest.spyOn(Date, 'now').mockReturnValue(now);
            mockViewedPosts.clear();
        });

        afterEach(() => {
            jest.restoreAllMocks();
        });

        test.each([
            {name: 'an older server marks the thread read a day ahead', deferredNotifications: false, readAt: now + dayInMS},
            {name: 'a server that defers notifications marks the thread read as of now', deferredNotifications: true, readAt: now},
        ])('$name', async ({deferredNotifications, readAt}) => {
            const state = stateWith(deferredNotifications);
            mockUseSelector.mockImplementation((selector) => selector(state));

            const {unmount} = renderRHS();

            await waitFor(() => {
                expect(mockUpdateRead).toHaveBeenCalledWith('user-id', 'team-id', 'post-id', readAt);
            });
            unmount();
        });

        test.each([
            {name: 'a delivered reply on screen marks the thread read as of its delivery', post: {id: 'reply', root_id: 'post-id', update_at: now + 500}, viewed: ['reply'], readAt: now + 500},
            {name: 'a delivered reply on screen uses this clock when it is later', post: {id: 'reply', root_id: 'post-id', update_at: now - 500}, viewed: ['reply'], readAt: now},
            {name: 'a delivered reply that is not on screen stays unread', post: {id: 'reply', root_id: 'post-id', update_at: now + 500}, viewed: [], readAt: null},
            {name: 'a reply in another thread is ignored', post: {id: 'reply', root_id: 'other-root', update_at: now + 500}, viewed: ['reply'], readAt: null},
        ])('$name', async ({post, viewed, readAt}) => {
            const state = stateWith(true);
            mockUseSelector.mockImplementation((selector) => selector(state));
            viewed.forEach((postId) => mockViewedPosts.add(postId));

            const {unmount} = renderRHS();
            await waitFor(() => {
                expect(mockUpdateRead).toHaveBeenCalledTimes(1);
            });

            deliver(post);

            if (readAt === null) {
                expect(mockUpdateRead).toHaveBeenCalledTimes(1);
            } else {
                expect(mockUpdateRead).toHaveBeenCalledTimes(2);
                expect(mockUpdateRead).toHaveBeenLastCalledWith('user-id', 'team-id', 'post-id', readAt);
            }
            unmount();
        });

        test.each([
            {name: 'an older server', deferredNotifications: false, readAt: now + dayInMS},
            {name: 'a server that defers notifications', deferredNotifications: true, readAt: now},
        ])('no marker is written until the server answers, then one for $name', async ({deferredNotifications, readAt}) => {
            let state = stateWith(null);
            mockUseSelector.mockImplementation((selector) => selector(state));

            const {rerender, unmount} = renderRHS();
            await act(async () => {
                await Promise.resolve();
            });
            expect(mockUpdateRead).not.toHaveBeenCalled();

            state = stateWith(deferredNotifications);
            rerender(<Rhs/>);
            await waitFor(() => {
                expect(mockUpdateRead).toHaveBeenCalledWith('user-id', 'team-id', 'post-id', readAt);
            });
            expect(mockUpdateRead).toHaveBeenCalledTimes(1);
            unmount();
        });

        test('closing the thread before the server answers writes no marker', async () => {
            const state = stateWith(null);
            mockUseSelector.mockImplementation((selector) => selector(state));

            const {unmount} = renderRHS();
            unmount();
            await act(async () => {
                await Promise.resolve();
            });

            expect(mockUpdateRead).not.toHaveBeenCalled();
        });

        test('closing the thread after the server answers marks it read as of now', async () => {
            let state = stateWith(null);
            mockUseSelector.mockImplementation((selector) => selector(state));

            const {rerender, unmount} = renderRHS();
            state = stateWith(true);
            rerender(<Rhs/>);
            await waitFor(() => {
                expect(mockUpdateRead).toHaveBeenCalledTimes(1);
            });

            unmount();

            expect(mockUpdateRead).toHaveBeenCalledTimes(2);
            expect(mockUpdateRead).toHaveBeenLastCalledWith('user-id', 'team-id', 'post-id', now);
        });

        test('an older server ignores delivered replies', async () => {
            const state = stateWith(false);
            mockUseSelector.mockImplementation((selector) => selector(state));
            mockViewedPosts.add('reply');

            const {unmount} = renderRHS();
            await waitFor(() => {
                expect(mockUpdateRead).toHaveBeenCalledTimes(1);
            });

            deliver({id: 'reply', root_id: 'post-id', update_at: now + 500});

            expect(mockUpdateRead).toHaveBeenCalledTimes(1);
            unmount();
        });
    });
});
