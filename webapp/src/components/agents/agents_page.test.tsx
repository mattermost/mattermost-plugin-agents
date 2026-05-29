// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render} from '@testing-library/react';

import AgentsPage from './agents_page';

jest.mock('./agents_license_gate', () => ({
    __esModule: true,
    default: ({children}: {children: React.ReactNode}) => <>{children}</>,
}));

jest.mock('./agents_list', () => ({
    __esModule: true,
    default: () => <div>Agents List</div>,
}));

describe('AgentsPage', () => {
    let root: HTMLDivElement;

    beforeEach(() => {
        document.body.className = '';
        root = document.createElement('div');
        root.id = 'root';
        root.className = '';
        document.body.appendChild(root);
    });

    afterEach(() => {
        root.remove();
    });

    it('does not toggle app__body on document.body', () => {
        render(<AgentsPage />);
        expect(document.body.classList.contains('app__body')).toBe(false);
    });

    it('adds channel-view to #root when missing', () => {
        render(<AgentsPage />);
        expect(root.classList.contains('channel-view')).toBe(true);
    });
});
