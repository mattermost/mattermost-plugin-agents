// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';

import AgentsPage from './agents_page';

jest.mock('@/manifest', () => ({
    __esModule: true,
    default: {id: 'mattermost-ai'},
}), {virtual: true});

jest.mock('./agents_license_gate', () => ({
    __esModule: true,
    default: ({children}: {children: React.ReactNode}) => <>{children}</>,
}));

jest.mock('./agents_list', () => ({
    __esModule: true,
    default: () => <div data-testid='agents-list'/>,
}));

describe('AgentsPage', () => {
    beforeEach(() => {
        document.body.classList.remove('app__body');
        document.body.innerHTML = '<div id="root"></div>';
    });

    afterEach(() => {
        document.body.classList.remove('app__body');
        document.body.innerHTML = '';
    });

    test('adds app classes on mount and leaves them after unmount', () => {
        expect(document.body.classList.contains('app__body')).toBe(false);
        expect(document.getElementById('root')?.classList.contains('channel-view')).toBe(false);

        const {unmount} = render(<AgentsPage/>);

        expect(screen.getByTestId('agents-list')).not.toBeNull();
        expect(document.body.classList.contains('app__body')).toBe(true);
        expect(document.getElementById('root')?.classList.contains('channel-view')).toBe(true);

        unmount();

        expect(document.body.classList.contains('app__body')).toBe(true);
        expect(document.getElementById('root')?.classList.contains('channel-view')).toBe(true);
    });
});
