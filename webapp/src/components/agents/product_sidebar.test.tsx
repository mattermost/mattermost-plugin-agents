// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';

import ProductSidebar from './product_sidebar';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const real = actual.createIntl({locale: 'en', defaultLocale: 'en', onError: () => null});
    const intl = {
        formatMessage: (descriptor: {id?: string; defaultMessage: string}, values?: Record<string, string | number>) =>
            real.formatMessage({id: descriptor.id ?? descriptor.defaultMessage, ...descriptor}, values),
    };
    return {...actual, useIntl: () => intl};
});

const renderSidebar = (props: Partial<React.ComponentProps<typeof ProductSidebar>> = {}) => {
    const onSelectAll = jest.fn();
    const onSelectPrompts = jest.fn();
    render(
        <ProductSidebar
            activeView='all'
            onSelectAll={onSelectAll}
            onSelectPrompts={onSelectPrompts}
            {...props}
        />,
    );
    return {onSelectAll, onSelectPrompts};
};

describe('ProductSidebar', () => {
    test('exposes a labelled landmark with the two nav rows as buttons', () => {
        renderSidebar();
        expect(screen.getAllByRole('navigation', {name: 'Agents'})).toHaveLength(1);
        expect(screen.getByRole('button', {name: /All agents/})).toBeTruthy();
        expect(screen.getByRole('button', {name: /Custom Prompts/})).toBeTruthy();
    });

    test('rows invoke their handlers', () => {
        const {onSelectAll, onSelectPrompts} = renderSidebar({activeView: 'prompts'});
        fireEvent.click(screen.getByRole('button', {name: /Custom Prompts/}));
        fireEvent.click(screen.getByRole('button', {name: /All agents/}));
        expect(onSelectPrompts).toHaveBeenCalledTimes(1);
        expect(onSelectAll).toHaveBeenCalledTimes(1);
    });

    test('renders the extension point children', () => {
        renderSidebar({children: <div>{'Your agents section'}</div>});
        expect(screen.getByText('Your agents section')).toBeTruthy();
    });
});
