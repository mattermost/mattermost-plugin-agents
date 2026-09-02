// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {IntlProvider} from 'react-intl';

import {LLMBot} from '@/bots';
import {ChannelAccessLevel, UserAccessLevel} from '@/components/system_console/bot';
import manifest from '@/manifest';

import RHSNewTab from './rhs_new_tab';

jest.mock('react-intl', () => {
    const ReactActual = jest.requireActual<typeof import('react')>('react');

    return {
        IntlProvider: ({children}: {children: React.ReactNode}) => ReactActual.createElement(ReactActual.Fragment, null, children),
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
        }),
    };
});

const mockAdvancedTextEditor = jest.fn();

jest.mock('@/mm_webapp', () => {
    const ReactActual = jest.requireActual<typeof import('react')>('react');

    return {
        AdvancedTextEditor: (props: Record<string, unknown>) => {
            mockAdvancedTextEditor(props);
            return ReactActual.createElement('div', {'data-testid': 'advanced-text-editor'});
        },
        CreatePost: null,
    };
});

jest.mock('@/client', () => ({
    createPost: jest.fn(),
    getBotDirectChannel: jest.fn(() => new Promise(() => {
        // Leave the DM-creation effect pending so this test only covers first paint.
    })),
}));

jest.mock('../assets/rhs_image', () => () => null);

jest.mock('../custom_prompts/rhs_prompt_buttons', () => () => null);

function makeBot(overrides: Partial<LLMBot> = {}): LLMBot {
    return {
        id: 'bot-id',
        displayName: 'Agents',
        username: 'ai',
        lastIconUpdate: 0,
        dmChannelID: 'dm-channel-id',
        channelAccessLevel: ChannelAccessLevel.All,
        channelIDs: [],
        userAccessLevel: UserAccessLevel.All,
        userIDs: [],
        enabledMCPTools: [],
        autoEnableNewMCPTools: false,
        ...overrides,
    };
}

function renderNewTab(activeBot: LLMBot | null) {
    const store = createStore(() => ({
        entities: {
            users: {currentUserId: 'user-id'},
        },
        [`plugins-${manifest.id}`]: {
            bots: activeBot ? [activeBot] : [],
        },
    }));

    return render(
        <Provider store={store}>
            <IntlProvider locale='en'>
                <RHSNewTab
                    selectPost={jest.fn()}
                    setCurrentTab={jest.fn()}
                    activeBot={activeBot}
                />
            </IntlProvider>
        </Provider>,
    );
}

describe('RHSNewTab AdvancedTextEditor', () => {
    beforeEach(() => {
        mockAdvancedTextEditor.mockClear();
    });

    test('passes an empty rootId so the host editor does not loop on undefined', () => {
        renderNewTab(makeBot());

        expect(screen.getByTestId('advanced-text-editor')).toBeTruthy();
        expect(mockAdvancedTextEditor).toHaveBeenCalledWith(expect.objectContaining({
            channelId: 'dm-channel-id',
            rootId: '',
        }));
    });

    test('does not mount the editor until a DM channel exists', () => {
        renderNewTab(makeBot({dmChannelID: ''}));

        expect(screen.queryByTestId('advanced-text-editor')).toBeNull();
        expect(mockAdvancedTextEditor).not.toHaveBeenCalled();
    });
});
