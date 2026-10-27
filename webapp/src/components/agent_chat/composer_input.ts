// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Pure rules of the agent chat pill composer, mirrored from the host message
// composer (webapp/channels/src in mattermost/mattermost) so the pill behaves
// like the regular one: utils/post_utils.ts (postMessageOnKeyPress,
// sendOnCtrlEnter, isWithinCodeBlock, canAutomaticallyCloseBackticks,
// isErrorInvalidSlashCommand) and actions/command.ts (executeCommand).

// Host preferences (Settings > Advanced), category advanced_settings.
export const PREF_SEND_ON_CTRL_ENTER = 'advanced_settings--send_on_ctrl_enter';
export const PREF_CODE_BLOCK_CTRL_ENTER = 'advanced_settings--code_block_ctrl_enter';

// Host Constants.DEFAULT_CHARACTER_LIMIT, used when the server sends no MaxPostSize.
export const DEFAULT_MAX_POST_SIZE = 4000;

export const INVALID_SLASH_COMMAND_ERROR_ID = 'api.command.execute_command.not_found.app_error';

const CODE_BLOCK_FENCE = /^```.*$/gm;

export const isWithinCodeBlock = (message: string, caret: number) => {
    const fences = message.substring(0, caret).match(CODE_BLOCK_FENCE);
    return Boolean(fences && fences.length % 2 !== 0);
};

// Ctrl/Cmd+Enter inside an open code block closes it before sending.
const closeCodeBlock = (message: string) => {
    const lines = message.split('\n').filter((line) => line.trim() !== '');
    if (lines.length > 1 && !lines[lines.length - 1].includes('```')) {
        return message.endsWith('\n') ? `${message}\`\`\`` : `${message}\n\`\`\``;
    }
    return message;
};

export interface EnterKey {
    key: string;
    shiftKey: boolean;
    altKey: boolean;
    ctrlKey: boolean;
    metaKey: boolean;
    isComposing: boolean;
    keyCode: number;
}

export interface EnterContext {
    message: string;
    caret: number;
    sendOnCtrlEnter: boolean;
    codeBlockOnCtrlEnter: boolean;
}

// Whether a key press sends, and the text to send (a closed code block).
// `send: false` leaves the key to the textarea (a newline).
export const decideEnter = (e: EnterKey, {message, caret, sendOnCtrlEnter, codeBlockOnCtrlEnter}: EnterContext): {send: boolean; message?: string} => {
    // keyCode 229 covers browsers that end IME composition before isComposing flips.
    if (e.key !== 'Enter' || e.shiftKey || e.altKey || e.isComposing || e.keyCode === 229) {
        return {send: false};
    }
    if (!sendOnCtrlEnter && !codeBlockOnCtrlEnter) {
        return {send: true};
    }
    if (!sendOnCtrlEnter && message.trim() === '') {
        return {send: true};
    }
    const ctrlOrMeta = e.ctrlKey || e.metaKey;
    const inCodeBlock = isWithinCodeBlock(message, caret);
    if (inCodeBlock) {
        return ctrlOrMeta ? {send: true, message: closeCodeBlock(message)} : {send: false};
    }
    return {send: !sendOnCtrlEnter || ctrlOrMeta};
};

// The host runs a message starting with "/" as a slash command; "//" is the
// plugin's literal escape (posts the text as typed).
export const isSlashCommand = (message: string) => message.startsWith('/') && !message.startsWith('//');

// As actions/command.ts: lower-cased trigger, then the trimmed arguments.
export const normalizeCommand = (message: string) => {
    let end = message.indexOf(' ');
    if (end < 0) {
        end = message.length;
    }
    return `${message.substring(0, end).toLowerCase()} ${message.substring(end).trim()}`;
};

// Where a command response's goto_location leads: an in-app path (pushed to
// the host history), an external http(s) URL (a new tab), or nowhere.
export const gotoTarget = (location: string | undefined, siteURL: string): {path: string} | {url: string} | null => {
    if (!location) {
        return null;
    }
    if (location.startsWith('/') && !location.startsWith('//')) {
        return {path: location};
    }
    if (siteURL && location.startsWith(`${siteURL}/`)) {
        return {path: location.substring(siteURL.length)};
    }
    return (/^https?:\/\//i).test(location) ? {url: location} : null;
};

// Drafts live in sessionStorage under the plugin's own prefix, never the
// host's `draft_<channel>` keys (the host's Channels composer must not pick
// up text typed in the agent chat).
const DRAFT_PREFIX = 'mattermost-ai.agent-chat-draft:';

export const draftKey = (channelId: string, rootId: string) => (channelId ? `${DRAFT_PREFIX}${channelId}:${rootId}` : '');

export const readDraft = (key: string): string => {
    if (!key) {
        return '';
    }
    try {
        return window.sessionStorage.getItem(key) ?? '';
    } catch {
        return '';
    }
};

export const writeDraft = (key: string, message: string) => {
    if (!key) {
        return;
    }
    try {
        if (message) {
            window.sessionStorage.setItem(key, message);
        } else {
            window.sessionStorage.removeItem(key);
        }
    } catch {
        // Storage unavailable (private mode, quota): the draft just isn't kept.
    }
};
