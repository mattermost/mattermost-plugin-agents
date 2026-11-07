// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {FieldMessageTone} from '@mattermost/compass-ui/components/field-message';
import type {SectionNoticeProps} from '@mattermost/compass-ui/components/section-notice';
import type {TagType} from '@mattermost/compass-ui/components/tag';

import {exposeValues} from './compass_leaf';

// Every Compass value is either exposed to the LLM under an OpenUI name or
// hidden (null). `satisfies` makes a new or removed Compass value a tsc error.
export const tagColor = exposeValues({
    default: 'neutral',
    info: 'info',
    success: 'success',
    warning: 'warning',
    danger: 'danger',
    'info-dim': null,
} satisfies Record<TagType, string | null>);

export const noticeType = exposeValues({
    info: 'info',
    success: 'success',
    warning: 'warning',
    danger: 'danger',
    hint: 'hint',
} satisfies Record<NonNullable<SectionNoticeProps['type']>, string | null>);

export const statusTone = exposeValues({
    info: 'info',
    success: 'success',
    warning: 'warning',
    error: 'danger',
} satisfies Record<FieldMessageTone, string | null>);

/** Why a Compass component is not offered to the LLM. */
export type ComponentExclusion =

    /** App shell: sidebars, headers, admin console, message chrome. */
    | 'host-chrome'

    /** Modals, popovers, menus, tooltips, toasts. */
    | 'overlay'

    /** Inputs and buttons; owned by the form-actions workstream. */
    | 'interactive'

    /** Needs IDs resolved on the server and must fail closed (users, posts, files). */
    | 'needs-real-data'

    /** A building block other Compass components use. */
    | 'internal';

/** How a mapped component's own prop is used. */
export type PropDecision =

    /** Set from an OpenUI argument. Literal unions need an entry in `values`. */
    | 'arg'

    /** Set to a constant by the plugin. */
    | 'fixed'

    /** Deliberately left at the Compass default. */
    | 'default'

    /** Not reachable from LLM output. */
    | 'callback' | 'slot' | 'styling' | 'interactive';

export type CompassCoverage =
    | {
        status: 'mapped';

        /** OpenUI components that render it. */
        openui: string[];

        /** Every own prop in the manifest snapshot, and nothing else. */
        props: Record<string, PropDecision>;
        values?: Record<string, {map: Record<string, string | null>}>;
    }
    | {status: 'candidate'; note: string}
    | {status: 'excluded'; reason: ComponentExclusion};

const excluded = (reason: ComponentExclusion): CompassCoverage => ({status: 'excluded', reason});

/**
 * Classifies every export in compass_manifest.snapshot.json. compass_coverage.test.ts
 * fails when an export is missing here, or a mapped component's props or
 * values no longer match the snapshot, which is what a Compass bump surfaces.
 */
