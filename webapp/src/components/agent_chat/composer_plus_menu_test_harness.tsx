// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Shared by the ComposerPlusMenu tests: the plugin's real reducers in a store,
// and a stand-in composer that owns the text the menu reads and writes.

import React, {useCallback, useState} from 'react';
import {Provider} from 'react-redux';
import {applyMiddleware, combineReducers, createStore, type Middleware, type Reducer} from 'redux';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';

import type {LLMBot} from '@/bots';
import manifest from '@/manifest';
import {setupRedux} from '@/redux';

import ComposerPlusMenu, {type ComposerPlusMenuProps} from './composer_plus_menu';

export const bot = {
    id: 'bot1',
    username: 'matty',
    displayName: 'Matty',
    dmChannelID: 'dm1',
    lastIconUpdate: 0,
    enabledMCPTools: null,
    autoEnableNewMCPTools: true,
} as unknown as LLMBot;

const thunk: Middleware = ({dispatch, getState}) => (next) => (action) => (
    typeof action === 'function' ? action(dispatch, getState) : next(action)
);

export function makeStore() {
    let pluginReducer: Reducer | null = null;
    setupRedux(
        {registerReducer: (r: Reducer) => {
            pluginReducer = r;
        }},
        {getState: () => ({entities: {general: {license: {}}}}), dispatch: () => null} as never,
    );
    const entities = {general: {config: {}, license: {}}, users: {currentUserId: 'user1'}};
    return createStore(combineReducers({
        entities: (state = entities) => state,
        [`plugins-${manifest.id}`]: pluginReducer as unknown as Reducer,
    }), applyMiddleware(thunk));
}

type HarnessProps = Partial<Omit<ComposerPlusMenuProps, 'getText' | 'setText'>> & {
    initialText?: string;
    onSetText: (text: string) => void;
};

const Composer = ({initialText = '', onSetText, ...props}: HarnessProps) => {
    const [text, setTextState] = useState(initialText);
    const getText = useCallback(() => text, [text]);
    const setText = useCallback((next: string) => {
        setTextState(next);
        onSetText(next);
    }, [onSetText]);
    return (
        <>
            <output data-testid='composer-text'>{text}</output>
            <ComposerPlusMenu
                bot={bot}
                channelId='dm1'
                rootId=''
                uploadEnabled={true}
                onUploadFiles={() => null}
                onError={() => null}
                {...props}
                getText={getText}
                setText={setText}
            />
        </>
    );
};

export function renderMenu(props: Partial<HarnessProps> = {}) {
    const store = makeStore();
    const handlers = {
        onSetText: jest.fn(),
        onUploadFiles: jest.fn(),
        onError: jest.fn(),
        onRewritingChange: jest.fn(),
    };
    const result = render(
        <Provider store={store}>
            <Composer
                {...handlers}
                {...props}
            />
        </Provider>,
    );
    return {...handlers, store, ...result};
}

// By test id: while the menu is open its focus manager hides the rest of the page.
export const trigger = () => screen.getByTestId('composer-plus-menu-button');
export const composerText = () => screen.getByTestId('composer-text').textContent;

export async function openMenu() {
    fireEvent.click(trigger());
    const menu = await screen.findByRole('menu', {name: 'Add to message'});

    // The focus manager moves focus onto the first item a frame after opening.
    await waitFor(() => expect(menu.contains(document.activeElement)).toBe(true));
}

// Opens a submenu by its row (by click, which also focuses its first item).
export async function openSubmenu(name: string) {
    await openMenu();
    fireEvent.click(screen.getByRole('menuitem', {name: new RegExp(`^${name}`)}));
    return screen.findByRole('menu', {name});
}

// The menu stays mounted until its exit transition ends.
export async function waitForMenuClosed() {
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull());
}
