// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';

import {ToolApprovalStage, ToolCall} from './tool_types';
import ToolArguments from './tool_arguments';
import ToolCardShell from './tool_renderers/tool_card_shell';

interface ToolCardProps {
    postID: string;
    tool: ToolCall;
    isCollapsed: boolean;
    isProcessing: boolean;
    localDecision?: boolean;
    onToggleCollapse: () => void;
    onApprove?: () => void;
    onReject?: () => void;
    canExpand: boolean;
    showArguments: boolean;
    showResults: boolean;
    approvalStage?: ToolApprovalStage;
    isAutoApproved?: boolean;
}

/**
 * ToolCard is the generic tool renderer: the shared approval shell with the
 * arguments rendered as the readable field list. Rich per-tool cards reuse the
 * same shell (ToolCardShell) but supply their own arguments body.
 */
const ToolCard: React.FC<ToolCardProps> = (props) => (
    <ToolCardShell {...props}>
        <ToolArguments arguments={props.tool.arguments}/>
    </ToolCardShell>
);

export default ToolCard;