export const compassCoverage: Record<string, CompassCoverage> = {
    Divider: {status: 'mapped', openui: ['Divider'], props: {}},
    EmptyState: {
        status: 'mapped',
        openui: ['Empty'],
        props: {title: 'arg', description: 'arg', illustration: 'slot', action: 'interactive', className: 'styling'},
    },
    FieldMessage: {
        status: 'mapped',
        openui: ['Status'],
        props: {message: 'arg', tone: 'arg', className: 'styling'},
        values: {tone: statusTone},
    },
    ProgressBar: {
        status: 'mapped',
        openui: ['Progress', 'BarChart'],
        props: {value: 'arg', 'aria-label': 'arg', decorative: 'fixed', size: 'default', semanticColors: 'default', indeterminate: 'default', className: 'styling'},
    },
    SectionNotice: {
        status: 'mapped',
        openui: ['Notice'],
        props: {
            type: 'arg',
            title: 'arg',
            description: 'arg',
            icon: 'slot',
            className: 'styling',
            primaryButtonLabel: 'interactive',
            onPrimaryAction: 'callback',
            primaryActionDisabled: 'interactive',
            primaryActionLoading: 'interactive',
            secondaryButtonLabel: 'interactive',
            onSecondaryAction: 'callback',
            secondaryActionDisabled: 'interactive',
            secondaryActionLoading: 'interactive',
            onDismiss: 'callback',
            dismissLabel: 'interactive',
            dismissButtonProps: 'interactive',
        },
        values: {type: noticeType},
    },
    ShortcutTagGroup: {
        status: 'mapped',
        openui: ['Keys'],
        props: {labels: 'arg', size: 'default', location: 'default', className: 'styling'},
    },
    Tag: {
        status: 'mapped',
        openui: ['Tag'],
        props: {label: 'arg', type: 'arg', size: 'fixed', casing: 'default', leadingIcon: 'slot'},
        values: {type: tagColor},
    },

    Chip: {status: 'candidate', note: 'Display-only chips overlap with Tag; revisit if Tag cannot carry a value.'},
    StatusBadge: {status: 'candidate', note: 'Presence dot; only meaningful next to a resolved user.'},
    Tabs: {status: 'candidate', note: 'Long RHS answers. Needs a streaming rule and a decision on hiding content from View source.'},

    AdminConsoleFooter: excluded('host-chrome'),
    AdminConsoleHeader: excluded('host-chrome'),
    AdminPanel: excluded('host-chrome'),
    AdminPanelFooter: excluded('host-chrome'),
    AdminPanelHeader: excluded('host-chrome'),
    ChannelInfoMsgHeader: excluded('host-chrome'),
    ChannelSidebarItem: excluded('host-chrome'),
    FeatureDiscoveryPanel: excluded('host-chrome'),
    GlobalBanner: excluded('host-chrome'),
    MentionBadge: excluded('host-chrome'),
    MessageActions: excluded('host-chrome'),
    MessageHeader: excluded('host-chrome'),
    MessageSeparator: excluded('host-chrome'),
    MoreUnreadsBanner: excluded('host-chrome'),
    NewMessageBanner: excluded('host-chrome'),
    PaginationDots: excluded('host-chrome'),
    PinnedSavedIndicators: excluded('host-chrome'),
    ReactionButton: excluded('host-chrome'),
    ReactionsRow: excluded('host-chrome'),
    RightSidebarHeader: excluded('host-chrome'),
    Scrollbar: excluded('host-chrome'),
    SearchTipBanner: excluded('host-chrome'),
    Spinner: excluded('host-chrome'),
    ThreadFooter: excluded('host-chrome'),
    ThreadListItem: excluded('host-chrome'),
    UnreadBadge: excluded('host-chrome'),

    Dropdown: excluded('overlay'),
    EmojiPopover: excluded('overlay'),
    MenuGroupHeading: excluded('overlay'),
    MenuItem: excluded('overlay'),
    Modal: excluded('overlay'),
    ModalFooter: excluded('overlay'),
    ModalHeader: excluded('overlay'),
    ModalOverlay: excluded('overlay'),
    PopoverMenu: excluded('overlay'),
    PopoverMenuDivider: excluded('overlay'),
    PopoverMenuGroup: excluded('overlay'),
    PopoverMenuGroupTitle: excluded('overlay'),
    PopoverMenuScroll: excluded('overlay'),
    PopoverMenuTitle: excluded('overlay'),
    PopoverNotice: excluded('overlay'),
    ProfilePopover: excluded('overlay'),
    Toast: excluded('overlay'),
    Tooltip: excluded('overlay'),
    TourPoint: excluded('overlay'),

    Button: excluded('interactive'),
    CardButton: excluded('interactive'),
    CardButtonGroup: excluded('interactive'),
    Checkbox: excluded('interactive'),
    ChipGroup: excluded('interactive'),
    Combobox: excluded('interactive'),
    DateRangePicker: excluded('interactive'),
    EmojiButton: excluded('interactive'),
    ErrorMessage: excluded('interactive'),
    IconButton: excluded('interactive'),
    PasswordInput: excluded('interactive'),
    Radio: excluded('interactive'),
    SearchInput: excluded('interactive'),
    Select: excluded('interactive'),
    Switch: excluded('interactive'),
    TextArea: excluded('interactive'),
    TextInput: excluded('interactive'),
    TileButton: excluded('interactive'),

    AttachmentCard: excluded('needs-real-data'),
    Emoji: excluded('needs-real-data'),
    ImagePreview: excluded('needs-real-data'),
    LinkPreview: excluded('needs-real-data'),
    PermalinkPreview: excluded('needs-real-data'),
    TeamAvatar: excluded('needs-real-data'),
    UserAvatar: excluded('needs-real-data'),
    UserAvatarGroup: excluded('needs-real-data'),

    Icon: excluded('internal'),
    IconSlotContext: excluded('internal'),
    Illustration: excluded('internal'),
    ShortcutTag: excluded('internal'),
};
