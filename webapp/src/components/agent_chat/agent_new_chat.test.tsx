// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {render, screen} from '@testing-library/react';

import AgentNewChat, {NEW_CHAT_GREETINGS} from './agent_new_chat';

jest.mock('@/client', () => ({
    createPost: jest.fn(),
    getProfilePictureUrl: (id: string) => `/users/${id}/image`,
}));

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, string>) => (
            defaultMessage.replace(/\{(\w+)\}/g, (_m, k) => values?.[k] ?? '')
        ),
    };
    return {...actual, useIntl: () => intl};
});

const store = createStore(() => ({entities: {users: {currentUserId: 'me'}}}));

const bot = {
    id: 'bot1',
    displayName: 'Matty',
    username: 'matty',
    lastIconUpdate: 0,
    dmChannelID: 'dm1',
    channelAccessLevel: 0,
    channelIDs: [],
    userAccessLevel: 0,
    userIDs: [],
    teamIDs: [],
    enabledMCPTools: [],
    autoEnableNewMCPTools: false,
} as any;

const ui = (children?: React.ReactNode) => (
    <Provider store={store}>
        <AgentNewChat
            bot={bot}
            channelId='dm1'
            layout='panel'
            onCreated={jest.fn()}
        >
            {children}
        </AgentNewChat>
    </Provider>
);

const text = (d: {defaultMessage?: unknown}) => d.defaultMessage as string;
const shown = () => ({
    title: screen.getByRole('heading', {level: 2}).textContent,
    subtitle: screen.getByRole('heading', {level: 2}).nextElementSibling?.textContent,
});

describe('AgentNewChat', () => {
    afterEach(() => {
        jest.restoreAllMocks();
    });

    test.each(NEW_CHAT_GREETINGS.map((greeting, index) => ({index, greeting})))(
        'random index $index shows its own title and subtitle together',
        ({index, greeting}) => {
            jest.spyOn(Math, 'random').mockReturnValue((index + 0.5) / NEW_CHAT_GREETINGS.length);
            render(ui());

            const {title, subtitle} = shown();
            expect(title).toBe(text(greeting.title));
            expect(subtitle).toBe(text(greeting.subtitle));

            // Never mixed with another pair's subtitle.
            NEW_CHAT_GREETINGS.forEach((other, j) => {
                if (j !== index) {
                    expect(subtitle).not.toBe(text(other.subtitle));
                }
            });
        },
    );

    test('the pair is stable across re-renders', () => {
        const random = jest.spyOn(Math, 'random').mockReturnValue(0.99);
        const {rerender} = render(ui());
        const first = shown();

        random.mockReturnValue(0);
        rerender(ui(<span>{'extra'}</span>));
        expect(shown()).toEqual(first);
        expect(first.title).toBe(text(NEW_CHAT_GREETINGS[NEW_CHAT_GREETINGS.length - 1].title));
    });

    test('renders the composer and the extra content', () => {
        render(ui(<button>{'Pinned prompt'}</button>));
        expect(screen.getByRole('textbox', {name: 'Message Matty'})).toBeTruthy();
        expect(screen.getByRole('button', {name: 'Pinned prompt'})).toBeTruthy();
    });
});
