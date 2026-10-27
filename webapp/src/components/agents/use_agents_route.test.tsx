// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook} from '@testing-library/react';

import {AGENTS_ROUTE, buildChatPath, parseAgentsRoute, useAgentsRoute} from './use_agents_route';

const makeHistory = (initial: string, withReplace = false) => {
    const listeners = new Set<() => void>();
    const history = {
        ...(withReplace && {
            replace: jest.fn((path: string) => {
                history.location = {pathname: path};
                listeners.forEach((l) => l());
            }),
        }),
        location: {pathname: initial},
        push: jest.fn((path: string) => {
            history.location = {pathname: path};
            listeners.forEach((l) => l());
        }),
        listen: jest.fn((l: () => void) => {
            listeners.add(l);
            return () => listeners.delete(l);
        }),
        listeners,
    };
    return history;
};

describe('parseAgentsRoute', () => {
    test.each([
        {path: AGENTS_ROUTE, expected: {view: 'landing'}},
        {path: `${AGENTS_ROUTE}/`, expected: {view: 'landing'}},
        {path: `${AGENTS_ROUTE}/all`, expected: {view: 'all'}},
        {path: `${AGENTS_ROUTE}/all/extra`, expected: {view: 'all'}},
        {path: `${AGENTS_ROUTE}/all/new`, expected: {view: 'all', create: true}},
        {path: `${AGENTS_ROUTE}/all/new/`, expected: {view: 'all', create: true}},
        {path: `${AGENTS_ROUTE}/all/new/more`, expected: {view: 'all'}},
        {path: `${AGENTS_ROUTE}/chat/bot%201`, expected: {view: 'chat', botId: 'bot 1'}},
        {path: `${AGENTS_ROUTE}/prompts`, expected: {view: 'prompts'}},
        {path: `${AGENTS_ROUTE}/chat/bot1`, expected: {view: 'chat', botId: 'bot1'}},
        {path: `${AGENTS_ROUTE}/chat/bot1/root1`, expected: {view: 'chat', botId: 'bot1', rootId: 'root1'}},
        {path: `${AGENTS_ROUTE}/chat`, expected: {view: 'all'}},
        {path: `${AGENTS_ROUTE}/chat/a/b/c`, expected: {view: 'all'}},
        {path: `${AGENTS_ROUTE}/unknown`, expected: {view: 'all'}},
        {path: `${AGENTS_ROUTE}x/prompts`, expected: {view: 'all'}},
        {path: '/other', expected: {view: 'all'}},
        {path: '', expected: {view: 'all'}},
    ])('$path', ({path, expected}) => {
        expect(parseAgentsRoute(path)).toEqual(expected);
    });
});

describe('useAgentsRoute', () => {
    afterEach(() => {
        delete window.WebappUtils;
    });

    test('reads the initial route, follows history changes, and unsubscribes on unmount', () => {
        const history = makeHistory(`${AGENTS_ROUTE}/prompts`);
        window.WebappUtils = {browserHistory: history} as any;

        const {result, unmount} = renderHook(() => useAgentsRoute());
        expect(result.current.view).toBe('prompts');

        act(() => {
            history.push(buildChatPath('bot 1', 'root1'));
        });
        expect(result.current).toMatchObject({view: 'chat', botId: 'bot 1', rootId: 'root1'});

        unmount();
        expect(history.listeners.size).toBe(0);
    });

    test('navigation helpers push the expected paths', () => {
        const history = makeHistory(AGENTS_ROUTE);
        window.WebappUtils = {browserHistory: history} as any;

        const {result} = renderHook(() => useAgentsRoute());
        act(() => result.current.toPrompts());
        act(() => result.current.toChat('bot1'));
        act(() => result.current.toChat('bot1', 'root1'));
        act(() => result.current.toAll());

        expect(history.push.mock.calls.map((c) => c[0])).toEqual([
            `${AGENTS_ROUTE}/prompts`,
            `${AGENTS_ROUTE}/chat/bot1`,
            `${AGENTS_ROUTE}/chat/bot1/root1`,
            `${AGENTS_ROUTE}/all`,
        ]);
        expect(result.current.view).toBe('all');
    });

    test('toNewAgent pushes the create route, closeNewAgent replaces it with the list', () => {
        const history = makeHistory(AGENTS_ROUTE, true);
        window.WebappUtils = {browserHistory: history} as any;

        const {result} = renderHook(() => useAgentsRoute());
        act(() => result.current.toNewAgent());
        expect(history.push).toHaveBeenCalledWith(`${AGENTS_ROUTE}/all/new`);
        expect(result.current).toMatchObject({view: 'all', create: true});

        act(() => result.current.closeNewAgent());
        expect(history.replace).toHaveBeenCalledWith(`${AGENTS_ROUTE}/all`);
        expect(history.push).toHaveBeenCalledTimes(1);
        expect(result.current.create).toBeUndefined();
    });

    test('closeNewAgent falls back to push when the history has no replace', () => {
        const history = makeHistory(`${AGENTS_ROUTE}/all/new`);
        window.WebappUtils = {browserHistory: history} as any;
        const {result} = renderHook(() => useAgentsRoute());
        act(() => result.current.closeNewAgent());
        expect(history.push).toHaveBeenCalledWith(`${AGENTS_ROUTE}/all`);
    });

    test('opens on the landing at the product root', () => {
        window.WebappUtils = {browserHistory: makeHistory(AGENTS_ROUTE)} as any;
        const {result} = renderHook(() => useAgentsRoute());
        expect(result.current.view).toBe('landing');
    });

    test('falls back to all agents without a host history', () => {
        const {result} = renderHook(() => useAgentsRoute());
        expect(result.current.view).toBe('all');
        expect(() => act(() => result.current.toPrompts())).not.toThrow();
    });
});
