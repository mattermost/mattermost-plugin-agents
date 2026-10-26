// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, render, screen} from '@testing-library/react';

import AgentsPage, {AGENTS_ROUTE, ContentRenderers} from './agents_page';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const real = actual.createIntl({locale: 'en', defaultLocale: 'en', onError: () => null});
    const intl = {
        formatMessage: (descriptor: {id?: string; defaultMessage: string}, values?: Record<string, string | number>) =>
            real.formatMessage({id: descriptor.id ?? descriptor.defaultMessage, ...descriptor}, values),
    };
    return {...actual, useIntl: () => intl};
});

jest.mock('./agents_list', () => ({__esModule: true, default: () => <div>{'agents list'}</div>}));

const makeHistory = (initial: string) => {
    const listeners = new Set<() => void>();
    const history = {
        location: {pathname: initial},
        push: jest.fn((path: string) => {
            history.location = {pathname: path};
            listeners.forEach((l) => l());
        }),
        listen: (l: () => void) => {
            listeners.add(l);
            return () => listeners.delete(l);
        },
    };
    return history;
};

const renderPage = (renderers?: ContentRenderers) => render(
    <AgentsPage renderers={renderers}/>,
);

describe('AgentsPage', () => {
    afterEach(() => {
        delete window.WebappUtils;
    });

    test('opens on All agents and navigates via the sidebar', () => {
        const history = makeHistory(AGENTS_ROUTE);
        window.WebappUtils = {browserHistory: history} as any;
        const renderers: ContentRenderers = {
            all: () => <div>{'all view'}</div>,
            prompts: () => <div>{'prompts view'}</div>,
            chat: (r) => <div>{`chat ${r.botId}`}</div>,
        };
        renderPage(renderers);
        expect(screen.getByText('all view')).toBeTruthy();

        act(() => screen.getByRole('button', {name: /Custom Prompts/}).click());
        expect(history.push).toHaveBeenCalledWith(`${AGENTS_ROUTE}/prompts`);
        expect(screen.getByText('prompts view')).toBeTruthy();

        act(() => history.push(`${AGENTS_ROUTE}/chat/bot1`));
        expect(screen.getByText('chat bot1')).toBeTruthy();
    });

    test('renders the agents list for the all view by default', () => {
        window.WebappUtils = {browserHistory: makeHistory(AGENTS_ROUTE)} as any;
        renderPage();
        expect(screen.getByText('agents list')).toBeTruthy();
    });
});
