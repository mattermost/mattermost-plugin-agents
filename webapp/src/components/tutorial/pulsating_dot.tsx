// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled, {keyframes} from 'styled-components';

const pulse1 = keyframes`
    0% { transform: scale(1); opacity: 1; }
    50% { transform: scale(1.8); opacity: 0; }
    100% { transform: scale(1); opacity: 0; }
`;

const pulse2 = keyframes`
    0% { transform: scale(1); opacity: 1; }
    30% { transform: scale(1); opacity: 1; }
    100% { transform: scale(2.2); opacity: 0; }
`;

const DotContainer = styled.span<{$clickable?: boolean}>`
    position: relative;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 12px;
    height: 12px;
    cursor: ${(props) => (props.$clickable ? 'pointer' : 'default')};

    &,
    &::before,
    &::after {
        width: 12px;
        height: 12px;
        background-color: var(--online-indicator);
        border-radius: 50%;
    }

    &::before,
    &::after {
        position: absolute;
        top: 0;
        left: 0;
        display: block;
        content: "";
    }

    &::after {
        animation: ${pulse1} 2s ease 0s infinite;
    }

    &::before {
        animation: ${pulse2} 2s ease 0s infinite;
    }
`;

type Props = {
    className?: string;
    onClick?: (e: React.MouseEvent) => void;
}

export const PulsatingDot: React.FC<Props> = ({className, onClick}) => {
    return (
        <DotContainer
            className={className}
            onClick={onClick}
            $clickable={Boolean(onClick)}
        />
    );
};

export default PulsatingDot;
