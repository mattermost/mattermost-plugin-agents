// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Shared chrome for user-interaction tool cards (QuestionCard, ConnectCard):
// an accent-bordered card, a title, a footer for the action buttons, and a
// status line for resolved/in-progress states.

import styled from 'styled-components';
import {CheckIcon, CloseCircleOutlineIcon} from '@mattermost/compass-icons/components';

export const InteractionCard = styled.div`
    position: relative;
    display: flex;
    flex-direction: column;
    gap: 12px;
    padding: 16px 16px 16px 12px;
    margin: 8px 0 12px;
    overflow: hidden;
    background: var(--center-channel-bg);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    border-radius: 4px;
    box-shadow: 0 2px 3px rgba(0, 0, 0, 0.08);

    &::before {
        content: '';
        position: absolute;
        top: 0;
        left: 0;
        bottom: 0;
        width: 3px;
        background: var(--button-bg);
    }
`;

export const InteractionTitle = styled.div`
    padding-left: 12px;
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    color: var(--center-channel-color);
    word-break: break-word;
`;

export const InteractionFooter = styled.div`
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding-left: 12px;
    padding-top: 4px;
`;

export const InteractionFooterButtons = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
    margin-left: auto;
`;

export const InteractionStatusLine = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
    padding-left: 12px;
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.75);
`;

export const ResolvedIcon = styled(CheckIcon)`
    color: var(--online-indicator);
`;

export const SkippedIcon = styled(CloseCircleOutlineIcon)`
    color: var(--dnd-indicator);
`;
