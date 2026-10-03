// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {AskAnotherUserToolName, ToolCall} from './tool_types';

export type AskCancelState = 'idle' | 'submitting' | 'error';

// Extracts the target username from AskAnotherUser tool arguments. Returns ''
// when the arguments are missing or redacted (observers see null arguments).
export function parseAskAnotherUserTarget(args: ToolCall['arguments']): string {
    if (args == null || typeof args !== 'object' || Array.isArray(args)) {
        return '';
    }
    const username = (args as {[key: string]: unknown}).username;
    if (typeof username !== 'string') {
        return '';
    }

    // The model may pass "@bob"; the backend tolerates it via TrimPrefix.
    // Strip it here too so the card doesn't render "@@bob".
    return username.startsWith('@') ? username.slice(1) : username;
}

// Returns the declining user's username (or '' when unknown) if this tool call
// is an AskAnotherUser call whose result records a decline; null otherwise.
// Result shape: {"status":"declined","target_username":"bob"} (contract C7).
export function parseAskAnotherUserDecline(tool: ToolCall): string | null {
    if (tool.name !== AskAnotherUserToolName || !tool.result) {
        return null;
    }
    try {
        const parsed = JSON.parse(tool.result);
        if (parsed?.status !== 'declined') {
            return null;
        }
        const username = typeof parsed?.target_username === 'string' ? parsed.target_username : '';
        return username || parseAskAnotherUserTarget(tool.arguments);
    } catch {
        return null;
    }
}

// True when this tool call is an AskAnotherUser call whose result records an
// initiator cancel ({"status":"canceled",…}, contract V2-C4/C5). The block
// itself completes with success status; "canceled" lives in the result JSON.
export function parseAskAnotherUserCanceled(tool: ToolCall): boolean {
    if (tool.name !== AskAnotherUserToolName || !tool.result) {
        return false;
    }
    try {
        return JSON.parse(tool.result)?.status === 'canceled';
    } catch {
        return false;
    }
}
