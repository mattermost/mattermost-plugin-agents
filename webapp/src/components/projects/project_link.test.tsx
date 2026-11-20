// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {createEvent, fireEvent, render, screen} from '@testing-library/react';

import ProjectLink from './project_link';

describe('ProjectLink', () => {
    const original = window.WebappUtils;
    afterEach(() => {
        window.WebappUtils = original;
    });

    const click = () => {
        const link = screen.getByTestId('link');
        const event = createEvent.click(link);
        fireEvent(link, event);
        return event;
    };

    test('routes an app path through the host router', () => {
        const push = jest.fn();
        window.WebappUtils = {browserHistory: {push}} as unknown as typeof window.WebappUtils;
        render(
            <ProjectLink
                path='/team/channels/board'
                testId='link'
            >
                {'Board'}
            </ProjectLink>,
        );

        expect(click().defaultPrevented).toBe(true);
        expect(push).toHaveBeenCalledWith('/team/channels/board');
    });

    test('lets the browser follow the link when the host router is missing', () => {
        Reflect.deleteProperty(window, 'WebappUtils');
        render(
            <ProjectLink
                path='/team/channels/board'
                testId='link'
            >
                {'Board'}
            </ProjectLink>,
        );

        expect(click().defaultPrevented).toBe(false);
    });
});
