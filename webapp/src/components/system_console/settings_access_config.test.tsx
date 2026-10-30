// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {getSettingsAccess, saveSettingsAccess} from '@/client';

import SettingsAccessConfig from './settings_access_config';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');

    // A stable intl instance: the page keys effects on [intl].
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
    return {
        ...actual,
        useIntl: () => intl,
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

jest.mock('@/client', () => ({
    getSettingsAccess: jest.fn(),
    saveSettingsAccess: jest.fn(),
}));

// The picker is not under test: show the IDs it holds and let a test pick.
jest.mock('../select', () => ({
    SelectUsers: ({userIDs, onChangeUserIDs, placeholder}: {userIDs: string[]; onChangeUserIDs: (ids: string[]) => void; placeholder: string}) => (
        <div>
            <span>{`${placeholder}: ${userIDs.join(',') || 'nobody'}`}</span>
            <button onClick={() => onChangeUserIDs([...userIDs, 'bob'])}>{`add bob (${placeholder})`}</button>
        </div>
    ),
}));

type SaveAction = () => Promise<{error?: {message?: string}}>;

const READ_WRITE = 'Search for people to give read and write access';
const READ_ONLY = 'Search for people to give read-only access';

function renderConfig() {
    const registerSaveAction = jest.fn();
    const setSaveNeeded = jest.fn();
    render(
        <IntlProvider
            locale='en'
            onError={() => null}
        >
            <SettingsAccessConfig
                id='Config'
                label=''
                helpText={null}
                value={null}
                disabled={false}
                config={{}}
                currentState={{}}
                license={{}}
                setByEnv={false}
                onChange={jest.fn()}
                setSaveNeeded={setSaveNeeded}
                registerSaveAction={registerSaveAction}
                unRegisterSaveAction={jest.fn()}
            />
        </IntlProvider>,
    );
    const save = async () => {
        const action = registerSaveAction.mock.calls.at(-1)?.[0] as SaveAction;
        let result: {error?: {message?: string}} = {};
        await act(async () => {
            result = await action();
        });
        return result;
    };
    return {save, setSaveNeeded};
}

describe('SettingsAccessConfig', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        (getSettingsAccess as jest.Mock).mockResolvedValue({readOnlyUserIDs: ['bob'], readWriteUserIDs: ['alice']});
        (saveSettingsAccess as jest.Mock).mockImplementation(async (access) => access);
    });

    it('shows both lists and saves only them through the host Save button', async () => {
        const {save, setSaveNeeded} = renderConfig();
        await screen.findByText(`${READ_WRITE}: alice`);
        expect(screen.getByText(`${READ_ONLY}: bob`)).toBeTruthy();

        // Giving bob read/write takes him off the read-only list.
        fireEvent.click(screen.getByRole('button', {name: `add bob (${READ_WRITE})`}));
        expect(setSaveNeeded).toHaveBeenCalled();
        expect(screen.getByText(`${READ_ONLY}: nobody`)).toBeTruthy();

        expect(await save()).toEqual({});
        expect(saveSettingsAccess).toHaveBeenCalledWith({readOnlyUserIDs: [], readWriteUserIDs: ['alice', 'bob']});
    });

    it('reports a failed save to the host', async () => {
        (saveSettingsAccess as jest.Mock).mockRejectedValue(new Error('403'));
        const {save} = renderConfig();
        await screen.findByText(`${READ_WRITE}: alice`);

        expect((await save()).error?.message).toBe('Failed to save who has access to the Agents settings.');
    });

    it('opens the Agents settings page', async () => {
        const push = jest.fn();
        window.WebappUtils = {browserHistory: {push}} as any;
        try {
            renderConfig();
            fireEvent.click(await screen.findByRole('button', {name: 'Open Agents settings'}));
            expect(push).toHaveBeenCalledWith(expect.stringMatching(/\/agents\/settings$/));
        } finally {
            delete window.WebappUtils;
        }
    });
});
