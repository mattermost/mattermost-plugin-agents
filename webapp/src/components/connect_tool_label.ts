// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {IntlShape} from 'react-intl';

import {ToolCall, UserInteractionConnect} from './tool_types';

/**
 * A user-facing label for a ConnectMCPServer call ("Connect Jira"), or null
 * for any other tool. Non-requesters get redacted arguments and see the
 * generic "Connect integration".
 */
export function connectToolLabel(tool: ToolCall, intl: IntlShape): string | null {
    if (tool.user_interaction !== UserInteractionConnect) {
        return null;
    }
    const args = tool.arguments;
    const server = args != null && typeof args === 'object' && !Array.isArray(args) ? args.server : null;
    if (typeof server === 'string' && server.trim() !== '') {
        return intl.formatMessage({id: 'ai.connect_mcp.activity_label', defaultMessage: 'Connect {server}'}, {server});
    }
    return intl.formatMessage({id: 'ai.connect_mcp.activity_label_unknown', defaultMessage: 'Connect integration'});
}
