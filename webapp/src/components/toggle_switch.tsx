// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';

type ToggleSwitchSize = 'small' | 'medium';

const SIZES = {
    small: {
        width: 26,
        height: 16,
        thumb: 12,
        borderRadius: 14,
        offset: 2,
        translate: 10,
    },
    medium: {
        width: 32,
        height: 20,
        thumb: 16,
        borderRadius: 10,
        offset: 2,
        translate: 12,
    },
};

const ToggleSwitchContainer = styled.label<{$size: ToggleSwitchSize}>`
    position: relative;
    display: inline-block;
    width: ${(props) => SIZES[props.$size].width}px;
    height: ${(props) => SIZES[props.$size].height}px;
    cursor: pointer;
    flex-shrink: 0;
`;

const ToggleSwitchInput = styled.input<{$size: ToggleSwitchSize}>`
    opacity: 0;
    width: 0;
    height: 0;

    &:focus-visible + span {
        outline: 2px solid var(--button-bg);
        outline-offset: 2px;
    }

    &:checked + span {
        background-color: var(--button-bg);
    }

    &:checked + span::before {
        transform: translateX(${(props) => SIZES[props.$size].translate}px);
    }
`;

const ToggleSwitchSlider = styled.span<{$size: ToggleSwitchSize}>`
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    background-color: rgba(var(--center-channel-color-rgb), 0.32);
    border-radius: ${(props) => SIZES[props.$size].borderRadius}px;
    transition: background-color 0.2s;

    &::before {
        content: '';
        position: absolute;
        height: ${(props) => SIZES[props.$size].thumb}px;
        width: ${(props) => SIZES[props.$size].thumb}px;
        left: ${(props) => SIZES[props.$size].offset}px;
        bottom: ${(props) => SIZES[props.$size].offset}px;
        background-color: white;
        border-radius: 50%;
        transition: transform 0.2s;
        box-shadow: 0px 2px 4px rgba(0, 0, 0, 0.16);
    }
`;

type ToggleSwitchProps = {
    checked: boolean;
    onChange: (checked: boolean) => void;
    disabled?: boolean;
    size?: ToggleSwitchSize;
    ariaLabel?: string;
};

export const ToggleSwitch = ({checked, onChange, disabled, size = 'medium', ariaLabel}: ToggleSwitchProps) => (
    <ToggleSwitchContainer $size={size}>
        <ToggleSwitchInput
            $size={size}
            type='checkbox'
            checked={checked}
            onChange={(e) => onChange(e.target.checked)}
            disabled={disabled}
            aria-label={ariaLabel}
        />
        <ToggleSwitchSlider $size={size}/>
    </ToggleSwitchContainer>
);
