// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import styled from 'styled-components';

export const Button = styled.button`
    border-radius: 4px;
    padding: 8px 16px;
    display: flex;
    align-items: center;
    font-weight: 600;
    font-size: 12px;
    background-color: rgb(var(--center-channel-bg-rgb));
    border: 0;

    &:hover {
        background-color: rgba(var(--button-bg-rgb), 0.08);
        color: rgb(var(--link-color-rgb));
        svg {
            fill: rgb(var(--link-color-rgb))
        }
    }

    svg {
        fill: rgb(var(--center-channel-color));
        margin-right: 6px;
    }

	i {
		display: flex;
		font-size: 14px;
		margin-right: 2px;
	}
`;

export const RHSTitle = styled.div`
    margin-top: 12px;
    margin-bottom: 8px;
    font-family: Metropolis;
    font-weight: 600;
    font-size: 22px;
	line-height: 28px;
`;

export const RHSText = styled.div`
    font-weight: 500;
    font-size: 14px;
	line-height: 20px;
	margin-bottom: 24px;
`;

export const RHSPaddingContainer = styled.div`
	margin: 0 24px;
	margin-top: 16px;
    display: flex;
    flex-direction: column;
	flex-grow: 1;
	gap: 8px;
`;

