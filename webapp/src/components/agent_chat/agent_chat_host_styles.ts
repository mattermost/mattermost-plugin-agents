// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {css} from 'styled-components';

// Every selector that reaches into host (Mattermost webapp) DOM for the agent
// chat (Agents product view and Agents RHS) lives in this file, so a host
// markup change has one place to fix.
//
// Host dependencies (webapp/channels/src in mattermost/mattermost):
// - components/post/post_component.tsx
//     `.post`             every post row (PostAriaLabelDiv className)
//     `.current--user`    on `.post` when post.user_id is the current user (not system messages)
//     `.post__content`    [data-testid="postContent"], row holding the avatar and the body column
//     `.post__img`        first child of `.post__content`: avatar slot, hidden here
//                         (the body column is its unnamed sibling `div`)
//     `.post__header`, `.badges-wrapper` (holds `.post__time`), `.post-message__text`
//                         header, time and text inside the body column
//     components/post/user_profile.tsx: `.col__name`, the author name and bot tag in the header
// - components/dynamic_virtualized_list/index.tsx: `.innerList`, the div inside the scroller that
//     holds the rows (each wrapped in `.item_measurer`, list_item.tsx); capped and centered here
//     for the product view's column. Its inline style only sets a top padding.
// - components/root_post_divider/root_post_divider.tsx: `.root-post__divider`,
//     the "N replies" line rendered in the root post's row (thread_viewer_row.tsx)
// - components/post_view/date_separator/date_separator.tsx renders
//     widgets/separator/basic-separator.tsx `.Separator.BasicSeparator` (the
//     new-messages line is `.Separator.NotificationSeparator`); separator.scss
//     draws the line (`.separator__hr`) and label (`.separator__text`)
// - components/threading/virtualized_thread_viewer/create_comment.tsx:
//     [data-testid="comment-create"] (`.post-create__container`), the host reply
//     composer, hidden here; virtualized_thread_viewer.tsx renders it after the
//     list inside `.virtual-list__ctr` (flex column, virtualized_thread_viewer.scss)
// - sass/components/_post.scss lays `.post__content` out as a table; we switch it
//   to flex inside the chat only.
// - Row backgrounds the host paints on `.post` (all neutralized here, so only the
//   bubble reacts):
//     sass/components/_post.scss  `.post:not(.post--editing):hover`, `.post.post--editing`,
//                                 `.post--pinned-or-flagged`,
//                                 `.app__body .app__content .post.post--highlight` (0,4,0)
//     utils/utils.tsx (applyTheme) `.app__body .post.post--hovered`, `.app__body .post.a11y--active`
//   `post--hovered` is set while the row is hovered or its menus are open;
//   `a11y--active` while the row has keyboard (a11y) focus. The host's focus ring
//   is not a background, so it stays.
// - Hover toolbar: components/post/post_options.tsx renders `ul.post-menu`
//   ([data-testid="post-menu-<id>"]) inside `.post__header`, i.e. inside the
//   bubble. sass/components/_post-menu.scss positions it absolute (top -12px,
//   right 0) against the nearest positioned ancestor (host: `.post__content`) and
//   shows its `.post-menu__item`s on `.post:hover/.post--hovered/.a11y--active`.
//   The plugin hides it entirely in the chat.
// - Bubble fit (CSS only, `:has()` on the bubble): fenced code as formatted by the host
//     (utils/markdown renderer: `.post-code`, not a `<pre>`), and host attachments the bubble may
//     hold below the message: components/file_attachment_list
//     (`.post-image__columns`, single image `.file-view--single`),
//     post_view/message_attachments (`.attachment`), and post_view/post_body_additional_content
//     embeds (`.post__embed-container`, `.PostAttachmentOpenGraph`).

// The host thread viewer's scroll container (components/dynamic_virtualized_list renders its
// `id` on the outer overflow-y: auto element; virtualized_thread_viewer.tsx passes this one).
// Read-only: the plugin only measures it and scrolls it to the bottom.
export const THREAD_SCROLLER_SELECTOR = '#threadViewerScrollContainer';

