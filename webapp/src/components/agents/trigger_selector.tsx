// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useMemo} from 'react';
import styled from 'styled-components';
import {useIntl} from 'react-intl';
import {
    AccountMultiplePlusOutlineIcon,
    AccountPlusOutlineIcon,
    ClockOutlineIcon,
    MessagePlusOutlineIcon,
    MessageTextOutlineIcon,
} from '@mattermost/compass-icons/components';
import {GroupBase} from 'react-select';

import {BaseSelectOption, SingleSelect} from '@/components/select';
import {TriggerType} from '@/types/automations';

type TriggerOption = BaseSelectOption & {
    icon: React.ReactNode;
};

type Props = {
    value: TriggerType;
    onChange: (value: TriggerType) => void;
};

const TriggerSelector = ({value, onChange}: Props) => {
    const intl = useIntl();

    const options = useMemo((): Array<GroupBase<TriggerOption>> => [
        {
            label: intl.formatMessage({defaultMessage: 'Schedule'}),
            options: [
                {
                    value: 'schedule',
                    label: intl.formatMessage({defaultMessage: 'At a scheduled date and time'}),
                    icon: <ClockOutlineIcon size={18}/>,
                },
            ],
        },
        {
            label: intl.formatMessage({defaultMessage: 'Channels'}),
            options: [
                {
                    value: 'message_posted',
                    label: intl.formatMessage({defaultMessage: 'A message is posted in a channel'}),
                    icon: <MessageTextOutlineIcon size={18}/>,
                },
                {
                    value: 'membership_changed',
                    label: intl.formatMessage({defaultMessage: 'Someone joins a channel'}),
                    icon: <AccountPlusOutlineIcon size={18}/>,
                },
                {
                    value: 'channel_created',
                    label: intl.formatMessage({defaultMessage: 'A new channel is created'}),
                    icon: <MessagePlusOutlineIcon size={18}/>,
                },
            ],
        },
        {
            label: intl.formatMessage({defaultMessage: 'Teams'}),
            options: [
                {
                    value: 'user_joined_team',
                    label: intl.formatMessage({defaultMessage: 'Someone joins a team'}),
                    icon: <AccountMultiplePlusOutlineIcon size={18}/>,
                },
            ],
        },
    ], [intl]);

    const flatOptions = useMemo(
        () => options.flatMap((group) => group.options),
        [options],
    );

    const selected = useMemo(
        () => flatOptions.find((option) => option.value === value) ?? flatOptions[0] ?? null,
        [flatOptions, value],
    );

    const handleChange = useCallback((option: TriggerOption | null) => {
        if (option) {
            onChange(option.value as TriggerType);
        }
    }, [onChange]);

    const formatOptionLabel = useCallback((option: TriggerOption) => (
        <OptionContent>
            {option.icon}
            {option.label}
        </OptionContent>
    ), []);

    return (
        <SingleSelect<TriggerOption>
            value={selected}
            options={options}
            onChange={handleChange}
            formatOptionLabel={formatOptionLabel}
            isSearchable={false}
            aria-label={intl.formatMessage({defaultMessage: 'What starts the automation?'})}
        />
    );
};

const OptionContent = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
`;

export default TriggerSelector;
