// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useState} from 'react';
import {useDispatch} from 'react-redux';
import {FormattedMessage, useIntl} from 'react-intl';
import styled from 'styled-components';
import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {UserAvatar} from '@mattermost/compass-ui/components/user-avatar';

//@ts-ignore it exists
import aiIcon from '../../../../assets/bot_icon.png';

import {useBotlist, type LLMBot} from '@/bots';
import {getProfilePictureUrl} from '@/client';
import {BotDropdownMenu, BotMenuContent} from '@/components/bot_selector';
import Dropdown from '@/components/dropdown';
import {useMenuTransition} from '@/hooks/use_menu_transition';

// No compass token for the translucent hover fill (the agent chat header's name button uses the same).
const HOVER_PERCENT = '8%';

const AgentAvatar = ({bot}: {bot: LLMBot}) => (
    <UserAvatar
        src={getProfilePictureUrl(bot.id, bot.lastIconUpdate)}
        alt=''
        aria-hidden='true'
        name={bot.displayName}
        size='24'
    />
);

// The RHS header title: the active agent, which opens the agent list when there is more than one.
// The host wraps the title in its own heading landmark (a span#rhsPanelTitle), so none is added here.
const RHSTitle = () => {
    const intl = useIntl();
    const {bots, activeBot, setActiveBot} = useBotlist();
    const dispatch = useDispatch();

    // Switching agents behaves like starting a new chat with the new agent (leave the open
    // thread and show the new-chat tab); choosing the current agent changes nothing.
    const switchAgent = useCallback((bot: LLMBot | null) => {
        if (!bot || bot.id === activeBot?.id) {
            return;
        }
        setActiveBot(bot);
        dispatch({type: 'SELECT_AI_POST', postId: ''});
        dispatch({type: 'AI_RHS_NEW_CHAT'});
    }, [activeBot?.id, setActiveBot, dispatch]);
    const [open, setOpen] = useState(false);
    const menu = useMenuTransition(open);

    // Loading, no agents, or an unresolved active agent: the generic title.
    if (!bots || bots.length === 0 || !activeBot) {
        return (
            <Container data-testid='rhs-title'>
                <FallbackIcon
                    src={aiIcon}
                    alt=''
                />
                <Name><FormattedMessage defaultMessage='Agents'/></Name>
            </Container>
        );
    }

    if (bots.length === 1) {
        return (
            <Container data-testid='rhs-title'>
                <AgentAvatar bot={activeBot}/>
                <Name title={activeBot.displayName}>{activeBot.displayName}</Name>
            </Container>
        );
    }

    const label = intl.formatMessage({defaultMessage: '{name}, choose agent'}, {name: activeBot.displayName});
    return (
        <Container data-testid='rhs-title'>
            <AgentAvatar bot={activeBot}/>
            <Dropdown
                isOpen={menu.rendered}
                onOpenChange={setOpen}
                placement='bottom-start'
                target={(
                    <TitleButton
                        type='button'
                        aria-label={label}
                        title={activeBot.displayName}
                        aria-haspopup='menu'
                        aria-expanded={open}
                        data-testid='bot-selector-rhs'
                        onClick={() => setOpen(!open)}
                    >
                        <Name>{activeBot.displayName}</Name>
                        <Icon
                            glyph={<ChevronDownIcon/>}
                            size='16'
                        />
                    </TitleButton>
                )}
            >
                <BotDropdownMenu
                    open={open}
                    onExited={menu.onExited}
                    role='menu'
                    aria-label={label}
                    onClick={(e: React.MouseEvent) => {
                        e.stopPropagation();
                        setOpen(false);
                    }}
                >
                    <BotMenuContent
                        bots={bots}
                        activeBot={activeBot}
                        setActiveBot={switchAgent}
                        itemRole='menuitem'
                    />
                </BotDropdownMenu>
            </Dropdown>
        </Container>
    );
};

// min-width: 0 all the way down lets a long name ellipsize instead of pushing the
// host's expand, popout and close buttons out of the header.
const Container = styled.span`
    display: flex;
    min-width: 0;
    align-items: center;
    gap: var(--spacing-xs);
    margin-left: var(--spacing-xs);
`;

const FallbackIcon = styled.img`
    width: var(--icon-size-24);
    height: var(--icon-size-24);
    flex: 0 0 auto;
    border-radius: var(--radius-pill);
`;

const Name = styled.span`
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
`;

const TitleButton = styled.button`
    display: inline-flex;
    min-width: 0;
    align-items: center;
    margin: 0;
    padding: 0 var(--spacing-xxxs) 0 var(--spacing-xxs);
    border: none;
    border-radius: var(--radius-s);
    background: transparent;
    color: inherit;
    font: inherit;
    cursor: pointer;
    text-align: left;

    &:hover,
    &[aria-expanded='true'] {
        background: color-mix(in srgb, var(--center-channel-color) ${HOVER_PERCENT}, transparent);
    }

    &:focus-visible {
        outline: var(--spacing-xxxxs) solid var(--focus-ring-color);
        outline-offset: var(--spacing-xxxxs);
    }

    svg {
        flex-shrink: 0;
    }
`;

export default RHSTitle;