// Width of the chat column (messages and composers), from the prototype's
// AgentChat.module.scss (`agent-chat__messages-list` / `__composer`: 800px).
// No compass token covers content widths.
export const CHAT_CONTENT_MAX_WIDTH = '800px';

// Compact only: its text sits optically high in the shorter bubble, so it gets 2px more under it than over it.
const BUBBLE_PADDING_BOTTOM_COMPACT = 'calc(var(--spacing-xxxs) + var(--spacing-xxxxs))';

// The body column of a post, which becomes the bubble.
const BUBBLE = '.post__content > .post__img + div';

// Plugin and host content that always needs the full row: code, tables, media,
// tool and reasoning UI, artifacts, permalink previews, notices, and host
// attachments. A collapsed activity line ("Thinking", "Used 2 tools") is short
// text and hugs like text; its expanded panel marks itself `data-bubble-wide`.
const BUBBLE_WIDE_CONTENT = [
    'pre',
    'table',
    'img',
    'video',
    'iframe',
    "[data-testid='llm-bot-tool-activity-rounds']",
    "[data-testid='html-artifact-card']",
    "[data-testid='html-artifact-wrapper']",
    "[data-testid='llm-bot-permalink']",
    "[data-testid='llm-bot-post-summary-help']",
    '[data-bubble-wide]',
    '.post-code',
    '.post-image__columns',
    '.file-view--single',
    '.attachment',
    '.post__embed-container',
    '.PostAttachmentOpenGraph',
].join(', ');

// Set by LLMBotPost on its own wrapper inside the bubble where short replies hug.
export const BUBBLE_HUG_ATTRIBUTE = 'data-bubble-hug';

// Set by the plugin's controls bar when it should sit under the bubble.
export const BUBBLE_CONTROLS_ATTRIBUTE = 'data-bubble-controls';
export const BUBBLE_CONTROLS_BELOW = 'below';

// Height of the controls bar (an x-small IconButton / Button row). No compass
// token covers a control row height; controls_bar.tsx uses the same value.
export const CONTROLS_BAR_HEIGHT = '28px';

// Gap between the bubble's bottom edge and the controls bar. The x-small
// IconButton (24px) is centered in the 28px bar, so it already sits 2px below
// the bubble with no gap. One-line tweak; the reserved margin follows it.
const CONTROLS_BAR_GAP = '0px';

// Compact mode pulls the controls bar this much closer to the bubble (and shortens the reserved space).
const COMPACT_CONTROLS_PULL = 'var(--spacing-xxxxs)';

// The bubble the user is on: pointer over it, keyboard focus inside it, or the
// row's a11y focus. Keyboard focus is :has(:focus-visible), not :focus-within: clicking a button or
// link in a reply leaves it focused, and :focus-within would keep the shade on after the pointer left.
// The :has() is wrapped in :is(), whose selector list is forgiving: in a browser without :has() an
// unwrapped entry would invalidate this whole comma-joined rule, dropping the :hover shade with it.
const activeBubble = (post: string) => [
    `${post} ${BUBBLE}:hover`,
    `${post} ${BUBBLE}:is(:has(:focus-visible))`,
    `${post}.a11y--active ${BUBBLE}`,
].join(',\n    ');

interface HostThreadChatStylesOptions {

    // Caps and centers the message column (the product view's 800px column).
    // Omit it where the container is already narrow (the RHS), so rows fill it.
    columnMaxWidth?: string;
}

// The host's own `.post` side padding (sass/components/_post.scss
// `padding: 8px 0.5em 0 1.5em`), mirrored so separators line up with the posts.
// Not a compass value: it matches host markup the plugin does not own.
const HOST_POST_INLINE_PADDING = '1.5em 0.5em';

// The chat column's side padding, shared with the composer under the messages (the thread view
// pads its composer area the same), so the message content and the composer line up.
export const CHAT_COLUMN_PADDING = 'var(--spacing-xxl)';

