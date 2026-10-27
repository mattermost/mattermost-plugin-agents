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
//     `.post__img`        first child of `.post__content`: avatar slot
//                         (the body column is its unnamed sibling `div`)
//     `.post__header`, `.post__time`, `.user-popover`, `.post-message__text`
//                         header and text inside the body column
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
//   An open toolbar menu marks its trigger `.post-menu__item--active` and/or
//   `aria-expanded="true"`. The host only renders the items while the row is
//   hovered (React state), which bubble hover implies.
// - Bubble fit (read-only measurement by llmbot_post/use_bubble_width.ts):
//     `.post__header` inside the bubble (its children's natural widths; the
//     absolute `.post-menu` is skipped), and host attachments the bubble may
//     hold below the message: components/file_attachment_list
//     (`.post-image__columns`, single image `.file-view--single`),
//     post_view/message_attachments (`.attachment`), and post_view/post_body_additional_content
//     embeds (`.post__embed-container`, `.PostAttachmentOpenGraph`).

// Width of the chat column (messages and composers), from the prototype's
// AgentChat.module.scss (`agent-chat__messages-list` / `__composer`: 800px).
// No compass token covers content widths.
export const CHAT_CONTENT_MAX_WIDTH = '800px';

const AVATAR_SLOT = 'var(--icon-size-32)';

// The body column of a post, which becomes the bubble.
const BUBBLE = '.post__content > .post__img + div';

// Exported for the plugin's read-only bubble measurement (use_bubble_width.ts).
export const BUBBLE_SELECTOR = BUBBLE;
export const BUBBLE_HEADER_SELECTOR = '.post__header';
export const BUBBLE_TOOLBAR_SELECTOR = '.post-menu';
export const BUBBLE_WIDE_HOST_CONTENT_SELECTOR = '.post-image__columns, .file-view--single, .attachment, .post__embed-container, .PostAttachmentOpenGraph';

// An agent bubble hugs its content when its natural width is under this share
// of the row (the row minus the avatar slot), and spans the row otherwise.
export const HUG_MAX_FRACTION = 0.75;

// Set by LLMBotPost on its own wrapper inside the bubble.
export const BUBBLE_FIT_ATTRIBUTE = 'data-bubble-fit';
export const BUBBLE_MOTION_ATTRIBUTE = 'data-bubble-motion';

// The bubble the user is on: pointer over it (the toolbar is its descendant, so
// moving onto the toolbar keeps it), keyboard focus inside it, the row's a11y
// focus, or one of its toolbar menus open.
const activeBubble = (post: string) => [
    `${post} ${BUBBLE}:hover`,
    `${post} ${BUBBLE}:focus-within`,
    `${post}.a11y--active ${BUBBLE}`,
    `${post} ${BUBBLE}:has(.post-menu__item--active, .post-menu [aria-expanded='true'])`,
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

// Center the column the prototype uses; the scroller stays full width.
const centeredColumn = (maxWidth: string) => css`
    .post,
    .Separator {
        max-width: ${maxWidth};
        margin-inline: auto;
    }
`;

// Chat-bubble restyle of the host ThreadViewer. Interpolate it into the styled
// wrapper that contains the ThreadViewer (the product view's thread pane, the
// RHS thread pane); it is scoped under that wrapper and never applied globally.
export const hostThreadChatStyles = ({columnMaxWidth}: HostThreadChatStylesOptions = {}) => css`
    ${columnMaxWidth ? centeredColumn(columnMaxWidth) : ''}

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
    }

    .post .post__content {
        display: flex;
        align-items: flex-start;
        gap: var(--spacing-xs);
    }

    .post .post__content > .post__img {
        flex: 0 0 auto;
        width: ${AVATAR_SLOT};
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

    /* The body column (header + message) becomes the bubble. */
    .post ${BUBBLE} {
        /* Anchors the host toolbar (absolute, top right) to the bubble, not the row. */
        position: relative;
        flex: 1 1 auto;
        min-width: 0;
        padding: var(--spacing-m) var(--spacing-l);
        border-radius: 0 var(--radius-xl) var(--radius-xl) var(--radius-xl);
        background: rgba(var(--center-channel-color-rgb), 0.04);
        transition: background var(--duration-quick) var(--ease-transition);

        @media (prefers-reduced-motion: reduce) {
            transition: none;
        }
    }

    ${activeBubble('.post')} {
        background: rgba(var(--center-channel-color-rgb), 0.08);
    }

    /*
     * Toolbar: hidden (and not hit-testable) unless its bubble is active, even
     * though the host reveals it for the whole row. Opacity rather than
     * visibility, because the host sets visibility on the items themselves.
     */
    .post .post-menu {
        opacity: 0;
        pointer-events: none;
    }

    ${activeBubble('.post')} {
        .post-menu {
            opacity: 1;
            pointer-events: auto;
        }
    }

    /* Keyboard focus inside a row must not clip the toolbar (the host only unclips on hover). */
    .post:focus-within {
        overflow: visible;
    }

    /*
     * Short agent replies hug their content: LLMBotPost marks its wrapper
     * hug or full (use_bubble_width.ts) and the bubble follows. Without :has()
     * support the rule never matches and every bubble stays full width.
     */
    .post ${BUBBLE}:has([${BUBBLE_FIT_ATTRIBUTE}='hug']) {
        flex: 0 1 auto;
        width: fit-content;
        max-width: calc(${HUG_MAX_FRACTION} * (100% - ${AVATAR_SLOT} - var(--spacing-xs)));
    }

    /* Width changes animate only for posts that streamed here, never on load. */
    .post ${BUBBLE}:has([${BUBBLE_MOTION_ATTRIBUTE}='on']) {
        interpolate-size: allow-keywords;
        transition:
            background var(--duration-quick) var(--ease-transition),
            flex-grow var(--duration-moderate) var(--ease-transition),
            width var(--duration-moderate) var(--ease-transition),
            max-width var(--duration-moderate) var(--ease-transition);

        @media (prefers-reduced-motion: reduce) {
            transition: none;
        }
    }

    /* Own messages: right-aligned, primary-colored bubble. */
    .post.current--user .post__content {
        flex-direction: row-reverse;
    }

    .post.current--user ${BUBBLE} {
        flex: 0 1 auto;
        max-width: calc(100% - ${AVATAR_SLOT} - var(--spacing-xs));
        border-radius: var(--radius-xl) 0 var(--radius-xl) var(--radius-xl);
        background: var(--button-bg);
        color: var(--button-color);
    }

    /* Shade the primary color toward the text color: darker on light themes, lighter on dark. */
    ${activeBubble('.post.current--user')} {
        background: linear-gradient(rgba(var(--center-channel-color-rgb), 0.12), rgba(var(--center-channel-color-rgb), 0.12)), var(--button-bg);
    }

    /* While editing, the host editor sits in the bubble: use the neutral bubble. */
    .post.current--user.post--editing ${BUBBLE} {
        background: rgba(var(--center-channel-color-rgb), 0.04);
        color: var(--center-channel-color);
    }

    .post.current--user .post__header {
        justify-content: flex-end;
    }

    .post.current--user .post__header .user-popover,
    .post.current--user .post-message__text,
    .post.current--user .post-message__text a {
        color: var(--button-color);
    }

    .post.current--user .post__time {
        color: rgba(var(--button-color-rgb), 0.72);
    }
`;
