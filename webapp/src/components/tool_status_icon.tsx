// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {CheckIcon, AlertCircleOutlineIcon, CloseCircleOutlineIcon} from '@mattermost/compass-icons/components';

import {ToolCallStatus} from './tool_types';

import LoadingSpinner from './assets/loading_spinner';

const StatusIcon = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.64);
    width: 12px;
    display: flex;
    align-items: center;
    justify-content: center;
`;

const SmallSpinner = styled(LoadingSpinner)`
    width: 12px;
    height: 12px;
`;

const SmallSuccessIcon = styled(CheckIcon)`
    color: var(--online-indicator);
    width: 12px;
    height: 12px;
`;

const SmallErrorIcon = styled(AlertCircleOutlineIcon)`
    color: var(--error-text);
    width: 12px;
    height: 12px;
`;

const SmallRejectedIcon = styled(CloseCircleOutlineIcon)`
    color: var(--dnd-indicator);
    width: 12px;
    height: 12px;
`;

interface ToolStatusIconProps {
    status: ToolCallStatus;

    // Forces the spinner regardless of status, for a decision that has been
    // submitted but whose new status has not landed yet.
    isProcessing?: boolean;
}

/** The 12px status glyph shown next to a tool name. */
const ToolStatusIcon: React.FC<ToolStatusIconProps> = ({status, isProcessing = false}) => {
    const showSpinner = isProcessing ||
        status === ToolCallStatus.Pending ||
        status === ToolCallStatus.Accepted;

    return (
        <StatusIcon>
            {showSpinner && <SmallSpinner/>}
            {!showSpinner && (status === ToolCallStatus.Success || status === ToolCallStatus.AutoApproved) && <SmallSuccessIcon/>}
            {!showSpinner && status === ToolCallStatus.Error && <SmallErrorIcon/>}
            {!showSpinner && status === ToolCallStatus.Rejected && <SmallRejectedIcon/>}
        </StatusIcon>
    );
};

export default ToolStatusIcon;
