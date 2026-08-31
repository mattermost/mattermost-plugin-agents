// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import styled from 'styled-components';

import {ToolCall, ToolCallStatus} from '../../tool_types';

export const PreviewWrap = styled.div`
    margin-top: 12px;
`;

// The preview cards only render while the call still needs a decision: that is
// when seeing the post has approval value. Executed calls render the generic
// card (the response already carries the outcome).
export function isAwaitingDecision(tool: ToolCall): boolean {
    return tool.status === ToolCallStatus.Pending || tool.status === ToolCallStatus.Accepted;
}
