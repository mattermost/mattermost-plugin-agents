// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useLayoutEffect, useRef, useState} from 'react';
import ReactDOM from 'react-dom';
import {useSelector, useDispatch} from 'react-redux';
import styled from 'styled-components';
import {GlobalState} from '@mattermost/types/store';
import {useIntl} from 'react-intl';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {WithTooltip} from '@mattermost/shared/components/tooltip';

import {doChannelAnalysis} from '@/client';
import {openRHS} from '@/redux_actions';
import {useIsLicensedFor} from '@/license';
import {isCurrentChannelAgentDM} from '@/agent_dm_view';

import {useBotlist} from '@/bots';

import IconAI from './assets/icon_ai';
import {ChannelSummarizePopover} from './channel_summarize_popover';
import {isDialogOpen} from './animated_modal_shell';
import {OPEN_POPUP_ATTRIBUTE} from './dropdown';

const POPOVER_FALLBACK_WIDTH = 328;
const POPOVER_GAP = 8;
const VIEWPORT_PADDING = 8;

const PopoverPortal = styled.div.attrs<{$top: number; $left: number}>((props) => ({
    style: {
        top: `${props.$top}px`,
        left: `${props.$left}px`,
    },
}))<{$top: number; $left: number}>`
    position: fixed;
    z-index: 1000;
`;

const AskChannelButton = () => {
    const intl = useIntl();
    const dispatch = useDispatch();
    const [showPopover, setShowPopover] = useState(false);
    const [popoverPos, setPopoverPos] = useState({top: 0, left: 0});
    const target = useRef<HTMLButtonElement>(null);
    const popoverRef = useRef<HTMLDivElement>(null);
    const {bots, activeBot, setActiveBot} = useBotlist();
    const channelSummarizationLicensed = useIsLicensedFor('channel_summarization');

    const currentChannelId = useSelector((state: GlobalState) => state.entities.channels.currentChannelId);
    const currentTeamId = useSelector((state: GlobalState) => state.entities.teams.currentTeamId);
    const currentChannel = useSelector((state: GlobalState) => state.entities.channels.channels[currentChannelId]);
    const lastViewedAt = useSelector((state: GlobalState) => state.entities.channels.myMembers[currentChannelId]?.last_viewed_at || 0);
    const inAgentDM = useSelector(isCurrentChannelAgentDM);
    const [initialLastViewedAt, setInitialLastViewedAt] = useState(lastViewedAt);

    useEffect(() => {
        setInitialLastViewedAt(lastViewedAt);
    }, [currentChannelId]);

    const channelName = currentChannel?.display_name || 'Current Channel';

    const updatePosition = useCallback(() => {
        if (!target.current) {
            return;
        }
        const rect = target.current.getBoundingClientRect();
        const popoverWidth = popoverRef.current?.offsetWidth || POPOVER_FALLBACK_WIDTH;
        const maxLeft = Math.max(VIEWPORT_PADDING, window.innerWidth - popoverWidth - VIEWPORT_PADDING);
        const top = rect.bottom + POPOVER_GAP;
        const left = Math.min(Math.max(VIEWPORT_PADDING, rect.left), maxLeft);
        setPopoverPos((prev) => (prev.top === top && prev.left === left ? prev : {top, left}));
    }, []);

    useLayoutEffect(() => {
        if (!showPopover) {
            return () => { /* empty */ };
        }
        updatePosition();
        window.addEventListener('resize', updatePosition);
        window.addEventListener('scroll', updatePosition, true);
        return () => {
            window.removeEventListener('resize', updatePosition);
            window.removeEventListener('scroll', updatePosition, true);
        };
    }, [showPopover, updatePosition]);

    const handleSummarize = async (options: any) => {
        if (!activeBot) {
            return;
        }

        setShowPopover(false);

        // Open RHS
        dispatch(openRHS());

        const result = await doChannelAnalysis(currentChannelId, 'summarize_channel', activeBot.id, {
            ...options,
            team_id: currentTeamId,
        });
        dispatch({type: 'SELECT_AI_POST', postId: result.postid});
    };

    useEffect(() => {
        if (!showPopover) {
            return () => { /* empty */ };
        }

        const handleDocumentClick = (e: MouseEvent) => {
            const node = e.target as Node | null;
            if (!node) {
                return;
            }
            if (
                target.current?.contains(node) ||
                popoverRef.current?.contains(node) ||
                (node instanceof Element && Boolean(node.closest(`[${OPEN_POPUP_ATTRIBUTE}], [role="dialog"]`)))
            ) {
                return;
            }
            setShowPopover(false);
        };

        const handleKeyDown = (e: KeyboardEvent) => {
            if (e.key !== 'Escape') {
                return;
            }

            // Let nested dropdown menus or visible modals handle Escape first.
            const hasOpenDialog = Array.from(document.querySelectorAll<HTMLElement>('[role="dialog"]')).some(isDialogOpen);
            if (document.querySelector(`[${OPEN_POPUP_ATTRIBUTE}]`) || hasOpenDialog) {
                return;
            }
            setShowPopover(false);
        };

        document.addEventListener('mousedown', handleDocumentClick);
        document.addEventListener('keydown', handleKeyDown);

        return () => {
            document.removeEventListener('mousedown', handleDocumentClick);
            document.removeEventListener('keydown', handleKeyDown);
        };
    }, [showPopover]);

    const handleToggle = () => {
        setShowPopover(!showPopover);
    };

    // Asking an agent about a chat with an agent adds nothing.
    if (!channelSummarizationLicensed || inAgentDM) {
        return null;
    }

    const buttonLabel = intl.formatMessage({defaultMessage: 'Ask Agents about this channel'});

    return (
        <>
            <WithTooltip title={showPopover ? '' : buttonLabel}>
                <IconButton
                    ref={target}
                    onClick={handleToggle}
                    size='x-small'
                    toggled={showPopover}
                    icon={<Icon glyph={<IconAI/>}/>}
                    aria-label={buttonLabel}
                    data-testid='ask-channel-button'
                />
            </WithTooltip>
            {showPopover && ReactDOM.createPortal(
                <PopoverPortal
                    ref={popoverRef}
                    className='channel-summarize-popover in'
                    $top={popoverPos.top}
                    $left={popoverPos.left}
                >
                    <ChannelSummarizePopover
                        bots={bots || []}
                        activeBot={activeBot}
                        setActiveBot={setActiveBot}
                        channelName={channelName}
                        onSummarize={handleSummarize}
                        lastViewedAt={initialLastViewedAt}
                    />
                </PopoverPortal>,
                document.body,
            )}
        </>
    );
};

export default AskChannelButton;