// The same for the RHS, where the panel's content and the composer under it are padded by 20px.
export const CHAT_PANEL_PADDING = 'var(--spacing-xl)';

// Center the column the prototype uses, inset by --spacing-m (12px) on each side and padded like the
// composer under it. The cap and padding are on the host list's inner wrapper (`.innerList`, which
// holds the rows), so the scroller, and with it the scrollbar, stays full width. The rows
// (`.item_measurer`) fill its content box, so the posts' and separators' own side padding (1.5em /
// 0.5em) is removed. The scroller reserves its scrollbar gutter on both sides (see below). &&& outranks the host's `.post` padding and the separator rule below.
const centeredColumn = (maxWidth: string) => css`
    /*
     * A classic scrollbar takes width from the scroller's content area, which would pull the column
     * off the composer's center (the composer is centered in the whole pane). Reserving the gutter
     * on both sides keeps the column centered; overlay scrollbars take no space either way.
     */
    ${THREAD_SCROLLER_SELECTOR} {
        scrollbar-gutter: stable both-edges;
    }

    .innerList {
        box-sizing: border-box;
        width: calc(100% - 2 * var(--spacing-m));
        max-width: ${maxWidth};
        margin-inline: auto;
        padding-inline: ${CHAT_COLUMN_PADDING};
    }

    &&& .post,
    &&& .Separator {
        padding-inline: 0;
    }
`;

// Without a column (the RHS fills its panel) the rows still get the composer's side padding, from the
// same wrapper, so the posts have the same space on both sides (the host pads a post 1.5em on the left
// and 0.5em on the right) and line up with the composer below.
const paddedRows = css`
    .innerList {
        box-sizing: border-box;
        padding-inline: ${CHAT_PANEL_PADDING};
    }

    &&& .post,
    &&& .Separator {
        padding-inline: 0;
    }
`;

