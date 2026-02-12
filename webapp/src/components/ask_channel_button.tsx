// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useRef, useState, useEffect} from 'react';
import styled, {css} from 'styled-components';
import {useSelector, useDispatch} from 'react-redux';
import {GlobalState} from '@mattermost/types/store';
//eslint-disable-next-line import/no-unresolved -- react-bootstrap is external
import {OverlayTrigger, Tooltip, Overlay} from 'react-bootstrap';

import {doChannelAnalysis} from '@/client';
import {openRHS} from '@/redux_actions';

import {useBotlist} from '@/bots';

import IconAI from './assets/icon_ai';
import {ChannelSummarizePopover} from './channel_summarize_popover';

interface ButtonContainerProps {
    isActive: boolean;
}

const ButtonContainer = styled.button<ButtonContainerProps>`
    display: flex;
    align-items: center;
    justify-content: center;
    background: transparent;
    border-radius: 4px;
    border: none;
    padding: 6px;
    cursor: pointer;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    transition: background 0.15s ease-in-out, color 0.15s ease-in-out;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.08);
        color: rgba(var(--center-channel-color-rgb), 0.72);
    }

    &:active {
        background: rgba(var(--center-channel-color-rgb), 0.16);
        color: rgba(var(--center-channel-color-rgb), 0.72);
    }

    ${({isActive}) => isActive && css`
        background: rgba(var(--button-bg-rgb), 0.08);
        color: var(--button-bg);

        &:hover {
            background: rgba(var(--button-bg-rgb), 0.12);
            color: var(--button-bg);
        }

        &:active {
            background: rgba(var(--button-bg-rgb), 0.16);
            color: var(--button-bg);
        }
    `}

    svg {
        width: 16px;
        height: 16px;
        display: block;
    }
`;

const PopoverWrapper = React.forwardRef((props: any, ref: any) => {
    const {
        style,
        className,
        positionTop,
        positionLeft,
        children,
        // eslint-disable-next-line @typescript-eslint/no-unused-vars
        arrowOffsetLeft,
        // eslint-disable-next-line @typescript-eslint/no-unused-vars
        arrowOffsetTop,
        // eslint-disable-next-line @typescript-eslint/no-unused-vars
        placement,
        ...rest
    } = props;

    return (
        <div
            ref={ref}
            className={className}
            style={{
                ...style,
                position: 'absolute',
                top: positionTop,
                left: positionLeft,
                zIndex: 1000,
                marginTop: '8px',
                marginLeft: '148px',
            }}
            {...rest}
        >
            {children}
        </div>
    );
});

const AskChannelButton = () => {
    const dispatch = useDispatch();
    const [showPopover, setShowPopover] = useState(false);
    const target = useRef<HTMLButtonElement>(null);
    const {bots, activeBot, setActiveBot} = useBotlist();

    const currentChannelId = useSelector((state: GlobalState) => state.entities.channels.currentChannelId);
    const currentTeamId = useSelector((state: GlobalState) => state.entities.teams.currentTeamId);
    const currentChannel = useSelector((state: GlobalState) => state.entities.channels.channels[currentChannelId]);
    const lastViewedAt = useSelector((state: GlobalState) => state.entities.channels.myMembers[currentChannelId]?.last_viewed_at || 0);
    const [initialLastViewedAt, setInitialLastViewedAt] = useState(lastViewedAt);

    useEffect(() => {
        setInitialLastViewedAt(lastViewedAt);
    }, [currentChannelId]);

    const channelName = currentChannel?.display_name || 'Current Channel';

    const handleSummarize = async (options: any) => {
        if (!activeBot) {
            return;
        }

        setShowPopover(false);

        // Open RHS
        dispatch(openRHS());

        const result = await doChannelAnalysis(currentChannelId, 'summarize_channel', activeBot.username, {
            ...options,
            team_id: currentTeamId,
        });
        dispatch({type: 'SELECT_AI_POST', postId: result.postid});
    };

    // Handle clicking outside to close
    useEffect(() => {
        const handleDocumentClick = (e: MouseEvent) => {
            if (target.current && !target.current.contains(e.target as Node)) {
                // Check if the click is inside the popover
                const popover = document.querySelector('.channel-summarize-popover');
                if (popover && popover.contains(e.target as Node)) {
                    return;
                }
                setShowPopover(false);
            }
        };

        if (showPopover) {
            document.addEventListener('mousedown', handleDocumentClick);
        }

        return () => {
            document.removeEventListener('mousedown', handleDocumentClick);
        };
    }, [showPopover]);

    const handleToggle = () => {
        setShowPopover(!showPopover);
    };

    const tooltip = (
        <Tooltip id='ask-agents-tooltip'>
            {'Ask Agents about'}
            <br/>
            {'this channel'}
        </Tooltip>
    );

    return (
        <>
            {showPopover ? (
                <ButtonContainer
                    ref={target}
                    onClick={handleToggle}
                    isActive={showPopover}
                >
                    <IconAI/>
                </ButtonContainer>
            ) : (
                <OverlayTrigger
                    placement='bottom'
                    overlay={tooltip}
                >
                    <ButtonContainer
                        ref={target}
                        onClick={handleToggle}
                        isActive={showPopover}
                    >
                        <IconAI/>
                    </ButtonContainer>
                </OverlayTrigger>
            )}
            <Overlay
                target={() => target.current}
                show={showPopover}
                placement='bottom'
                rootClose={true}
                onHide={() => setShowPopover(false)}
            >
                <PopoverWrapper className='channel-summarize-popover'>
                    <ChannelSummarizePopover
                        bots={bots || []}
                        activeBot={activeBot}
                        setActiveBot={setActiveBot}
                        channelName={channelName}
                        onSummarize={handleSummarize}
                        lastViewedAt={initialLastViewedAt}
                    />
                </PopoverWrapper>
            </Overlay>
        </>
    );
};

export default AskChannelButton;
