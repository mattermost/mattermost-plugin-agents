// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';

import {dismissLegacyMenu, getHostMenuComponents} from './ai_actions_menu_utils';
import AgentsDropdown from './agents/agents_dropdown';

jest.mock('./agents/agents_page', () => ({
    AGENTS_ROUTE: '/plugins/mattermost-ai/agents',
}));

jest.mock('react-intl', () => ({
    FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
}));

describe('ai_actions_menu_utils', () => {
    afterEach(() => {
        delete window.Components;
        document.body.innerHTML = '';
    });

    test('returns host menu components when Mattermost exposes them', () => {
        const Item = jest.fn();
        window.Components = {Menu: {Item}};

        expect(getHostMenuComponents()).toEqual({Item});
    });

    test('dismissLegacyMenu clicks the old backdrop when present', () => {
        const backdrop = document.createElement('button');
        backdrop.id = 'backdropForMenuComponent';
        const clickSpy = jest.spyOn(backdrop, 'click');
        document.body.appendChild(backdrop);

        dismissLegacyMenu();

        expect(clickSpy).toHaveBeenCalled();
    });
});

describe('AgentsDropdown', () => {
    const originalLocation = window.location;

    beforeEach(() => {
        delete (window as Partial<Window>).location;
        window.location = {
            ...originalLocation,
            assign: jest.fn(),
        } as Location;
    });

    afterEach(() => {
        delete window.Components;
        document.body.innerHTML = '';
        window.location = originalLocation;
    });

    test('uses Mattermost host menu items when available', () => {
        const HostMenuItem = jest.fn(({labels, onClick}) => (
            <button
                type='button'
                onClick={onClick}
            >
                {labels}
            </button>
        ));

        window.Components = {
            Menu: {
                Item: HostMenuItem,
            },
        };
        window.WebappUtils = {
            browserHistory: {
                push: jest.fn(),
            },
            sendWebSocketMessage: jest.fn(),
        };

        render(<AgentsDropdown/>);

        fireEvent.click(screen.getByRole('button', {name: 'Manage agents'}));

        expect(HostMenuItem).toHaveBeenCalled();
        expect(window.WebappUtils.browserHistory?.push).toHaveBeenCalledWith('/plugins/mattermost-ai/agents');
    });

    test('falls back to dismissing the legacy menu when host menu items are unavailable', () => {
        delete window.Components;
        window.WebappUtils = {
            browserHistory: {
                push: jest.fn(),
            },
            sendWebSocketMessage: jest.fn(),
        };

        const backdrop = document.createElement('button');
        backdrop.id = 'backdropForMenuComponent';
        const clickSpy = jest.spyOn(backdrop, 'click');
        document.body.appendChild(backdrop);

        render(<AgentsDropdown/>);

        fireEvent.click(screen.getByRole('menuitem', {name: 'Manage agents'}));

        expect(clickSpy).toHaveBeenCalled();
        expect(window.WebappUtils.browserHistory?.push).toHaveBeenCalledWith('/plugins/mattermost-ai/agents');
    });
});
