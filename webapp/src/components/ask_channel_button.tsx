// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useRef, useState, useEffect} from 'react';
import {useSelector, useDispatch} from 'react-redux';
import {GlobalState} from '@mattermost/types/store';
//eslint-disable-next-line import/no-unresolved -- react-bootstrap is external
import {OverlayTrigger, Tooltip, Overlay} from 'react-bootstrap';
import {FormattedMessage, useIntl} from 'react-intl';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';

import {doChannelAnalysis} from '@/client';
import {openRHS} from '@/redux_actions';
import {useIsLicensedFor} from '@/license';

import {useBotlist} from '@/bots';

import IconAI from './assets/icon_ai';
import {ChannelSummarizePopover} from './channel_summarize_popover';

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
        // eslint-disable-next-line @typescript-eslint/no-unused-vars
        arrowRef,
        // eslint-disable-next-line @typescript-eslint/no-unused-vars
        arrowStyle,
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
    const intl = useIntl();
    const dispatch = useDispatch();
    const [showPopover, setShowPopover] = useState(false);
    const target = useRef<HTMLButtonElement>(null);
    const {bots, activeBot, setActiveBot} = useBotlist();
    const channelSummarizationLicensed = useIsLicensedFor('channel_summarization');

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

    if (!channelSummarizationLicensed) {
        return null;
    }

    const buttonLabel = intl.formatMessage({defaultMessage: 'Ask Agents about this channel'});
    const tooltip = (
        <Tooltip id='ask-agents-tooltip'>
            <FormattedMessage defaultMessage='Ask Agents about this channel'/>
        </Tooltip>
    );

    return (
        <>
            {showPopover ? (
                <IconButton
                    ref={target}
                    onClick={handleToggle}
                    size='x-small'
                    toggled={showPopover}
                    icon={<Icon glyph={<IconAI/>}/>}
                    aria-label={buttonLabel}
                    title={buttonLabel}
                    data-testid='ask-channel-button'
                />
            ) : (
                <OverlayTrigger
                    placement='bottom'
                    overlay={tooltip}
                >
                    <IconButton
                        ref={target}
                        onClick={handleToggle}
                        size='x-small'
                        toggled={showPopover}
                        icon={<Icon glyph={<IconAI/>}/>}
                        aria-label={buttonLabel}
                        title={buttonLabel}
                        data-testid='ask-channel-button'
                    />
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
