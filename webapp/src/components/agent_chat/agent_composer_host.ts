// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// The host composer (mattermost/mattermost advanced_text_editor) has no agent-mode
// prop, so agent chat reaches these controls by the ids the host already renders.
// A host markup change has this list and agentComposerHostStyles to update.
export const HOST_EDITOR_BODY = '.AdvancedTextEditor__body';
export const HOST_EDITOR_CELL = '.AdvancedTextEditor__cell';
export const HOST_FORMATTING_TOGGLE = '#toggleFormattingBarButton';
export const HOST_FILE_UPLOAD_BUTTON = '#fileUploadButton';
export const HOST_EMOJI_BUTTON = '#emojiPickerButton';
export const HOST_ATTRIBUTES_BUTTON = '#postAttributesButton';
export const HOST_PREVIEW_BUTTON = '#PreviewInputTextButton';
export const HOST_PRIORITY_BUTTON = '#messagePriority';
export const HOST_BURN_BUTTON = '#burnOnReadButton';
export const HOST_AI_ACTIONS_BUTTON = '#ai-actions-button';
export const HOST_SCHEDULE_BUTTON = '.button_send_post_options';
export const HOST_EDITOR_LABELS = '.AdvancedTextEditor__labels';
export const HOST_FORMATTING_BAR = '[data-testid="formattingBarContainer"]';
export const HOST_ACTION_SEPARATOR = '.AdvancedTextEditor__cell > span [data-testid="formatting-bar-separator"]';
export const HOST_SEND_BUTTON = '[data-testid="SendMessageButton"]';
export const HOST_SEND_SPLIT = '.splitSendButton';

const SEND_WAS_DISABLED = 'data-agent-send-was-disabled';

export const clickHostControl = (root: ParentNode | null, selector: string): boolean => {
    const control = root?.querySelector<HTMLButtonElement>(selector);
    if (!control || control.disabled || control.getAttribute('aria-disabled') === 'true') {
        return false;
    }
    control.click();
    return true;
};

// The host's Add attributes button is a disabled placeholder until messages can
// carry attributes. The menu item follows that: present, and disabled until the
// host button can actually be used.
export const attributesCanBeSet = (root: ParentNode | null): boolean => {
    if (!root) {
        return false;
    }
    const control = root.querySelector<HTMLButtonElement>(HOST_ATTRIBUTES_BUTTON);
    return Boolean(control && !control.disabled && control.getAttribute('aria-disabled') !== 'true');
};

export const formattingBarIsRendered = (root: ParentNode | null): boolean => {
    return Boolean(root?.querySelector(HOST_FORMATTING_BAR));
};

export const clickFormattingToggle = (root: ParentNode | null): boolean => {
    return Boolean(root && clickHostControl(root, HOST_FORMATTING_TOGGLE));
};

// Holds the host send button while a skill or rewrite is writing the draft, and
// puts it back after. A button the host had already disabled stays disabled.
export const setHostSendDisabled = (root: ParentNode | null, disabled: boolean) => {
    const button = root?.querySelector<HTMLButtonElement>(HOST_SEND_BUTTON);
    if (!button) {
        return;
    }
    if (disabled) {
        if (!button.hasAttribute(SEND_WAS_DISABLED)) {
            button.setAttribute(SEND_WAS_DISABLED, button.disabled ? 'true' : 'false');
        }
        button.disabled = true;
        return;
    }
    if (button.getAttribute(SEND_WAS_DISABLED) === 'false') {
        button.disabled = false;
    }
    button.removeAttribute(SEND_WAS_DISABLED);
};
