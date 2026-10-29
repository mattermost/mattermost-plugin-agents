// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {act, render, screen} from '@testing-library/react';

import AgentNewChat, {isLightSurface, NEW_CHAT_GREETINGS, NewChatBackdrop, orbStrength} from './agent_new_chat';

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

    // The orbs swing past their layer; unclipped they extend the parent's scrollable overflow, and
    // scrolling something into view (expanding a panel in a reply) scrolls the pane and hides the header.
    test('the orb layer clips its overflow so it cannot make the parent scrollable', () => {
        const {container} = render(
            <Provider store={store}>
                <NewChatBackdrop visible={false}/>
            </Provider>,
        );
        const layer = container.querySelector('[aria-hidden="true"]');
        expect(layer).not.toBeNull();
        expect(getComputedStyle(layer as Element).overflow).toBe('clip');
    });

    describe('theme-dependent orb opacity', () => {
        const surface = (background: string) => {
            const el = document.createElement('div');
            el.style.backgroundColor = background;
            document.body.appendChild(el);
            return el;
        };
        afterEach(() => {
            document.body.innerHTML = '';
        });

        test.each([
            {name: 'a light theme', background: 'rgb(255, 255, 255)', light: true},
            {name: 'a dark theme', background: 'rgb(25, 27, 31)', light: false},

            // No background: the dim setting is the safe failure (colorSchemeFor itself answers "light"
            // for anything it cannot parse, which must not reach the orbs).
            {name: 'a transparent surface', background: 'rgba(0, 0, 0, 0)', light: false},
            {name: 'no background at all', background: '', light: false},
        ])('$name is read as light: $light', ({background, light}) => {
            expect(isLightSurface(surface(background))).toBe(light);
            expect(isLightSurface(null)).toBe(false);
        });

        const accent = {size: '1cqmax', top: '0', left: '0', strength: 10, dx: 0, dy: 0, scaleFrom: 1, scaleTo: 1, xDuration: 1, yDuration: 1};
        test.each([
            {name: 'an accent orb on a dark theme', orb: accent, light: false, expected: 10},
            {name: 'an accent orb gets a slight boost on a light theme', orb: accent, light: true, expected: 12},
            {name: 'a surface orb is dim on a dark theme', orb: {...accent, surface: true, strength: 3, lightStrength: 90}, light: false, expected: 3},
            {name: 'a surface orb is near-solid on a light theme', orb: {...accent, surface: true, strength: 3, lightStrength: 90}, light: true, expected: 90},
            {name: 'a surface orb without a light value keeps its strength', orb: {...accent, surface: true, strength: 3}, light: true, expected: 3},
        ])('$name', ({orb, light, expected}) => {
            expect(orbStrength(orb, light)).toBeCloseTo(expected);
        });

        // Through the component: the colors follow the pane's background, and a theme change re-reads it.
        test('the surface orbs are re-read when the theme preference changes', () => {
            let theme = 'dark';
            const themeStore = createStore((state: any = {entities: {preferences: {myPreferences: {'theme--': {value: 'dark'}}}}}, action: any) => (
                action.type === 'SET_THEME' ? {entities: {preferences: {myPreferences: {'theme--': {value: action.value}}}}} : state
            ));

            // The backdrop reads its parent's background, here the render container's.
            const pane = document.createElement('div');
            pane.style.backgroundColor = 'rgb(25, 27, 31)';
            document.body.appendChild(pane);
            const {container} = render(
                <Provider store={themeStore}>
                    <NewChatBackdrop visible={true}/>
                </Provider>,
                {container: pane},
            );
            const surfaceOrbColor = () => {
                const orbs = Array.from(container.querySelectorAll('[aria-hidden="true"] > span > span'));
                return getComputedStyle(orbs[orbs.length - 1]).getPropertyValue('--orb-color');
            };
            expect(surfaceOrbColor()).toContain('3%');

            theme = 'light';
            pane.style.backgroundColor = 'rgb(255, 255, 255)';
            act(() => {
                themeStore.dispatch({type: 'SET_THEME', value: theme});
            });
            expect(surfaceOrbColor()).toMatch(/\b(85|90|95)%/);
        });
    });
});
