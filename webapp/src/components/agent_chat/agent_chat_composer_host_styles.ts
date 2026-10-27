// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {css} from 'styled-components';

// Every selector that reaches into the host Textbox's DOM for the pill
// composer lives in this file, so a host markup change has one place to fix.
// (Host ThreadViewer selectors are in agent_chat_host_styles.ts.)
//
// Host dependencies (webapp/channels/src in mattermost/mattermost):
// - components/textbox/textbox.tsx renders
//     `div.textarea-wrapper` (position: relative, min-height 37px in
//       sass/components/_post.scss) holding
//     `div.custom-textarea.textbox-preview-area` (markdown preview, hidden
//       while not previewing) and the SuggestionBox, whose input is
//     components/autosize_textarea.tsx: `textarea.form-control.custom-textarea
//       .textbox-edit-area` plus an invisible sizing copy, a `div` with the same
//       classes inside a zero-height container. Its scrollHeight becomes the
//       textarea's inline height, so both must share the text metrics below.
// - sass/components/_post.scss `.app__body .custom-textarea` (0,2,0; :focus
//   0,3,0) draws a 46px minimum, an inset box-shadow border and 4px radius;
//   bootstrap `.form-control` adds its own border, padding and focus ring.
// - components/suggestion/suggestion_list.tsx renders `div.suggestion-list
//   .suggestion-list--top|--bottom` (sass/components/_suggestion-list.scss:
//   absolute, `bottom: 100%`, z-index 100) inside the SuggestionBox, i.e.
//   against the nearest positioned ancestor, `.textarea-wrapper` by default.

// One line of body text plus the textarea's block padding: 40px, a medium
// compass IconButton, so an empty box keeps its full first line.
export const TEXTAREA_ONE_LINE_HEIGHT = 'calc(var(--line-height-100) + 2 * var(--spacing-s))';

// No compass token caps a text box; eight lines of body text plus the
// textarea's block padding.
export const TEXTAREA_MAX_HEIGHT = 'calc(8 * var(--line-height-100) + 2 * var(--spacing-s))';

// Expanded composer (text on its own full-width row): the text box is inset by
// spacing-xs on every side instead of the inline state's 40px line alignment,
// so one line is line-height-100 + 2 * spacing-xs and the cap is eight such lines.
export const TEXTAREA_EXPANDED_PADDING = 'var(--spacing-xs)';
export const TEXTAREA_EXPANDED_ONE_LINE_HEIGHT = 'calc(var(--line-height-100) + 2 * var(--spacing-xs))';
export const TEXTAREA_EXPANDED_MAX_HEIGHT = 'calc(8 * var(--line-height-100) + 2 * var(--spacing-xs))';

// Shared by the plain textarea, its auto-grow copy, and the host textarea and
// its sizing copy.
export const composerTextStyles = `
    box-sizing: border-box;
    margin: 0;

    /*
     * One line of text plus this padding is exactly a medium compass
     * IconButton (40px: line-height-100 + 2 * spacing-s), the height of the
     * "+" and send buttons. With the pill's items bottom-aligned, the first
     * line (and the placeholder) shares their center while the text is one
     * line, and the last line keeps it as the text grows.
     */
    padding: var(--spacing-s) 0;
    border: none;
    font-family: var(--font-family-body);
    font-size: var(--font-size-100);
    font-weight: var(--font-weight-regular);
    line-height: var(--line-height-100);
    white-space: pre-wrap;
    overflow-wrap: anywhere;
`;

// Interpolate into the pill row (it must be position: relative). &&& outranks
// the host's `.app__body .custom-textarea:focus` (0,3,0).
export const hostTextboxPillStyles = css`
    /*
     * The suggestion list anchors to the pill rather than the text, so it
     * spans the pill and floats just above it.
     */
    &&& .textarea-wrapper {
        position: static;
        min-height: 0;
    }

    &&& .suggestion-list--top {
        bottom: calc(100% + var(--spacing-xs));
        left: 0;
    }

    &&& .suggestion-list--bottom {
        top: calc(100% + var(--spacing-xs));
        left: 0;
    }

    /* The textarea and its sizing copy: the pill's text metrics, no frame. */
    &&& .custom-textarea {
        ${composerTextStyles}
        min-height: 0;
        border-radius: 0;
        background: transparent;
        box-shadow: none;
        color: var(--center-channel-color);
        transition: none;
    }

    /*
     * The autosize copy of an empty box measures padding only (no line), and
     * becomes the textarea's inline height: the floor keeps the placeholder's
     * line whole instead of clipping its top.
     */
    &&& textarea.custom-textarea {
        min-height: ${TEXTAREA_ONE_LINE_HEIGHT};
        max-height: ${TEXTAREA_MAX_HEIGHT};
        resize: none;
        outline: none;
    }

    /*
     * Expanded (the pill's data-expanded): both the textarea and its sizing
     * copy take the expanded inset, so autosize measures what is shown.
     */
    &&&[data-expanded='true'] .custom-textarea {
        padding: ${TEXTAREA_EXPANDED_PADDING};
    }

    &&&[data-expanded='true'] textarea.custom-textarea {
        min-height: ${TEXTAREA_EXPANDED_ONE_LINE_HEIGHT};
        max-height: ${TEXTAREA_EXPANDED_MAX_HEIGHT};
    }

    &&& textarea.custom-textarea:focus {
        box-shadow: none;
    }

    &&& textarea.custom-textarea::placeholder {
        color: rgba(var(--center-channel-color-rgb), 0.64);
        opacity: 1;
    }
`;
