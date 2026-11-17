// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {css} from 'styled-components';

import {noMotionWhenReduced} from '@/components/llmbot_post/motion';

import {
    HOST_ACTION_SEPARATOR,
    HOST_AI_ACTIONS_BUTTON,
    HOST_ATTRIBUTES_BUTTON,
    HOST_BURN_BUTTON,
    HOST_EDITOR_LABELS,
    HOST_EMOJI_BUTTON,
    HOST_FILE_UPLOAD_BUTTON,
    HOST_FORMATTING_BAR,
    HOST_FORMATTING_TOGGLE,
    HOST_PREVIEW_BUTTON,
    HOST_PRIORITY_BUTTON,
    HOST_SCHEDULE_BUTTON,
    HOST_SEND_BUTTON,
    HOST_SEND_SPLIT,
} from './agent_composer_host';

// Agent chat's treatment of the host box. Interpolate into the plugin container that wraps the
// composer (data-agent-composer). It does not apply to a normal channel composer.
//
// The formatting bar starts closed here even when the user's formatting preference has the host
// render it: that preference is shared with the channel composer, so agent chat hides the bar
// instead of clicking the host's toggle. data-agent-formatting="open" shows the bar the host
// already rendered. data-agent-generating="true" hides the host send; the plugin's Stop sits in
// its place.
//
// Host buttons this reaches (mattermost/mattermost advanced_text_editor): the ids in
// agent_composer_host.ts.
export const agentComposerHostStyles = css`
    .AdvancedTextEditor__body {
        /* Half of one line: the 32px control, the inset under it, and the host's 2px border
           (the smallest space token). A one-line box reads as a capsule; a taller box keeps the same corner. */
        border-radius: calc(var(--spacing-xxxl) / 2 + var(--spacing-xs) + var(--spacing-xxxxs));
        box-shadow: var(--elevation-4);
        transition: box-shadow var(--duration-quick) var(--ease-transition);

        ${noMotionWhenReduced}
    }

    .AdvancedTextEditor__body:hover,
    .AdvancedTextEditor__body:focus-within {
        box-shadow: var(--elevation-5);
    }

    .AdvancedTextEditor__cell {
        padding-inline-start: calc(var(--spacing-xxxl) + var(--spacing-m));
    }

    .AdvancedTextEditor ${HOST_FORMATTING_TOGGLE},
    .AdvancedTextEditor ${HOST_FILE_UPLOAD_BUTTON},
    .AdvancedTextEditor ${HOST_EMOJI_BUTTON},
    .AdvancedTextEditor ${HOST_ATTRIBUTES_BUTTON},
    .AdvancedTextEditor ${HOST_PREVIEW_BUTTON},
    .AdvancedTextEditor ${HOST_PRIORITY_BUTTON},
    .AdvancedTextEditor ${HOST_BURN_BUTTON},
    .AdvancedTextEditor ${HOST_AI_ACTIONS_BUTTON},
    .AdvancedTextEditor ${HOST_SCHEDULE_BUTTON},
    .AdvancedTextEditor ${HOST_EDITOR_LABELS},
    ${HOST_ACTION_SEPARATOR} {
        display: none;
    }

    &[data-agent-formatting='closed'] ${HOST_FORMATTING_BAR},
    &[data-agent-formatting='closed'] :has(> ${HOST_FORMATTING_BAR}) {
        display: none;
    }

    &[data-agent-generating='true'] ${HOST_SEND_SPLIT},
    &[data-agent-generating='true'] ${HOST_SEND_BUTTON} {
        visibility: hidden;
    }

    ${HOST_SEND_BUTTON} {
        width: var(--spacing-xxxl);
        height: var(--spacing-xxxl);
        padding: 0;
        border-radius: var(--radius-pill);
    }

    /* The send glyph sits optically left of center. A 2px nudge (the smallest space token) corrects it. */
    ${HOST_SEND_BUTTON} svg {
        transform: translateX(var(--spacing-xxxxs));
    }
`;
