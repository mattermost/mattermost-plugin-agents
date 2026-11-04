// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';

import {Switch} from '@mattermost/compass-ui/components/switch';

type ToggleSwitchSize = 'small' | 'medium';

type ToggleSwitchProps = {
    checked: boolean;
    onChange: (checked: boolean) => void;
    disabled?: boolean;
    size?: ToggleSwitchSize;
    ariaLabel?: string;
};

export const ToggleSwitch = ({checked, onChange, disabled, size = 'medium', ariaLabel}: ToggleSwitchProps) => (
    <InlineSwitch
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        disabled={disabled}
        size={size}
        aria-label={ariaLabel}
    />
);

// Compass Switch fills its row to fit a label; these toggles have none.
const InlineSwitch = styled(Switch)`
    && {
        width: auto;
        flex-shrink: 0;
        align-self: center;
    }
`;
