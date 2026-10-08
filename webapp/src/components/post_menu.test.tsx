// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {Post} from '@mattermost/types/posts';

import PostMenu from './post_menu';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {
        ...actual,
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
        }),
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

jest.mock('@/license', () => ({
    useIsLicensedFor: jest.fn(() => true),
}));

jest.mock('@/hooks', () => ({
    useSelectPost: () => jest.fn(),
}));

jest.mock('@/bots', () => ({
    useBotlistForChannel: () => ({
        bots: [{username: 'matty', displayName: 'Matty'}],
        activeBot: {username: 'matty', displayName: 'Matty'},
        setActiveBot: jest.fn(),
    }),
}));

jest.mock('../client', () => ({
    doReaction: jest.fn(),
    doThreadAnalysis: jest.fn(),
}));

jest.mock('./dot_menu', () => ({
    __esModule: true,
    default: ({
        children,
        size,
        padding,
        title,
        testId,
    }: {
        children: React.ReactNode;
        size?: string;
        padding?: string;
        title?: string;
        testId?: string;
    }) => (
        <div>
            <button
                type='button'
                data-testid={testId}
                data-size={size}
                data-padding={padding}
                aria-label={title}
            />
            {children}
        </div>
    ),
    DropdownMenu: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
    DropdownMenuItem: ({label}: {label: React.ReactNode}) => <button type='button'>{label}</button>,
}));

jest.mock('./bot_selector', () => ({
    DropdownBotSelector: () => <div>{'bot-selector'}</div>,
}));

const post = {id: 'post1', channel_id: 'chan1'} as Post;

describe('PostMenu license gating', () => {
    const {useIsLicensedFor} = jest.requireMock('@/license') as {useIsLicensedFor: jest.Mock};

    beforeEach(() => {
        useIsLicensedFor.mockReturnValue(true);
    });

    test('shows thread summarization actions at Professional', () => {
        render(
            <IntlProvider locale='en'>
                <PostMenu post={post}/>
            </IntlProvider>,
        );

        expect(screen.getByText('Summarize Thread')).not.toBeNull();
        expect(screen.getByText('Find action items')).not.toBeNull();
        expect(screen.getByText('Find open questions')).not.toBeNull();
        expect(screen.getByText('React for me')).not.toBeNull();
    });

    test('hides thread summarization actions below Professional and keeps React for me', () => {
        useIsLicensedFor.mockReturnValue(false);

        render(
            <IntlProvider locale='en'>
                <PostMenu post={post}/>
            </IntlProvider>,
        );

        expect(screen.queryByText('Summarize Thread')).toBeNull();
        expect(screen.queryByText('Find action items')).toBeNull();
        expect(screen.queryByText('Find open questions')).toBeNull();
        expect(screen.getByText('React for me')).not.toBeNull();
    });
});

describe('PostMenu sizing', () => {
    test('uses small + compact IconButton to match host post-menu actions (28px)', () => {
        render(
            <IntlProvider locale='en'>
                <PostMenu post={post}/>
            </IntlProvider>,
        );

        const button = screen.getByTestId('ai-actions-menu');
        expect(button.getAttribute('data-size')).toBe('small');
        expect(button.getAttribute('data-padding')).toBe('compact');
        expect(button.getAttribute('aria-label')).toBe('AI Actions');
        expect(button.getAttribute('title')).toBeNull();
    });
});
