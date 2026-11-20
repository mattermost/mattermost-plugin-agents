// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';

import {chooseOption, getSelect, selectedLabel} from '../../../../tests/compass_select';

import {PluginConfig} from '@/components/system_console/plugin_config_types';

import {defaultConfig} from './settings_sections';
import ProjectChannelsSettings from './project_channels_settings';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const format = ({defaultMessage}: {defaultMessage: string}) => defaultMessage;
    return {
        ...actual,
        useIntl: () => ({formatMessage: format}),
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => <>{defaultMessage}</>,
    };
});

const services = [
    {id: 'svc-a', name: 'Service A'},
    {id: 'svc-b', name: 'Service B'},
] as PluginConfig['services'];

function renderSettings(overrides: Partial<PluginConfig> = {}) {
    const updateConfig = jest.fn();
    render(
        <ProjectChannelsSettings
            value={{...defaultConfig, services, ...overrides}}
            updateConfig={updateConfig}
        />,
    );
    return updateConfig;
}

describe('ProjectChannelsSettings', () => {
    test('is off by default, marked experimental, and shows no further settings', () => {
        const updateConfig = renderSettings();

        expect(defaultConfig.enableProjectChannels).toBe(false);
        expect(screen.getByText('EXPERIMENTAL')).not.toBeNull();
        expect(((screen.getAllByRole('radio') as HTMLInputElement[])[1]).checked).toBe(true);
        expect(screen.queryByRole('combobox')).toBeNull();
        expect(screen.queryByText('Project agent model')).toBeNull();
        expect(updateConfig).not.toHaveBeenCalled();
    });

    test('turning it on saves enableProjectChannels', () => {
        const updateConfig = renderSettings();

        fireEvent.click(screen.getAllByRole('radio')[0]);

        expect(updateConfig).toHaveBeenCalledWith({enableProjectChannels: true});
    });

    test('when on, offers the configured services and saves the chosen one', () => {
        const updateConfig = renderSettings({enableProjectChannels: true});

        chooseOption(getSelect('Project agent service'), 'Service B');

        expect(updateConfig).toHaveBeenCalledWith({projectDefaultServiceID: 'svc-b'});
    });

    test('shows the saved service and the optional model', () => {
        const updateConfig = renderSettings({enableProjectChannels: true, projectDefaultServiceID: 'svc-a', projectDefaultModel: 'gpt-x'});

        expect(selectedLabel(getSelect('Project agent service'))).toBe('Service A');
        const model = screen.getByDisplayValue('gpt-x');
        fireEvent.change(model, {target: {value: 'gpt-y'}});

        expect(updateConfig).toHaveBeenCalledWith({projectDefaultModel: 'gpt-y'});
    });

    test('warns when the saved service no longer exists', () => {
        renderSettings({enableProjectChannels: true, projectDefaultServiceID: 'deleted'});

        expect(screen.getByText('The selected service no longer exists. Project channels stay off until you choose another.')).not.toBeNull();
    });
});
