// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';
import {useSelector, useDispatch} from 'react-redux';

// Stub heavy modules pulled in transitively. `system_console/bot` imports
// `avatar.tsx`, which imports a PNG asset that jest can't transform.
jest.mock('@/components/system_console/bot', () => ({
    ChannelAccessLevel: {All: 0, Allow: 1, Block: 2, None: 3},
    UserAccessLevel: {All: 0, Allow: 1, Block: 2, None: 3},
}));

// eslint-disable-next-line import/first
import CustomPromptsDropdown from './custom_prompts_dropdown';
// eslint-disable-next-line import/first
import {LLMBot} from '@/bots';
// eslint-disable-next-line import/first
import {getCustomPrompts, getDefaultBotID, getSelectedBotId} from '@/selectors';
// eslint-disable-next-line import/first
import {SelectedBotIdHandler} from '@/redux';
// eslint-disable-next-line import/first
import manifest from '@/manifest';

const ChannelAccessLevelAll = 0;
const UserAccessLevelAll = 0;

// Stub the bot selector so we can assert on the bot it received without
// pulling the entire agents UI graph (which transitively imports PNG assets
// and the host web app) into this unit test.
jest.mock('@/components/bot_selector', () => ({
    __esModule: true,
    DropdownBotSelector: ({activeBot}: {activeBot: {displayName: string} | null}) => (
        <div data-testid='active-bot'>{activeBot ? activeBot.displayName : 'none'}</div>
    ),
}));

jest.mock('@/mm_webapp', () => ({
    AdvancedTextEditor: () => null,
    CreatePost: () => null,
    PostMessagePreview: () => null,
}));

jest.mock('react-intl', () => ({
    FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => <>{defaultMessage}</>,
    IntlProvider: ({children}: {children: React.ReactNode}) => <>{children}</>,
    useIntl: () => ({formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage}),
}));

jest.mock('react-redux', () => ({
    useSelector: jest.fn(),
    useDispatch: jest.fn(),
}));

jest.mock('@/client', () => ({
    renderCustomPrompt: jest.fn(),
    getProfilePictureUrl: () => 'http://localhost/picture.png',
    getCustomPrompts: jest.fn(),
    getCustomPromptPins: jest.fn(),
}));

const mockUseSelector = useSelector as unknown as jest.Mock;
const mockUseDispatch = useDispatch as unknown as jest.Mock;
const mockDispatch = jest.fn();

function makeBot(overrides: Partial<LLMBot>): LLMBot {
    return {
        id: 'bot-id',
        displayName: 'Bot',
        username: 'bot',
        lastIconUpdate: 0,
        dmChannelID: '',
        channelAccessLevel: ChannelAccessLevelAll as LLMBot['channelAccessLevel'],
        channelIDs: [],
        userAccessLevel: UserAccessLevelAll as LLMBot['userAccessLevel'],
        userIDs: [],
        teamIDs: [],
        enabledMCPTools: [],
        autoEnableNewMCPTools: false,
        ...overrides,
    };
}

interface TestState {
    bots: LLMBot[];
    defaultBotID: string;
    selectedBotId: string | null;
}

function selectFromState(state: TestState, selector: any): any {
    const pluginState = {
        customPrompts: [],
        pinnedPromptIds: [],
        showCustomPromptsModal: false,
        bots: state.bots,
        defaultBotID: state.defaultBotID,
        selectedBotId: state.selectedBotId,
    };

    const fakeGlobal = {
        [`plugins-${manifest.id}`]: pluginState,
    } as any;

    // Support the inline selector used inside the component for bots and
    // the named selectors for everything else.
    if (selector === getCustomPrompts) {
        return [];
    }
    if (selector === getDefaultBotID) {
        return state.defaultBotID;
    }
    if (selector === getSelectedBotId) {
        return state.selectedBotId;
    }
    return selector(fakeGlobal);
}

function renderDropdown(state: TestState) {
    mockUseSelector.mockImplementation((selector) => selectFromState(state, selector));
    mockUseDispatch.mockReturnValue(mockDispatch);

    return render(
        <CustomPromptsDropdown
            draft={{}}
            getSelectedText={() => ({start: 0, end: 0})}
            updateText={jest.fn()}
            channelId='channel-1'
            isRHS={false}
        />,
    );
}

beforeEach(() => {
    jest.clearAllMocks();
});

describe('CustomPromptsDropdown bot selection (MM-68856)', () => {
    const aira = makeBot({id: 'aira-id', username: 'aira', displayName: 'Aira'});
    const matty = makeBot({id: 'matty-id', username: 'matty', displayName: 'Matty'});
    const zorro = makeBot({id: 'zorro-id', username: 'zorro', displayName: 'Zorro'});

    test('pre-selects the system-wide default agent when one is configured', () => {
        // The order intentionally puts a non-default bot first to exercise
        // the bug: previously the dropdown showed bots[0] ("Aira") even when
        // the admin had configured Matty as the system default.
        renderDropdown({
            bots: [aira, matty, zorro],
            defaultBotID: matty.id,
            selectedBotId: null,
        });

        expect(screen.getByTestId('active-bot').textContent).toBe('Matty');
    });

    test('falls back to the first bot when no default is configured', () => {
        renderDropdown({
            bots: [aira, matty, zorro],
            defaultBotID: '',
            selectedBotId: null,
        });

        expect(screen.getByTestId('active-bot').textContent).toBe('Aira');
    });

    test('falls back to the first bot when defaultBotID does not match any visible bot', () => {
        // E.g. the configured default is restricted from this user.
        renderDropdown({
            bots: [aira, zorro],
            defaultBotID: 'missing-default-id',
            selectedBotId: null,
        });

        expect(screen.getByTestId('active-bot').textContent).toBe('Aira');
    });

    test('honors the user-selected bot over the system default', () => {
        renderDropdown({
            bots: [aira, matty, zorro],
            defaultBotID: matty.id,
            selectedBotId: zorro.id,
        });

        expect(screen.getByTestId('active-bot').textContent).toBe('Zorro');
    });

    test('dispatches the default bot id when no selection exists yet', () => {
        renderDropdown({
            bots: [aira, matty, zorro],
            defaultBotID: matty.id,
            selectedBotId: null,
        });

        expect(mockDispatch).toHaveBeenCalledWith({
            type: SelectedBotIdHandler,
            botId: matty.id,
        });
    });
});
