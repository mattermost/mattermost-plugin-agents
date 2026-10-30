// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {MenuItem} from '@mattermost/compass-ui/components/menu-item';
import {Spinner} from '@mattermost/compass-ui/components/spinner';
import {UserAvatar} from '@mattermost/compass-ui/components/user-avatar';

import type {LLMBot} from '@/bots';
import {getProfilePictureUrl} from '@/client';

// No compass or host token for secondary menu text: the compass menu's own
// group titles use this 64% center-channel color.
const SECONDARY_TEXT = 'rgba(var(--center-channel-color-rgb), 0.64)';

// A 16px compass icon for a MenuItem's leading slot.
export const menuIcon = (glyph: React.ReactNode) => (
    <Icon
        glyph={glyph}
        size='16'
    />
);

type MenuItemProps = Omit<React.ComponentProps<typeof MenuItem>, 'role' | 'leadingElement' | 'leadingVisual' | 'label'>;

// A menu action with a 16px leading icon.
export const IconMenuItem = ({icon, label, ...props}: MenuItemProps & {icon: React.ReactNode; label: string}) => (
    <MenuItem
        role='menuitem'
        label={label}
        leadingElement={true}
        leadingVisual={menuIcon(icon)}
        {...props}
    />
);

// An agent as a menu row: its 20px avatar and name.
export const AgentMenuItem = ({bot, ...props}: MenuItemProps & {bot: LLMBot}) => (
    <MenuItem
        role='menuitem'
        label={bot.displayName}
        leadingElement={true}
        leadingVisual={(
            <UserAvatar
                src={getProfilePictureUrl(bot.id, bot.lastIconUpdate)}
                alt=''
                aria-hidden='true'
                name={bot.displayName}
                size='20'
            />
        )}
        {...props}
    />
);

// Loading, empty and error lines inside a menu: text, not items, so arrow
// keys skip them; announced through their live role.
export const StatusRow = styled.p`
    display: flex;
    align-items: center;
    gap: var(--spacing-xs);
    margin: 0;
    padding: var(--spacing-xxs) var(--spacing-l);
    font-family: var(--font-family-body);
    font-size: var(--font-size-75);
    line-height: var(--line-height-75);
    color: ${SECONDARY_TEXT};
`;

export const ErrorRow = styled(StatusRow)`
    color: var(--error-text);
`;

export const LoadingRow = ({label}: {label: string}) => (
    <StatusRow role='status'>
        <Spinner
            size='16'
            aria-hidden={true}
        />
        {label}
    </StatusRow>
);
