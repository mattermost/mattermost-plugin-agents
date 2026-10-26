// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

/*
 * TEMPORARY LOCAL COPY. Replace with the compass-ui export once ChannelHeader is promoted out of
 * compass-proto.
 *
 * Ported from the `ChannelHeader` component of the private, unpublished `@mattermost/compass-proto`
 * (version 0.1.0-alpha.3):
 *   ~/Documents/GitHub/mattermost/compass-design/packages/compass-proto/src/components/ChannelHeader/
 *   (ChannelHeader.tsx + ChannelHeader.module.scss)
 *
 * DM variant only. Differences from the proto, so the swap is mechanical:
 *  - Omitted: `type` (channel/bot/gm/threads/drafts), `memberCount`, `pinnedCount`, `favorited`,
 *    `muted`, `onFavoriteClick`, `onMembersClick`, `membersToggled`, the stat icons, and the
 *    proto's hardcoded English labels.
 *  - Kept with the same names/meaning: `name`, `description`, `avatarSrc`, `avatarStatus`,
 *    `onNameClick`, `className`.
 *  - Replaced: the proto's fixed "Start a Call" + "Channel info" buttons (`onCallClick`,
 *    `callButton`, `onInfoClick`, `infoToggled`) by a generic trailing `actions` slot.
 *  - Added (not in the proto): `nameSlot` (caller-owned name trigger, e.g. a menu button),
 *    `ariaLabel` (the header is a <header> landmark; the name is an <h1>).
 *  - Root is a <header> instead of a <div>; the avatar is decorative (the name is adjacent).
 *  - Styling: scss translated to styled-components; the proto's translucent rgba colors use the
 *    host's theme variables (see SECONDARY_TEXT_PERCENT).
 *  - Swap: when promoted, `import {ChannelHeader} from '@mattermost/compass-ui/...'`; the extra
 *    props (`nameSlot`, `actions`, `ariaLabel`) must exist upstream or be dropped.
 */

import React from 'react';
import styled from 'styled-components';
import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';

import {Button} from '@mattermost/compass-ui/components/button';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {UserAvatar} from '@mattermost/compass-ui/components/user-avatar';

// No compass token for a translucent text color (the proto uses rgba(center-channel-color-rgb, 0.75)).
const SECONDARY_TEXT_PERCENT = '75%';

export interface ChannelHeaderProps {

    /** Conversation / DM name. */
    name: string;

    /** Secondary text next to the name (e.g. a chat title or description). */
    description?: string;

    /** Avatar image URL. */
    avatarSrc?: string;

    /** Show the online status badge on the avatar. */
    avatarStatus?: boolean;

    /** Called when the default name button is clicked. Ignored when `nameSlot` is given. */
    onNameClick?: (e: React.MouseEvent<HTMLButtonElement>) => void;

    /** Replaces the default name button, e.g. with a menu trigger. Rendered inside the <h1>. */
    nameSlot?: React.ReactNode;

    /** Trailing actions (compass IconButtons). No container is rendered when empty. */
    actions?: React.ReactNode;

    /** Accessible name of the <header> landmark. */
    ariaLabel?: string;
    className?: string;
}

const ChannelHeader = ({
    name,
    description,
    avatarSrc,
    avatarStatus = false,
    onNameClick,
    nameSlot,
    actions,
    ariaLabel,
    className,
}: ChannelHeaderProps) => {
    let nameContent: React.ReactNode = <NameText>{name}</NameText>;
    if (nameSlot) {
        nameContent = nameSlot;
    } else if (onNameClick) {
        nameContent = (
            <NameButton
                emphasis='quaternary'
                size='small'
                trailingIcon={<Icon glyph={<ChevronDownIcon/>}/>}
                onClick={onNameClick}
            >
                {name}
            </NameButton>
        );
    }

    return (
        <Root
            className={className}
            aria-label={ariaLabel}
        >
            <Left>
                <UserAvatar
                    src={avatarSrc}
                    alt=''
                    aria-hidden='true'
                    name={name}
                    size='24'
                    status={avatarStatus}
                />
                <Name>{nameContent}</Name>
                {description && <Description>{description}</Description>}
            </Left>
            {actions && <Actions>{actions}</Actions>}
        </Root>
    );
};

// Proto: height 56px = padding spacing-m + 32px content. Host: border-bottom --border-default, bg center-channel-bg.
const Root = styled.header`
    display: flex;
    box-sizing: border-box;
    flex: 0 0 auto;
    align-items: center;
    gap: var(--spacing-l);
    min-height: calc(var(--spacing-xxxl) + 2 * var(--spacing-m));
    padding: var(--spacing-m);
    background: var(--center-channel-bg);
    border-bottom: var(--border-default);
`;

const Left = styled.div`
    display: flex;
    flex: 1 0 0;
    align-items: center;
    gap: var(--spacing-xxxs);
    min-width: 0;
    padding-inline-start: var(--spacing-xxxs);
`;

const Name = styled.h1`
    display: flex;
    flex-shrink: 0;
    min-width: 0;
    max-width: 100%;
    margin: 0;
    font-family: var(--font-family-heading);
    font-size: var(--font-size-200);
    font-weight: var(--font-weight-semibold);
    line-height: var(--line-height-200);
    color: var(--center-channel-color);
`;

const NameText = styled.span`
    padding-inline: var(--spacing-xxs);
    white-space: nowrap;
`;

// The proto's name button is a bare <button>; compass quaternary Button gives the same hover/focus.
const NameButton = styled(Button)`
    && {
        min-width: 0;
        padding-inline: var(--spacing-xxs);
        font: inherit;
        color: inherit;
    }
`;

const Description = styled.div`
    flex: 1 0 0;
    min-width: 0;
    margin-inline-start: var(--spacing-xs);
    overflow: hidden;
    font-family: var(--font-family-body);
    font-size: var(--font-size-75);
    font-weight: var(--font-weight-regular);
    line-height: var(--line-height-75);
    color: color-mix(in srgb, var(--center-channel-color) ${SECONDARY_TEXT_PERCENT}, transparent);
    text-overflow: ellipsis;
    white-space: nowrap;
`;

const Actions = styled.div`
    display: flex;
    flex-shrink: 0;
    align-items: center;
    gap: var(--spacing-xxxs);
`;

export default ChannelHeader;