// Chat-bubble restyle of the host ThreadViewer. Interpolate it into the styled
// wrapper that contains the ThreadViewer (the product view's thread pane, the
// RHS thread pane); it is scoped under that wrapper and never applied globally.
export const hostThreadChatStyles = ({columnMaxWidth}: HostThreadChatStylesOptions = {}) => css`
    ${columnMaxWidth ? centeredColumn(columnMaxWidth) : paddedRows}

    /* A little room under the last post, so the end of the list reads as the end. */
    .innerList {
        padding-bottom: var(--spacing-m);
    }

    /*
     * The plugin's pill composer replaces the host reply box. The host renders it
     * as the last child of the flex column \`.virtual-list__ctr\`, after the
     * list (\`.post-right__content\`, height 100% around an AutoSizer), so with it
     * removed the list's AutoSizer simply takes the freed height. The archived /
     * deactivated-user notices the host renders instead are not hidden.
     */
    [data-testid='comment-create'] {
        display: none;
    }

    /*
     * No "N replies" divider under the root post: a chat has no root/replies
     * split. It renders inside the root post's list row, which the virtualized
     * list re-measures, so removing it only shortens that row.
     */
    .root-post__divider {
        display: none;
    }

    /*
     * Date (and new-messages) separators span the post column, not the whole
     * scroller: capped with the posts where there is a column (above), and
     * inset like the posts' content everywhere. The line and label keep the
     * host styling.
     */
    .Separator {
        padding-inline: ${HOST_POST_INLINE_PADDING};

        /* More room under the date line than the host's 1em, so the first post breathes. */
        margin-bottom: var(--spacing-xxl);
    }

    /*
     * The host pads the row (0 8px 0 5px, a table layout's gutters), which would shift the posts
     * off the column's center: the content fills it exactly. &&& outranks the host's rule.
     */
    &&& .post .post__content {
        display: flex;
        align-items: flex-start;
        gap: var(--spacing-xs);
        padding: 0;
    }

    /* No avatars: neither the agent's nor the user's. */
    .post .post__content > .post__img {
        display: none;
    }

    /*
     * Rows never change background (hover, menus, a11y focus, edit, highlight,
     * pinned/saved). &&& outranks the host's theme rules (up to 0,4,0).
     */
    &&& .post,
    &&& .post:hover,
    &&& .post.post--hovered,
    &&& .post.a11y--active,
    &&& .post.post--editing,
    &&& .post.post--highlight,
    &&& .post.post--pinned-or-flagged {
        background: transparent;
    }

    /*
     * The user's compact display setting (the host adds \`post--compact\` to every row): rows sit
     * closer together. The host's own compact padding is kept for the row; the bubble gives back
     * some of its vertical padding.
     */
    &&& .post.post--compact ${BUBBLE} {
        /* All four sides, so no host compact rule leaves one of them behind. */
        padding: var(--spacing-xxxs) var(--spacing-s) ${BUBBLE_PADDING_BOTTOM_COMPACT};
    }

    /* The host pads a row 8px at the top; compact halves it, which is the space between posts. */
    &&& .post.post--compact {
        padding-top: var(--spacing-xxxs);
    }

    /*
     * The host's compact header adds its own top padding (3px on thread posts) and floats left, so
     * the text sits lower than the bubble's bottom padding. The header holds nothing in flow here
     * (the time is absolute), so it takes no space of its own.
     */
    &&& .post.post--compact .post__header {
        float: none;
        padding: 0;
    }

    /* The body column (header + message). */
    .post ${BUBBLE} {
        /* Anchors the host toolbar (absolute, top right) to the body, not the row. */
        position: relative;
        flex: 1 1 auto;
        min-width: 0;
        padding: var(--spacing-xs) var(--spacing-l);
        border-radius: var(--radius-l);
        background: transparent;
        transition: background var(--duration-quick) var(--ease-transition);

        @media (prefers-reduced-motion: reduce) {
            transition: none;
        }
    }

    /* The agent's reply shows its bubble shape only while the user is on it. */
    ${activeBubble('.post')} {
        background: rgba(var(--center-channel-color-rgb), 0.06);
    }

    /*
     * The agent's host header (author name, bot tag, time) is hidden except for the hover toolbar;
     * the plugin's controls bar shows the time. !important because the host's \`.d-flex\` utility
     * on the badges wrapper is !important too.
     */
    .post:not(.current--user) .post__header > :not(.post-menu) {
        display: none !important;
    }

    /*
     * The host's hover toolbar (reply, follow, pin, forward, ...) is built for channels and mostly
     * means nothing in a one-to-one agent chat, so it is hidden. The plugin's controls bar under
     * each post carries the actions that matter here.
     */
    .post .post-menu {
        display: none !important;
    }

    /* The time is not a link here (clicks are blocked by the thread view): it keeps the host tooltip. */
    .post .post__permalink {
        cursor: default;
    }

    /* Keyboard focus inside a row must not clip the toolbar (the host only unclips on hover). */
    .post:focus-within {
        overflow: visible;
    }

    /*
     * Short agent replies hug their content: LLMBotPost marks its wrapper and
     * the bubble shrinks to its text, up to the row (longer text wraps and
     * fills it). Content that needs the row (code, tables, media, tool UI)
     * keeps the full-width bubble. Without :has() support the rule never
     * matches and every bubble stays full width.
     */
    .post ${BUBBLE}:has([${BUBBLE_HUG_ATTRIBUTE}]):not(:has(${BUBBLE_WIDE_CONTENT})) {
        flex: 0 1 auto;
        width: fit-content;
        max-width: 100%;
    }

    /*
     * Post controls under the bubble: the plugin marks its controls bar (still
     * inside the bubble in the DOM, so tab order and hover/focus-within are
     * unchanged) and it is placed just below the bubble's bottom edge, aligned
     * with the text. The bubble reserves that space with a bottom margin only
     * when the bar is rendered. A flex item's margin counts in the height of
     * \`.post__content\` (flex margins never collapse), so the virtualized
     * list, which measures each row from the DOM, includes it.
     */
    .post ${BUBBLE}:has([${BUBBLE_CONTROLS_ATTRIBUTE}='${BUBBLE_CONTROLS_BELOW}']) {
        margin-bottom: calc(${CONTROLS_BAR_HEIGHT} + ${CONTROLS_BAR_GAP});
    }

    .post ${BUBBLE} [${BUBBLE_CONTROLS_ATTRIBUTE}='${BUBBLE_CONTROLS_BELOW}'] {
        position: absolute;
        top: calc(100% + ${CONTROLS_BAR_GAP});
        /* The bubble's text inset, so the time (first in the bar) lines up with the text above. */
        left: var(--spacing-l);
        margin-top: 0;
    }

    /*
     * Compact bubbles have a smaller side inset: the controls bar and your time follow it. They also
     * sit 2px closer to the bubble (the bar's buttons are centered in it, so they were 2px lower
     * than the bubble's edge), and the reserved space shrinks with them.
     */
    .post.post--compact ${BUBBLE} [${BUBBLE_CONTROLS_ATTRIBUTE}='${BUBBLE_CONTROLS_BELOW}'] {
        top: calc(100% + ${CONTROLS_BAR_GAP} - ${COMPACT_CONTROLS_PULL});
        left: var(--spacing-s);
    }

    .post.post--compact ${BUBBLE}:has([${BUBBLE_CONTROLS_ATTRIBUTE}='${BUBBLE_CONTROLS_BELOW}']),
    .post.post--compact.current--user ${BUBBLE} {
        margin-bottom: calc(${CONTROLS_BAR_HEIGHT} + ${CONTROLS_BAR_GAP} - ${COMPACT_CONTROLS_PULL});
    }

    .post.post--compact.current--user .post__header .badges-wrapper {
        top: calc(100% + ${CONTROLS_BAR_GAP} - ${COMPACT_CONTROLS_PULL});
        right: var(--spacing-s);
    }

    /*
     * Own messages: a right-aligned bubble, filled with 8% of the text color. It has no hover shade:
     * nothing on it is actionable. No avatar or name; the time sits under the bubble,
     * in the space the bubble reserves.
     */
    .post.current--user .post__content {
        flex-direction: row-reverse;
    }

    .post.current--user ${BUBBLE} {
        flex: 0 1 auto;
        max-width: 100%;
        padding: var(--spacing-xs) var(--spacing-l);

        /* Room for the time under the bubble, and before the agent's reply. */
        margin-bottom: calc(${CONTROLS_BAR_HEIGHT} + ${CONTROLS_BAR_GAP});
        border-radius: var(--radius-l);
        background: rgba(var(--center-channel-color-rgb), 0.08);
    }

    /* Keyboard focus inside your own bubble, or the row's a11y focus, also gets a ring. */
    .post.current--user.a11y--active ${BUBBLE},
    .post.current--user ${BUBBLE}:is(:has(:focus-visible)) {
        outline: 2px solid var(--button-bg);
        outline-offset: 2px;
    }

    /* While editing, the host editor sits in the bubble: a quieter fill than the message has. */
    .post.current--user.post--editing ${BUBBLE} {
        background: rgba(var(--center-channel-color-rgb), 0.04);
    }

    .post.current--user .post__header .col__name {
        display: none;
    }

    /* The time: in a row below the bubble, at its right edge (the agent's is in its controls bar). */
    .post.current--user .post__header .badges-wrapper {
        position: absolute;
        top: calc(100% + ${CONTROLS_BAR_GAP});
        right: var(--spacing-l);
        height: ${CONTROLS_BAR_HEIGHT};
        align-items: center;
    }

    .post.current--user .post__time {
        color: rgba(var(--center-channel-color-rgb), 0.64);
        font-size: var(--font-size-75);
        line-height: var(--line-height-75);
    }
`;
