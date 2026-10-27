// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {ClockOutlineIcon, PencilOutlineIcon} from '@mattermost/compass-icons/components';
import React from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

import {Button} from '@mattermost/compass-ui/components/button';
import {Icon} from '@mattermost/compass-ui/components/icon';

import {LLMBot} from '@/bots';

import ContextUsageIndicator from './context_usage_indicator';
import ToolProviderPopover, {UserMCPServerInfo} from './tool_provider_popover';

type Props = {
    currentTab: string
    activeBot: LLMBot | null
    setCurrentTab: (tab: string) => void
    selectPost: (postId: string) => void
    disabledServers: string[]
    onDisabledServersChange: (servers: string[]) => void
    preloadedServers?: UserMCPServerInfo[]
    activeConversationId?: string
}

const RHSHeader = (props: Props) => {
    let historyButton = null;
    if (props.currentTab === 'threads') {
        historyButton = (
            <CurrentViewTitle>
                <Icon
                    size='16'
                    glyph={<ClockOutlineIcon/>}
                />
                <FormattedMessage defaultMessage='Chat history'/>
            </CurrentViewTitle>
        );
    } else {
        historyButton = (
            <Button
                emphasis='quaternary'
                size='x-small'
                leadingIcon={<Icon glyph={<ClockOutlineIcon/>}/>}
                data-testid='chat-history'
                onClick={() => {
                    props.setCurrentTab('threads');
                    props.selectPost('');
                }}
            >
                <FormattedMessage defaultMessage='View chat history'/>
            </Button>
        );
    }

    // All right-side controls share one flex group so the ring
    // appearing/disappearing doesn't shift the others.
    return (
        <Header>
            {historyButton}
            <RightControls>
                {props.activeConversationId && (
                    <ContextUsageIndicator conversationId={props.activeConversationId}/>
                )}
                {props.currentTab === 'new' ? (
                    <>
                        {/* SA agents have no per-user connections or preferences to manage. */}
                        {props.activeBot?.useServiceAccountAuth !== true && (
                            <ToolProviderPopover
                                disabledServers={props.disabledServers}
                                onDisabledServersChange={props.onDisabledServersChange}
                                preloadedServers={props.preloadedServers}
                                enabledMCPTools={props.activeBot?.enabledMCPTools}
                                autoEnableNewMCPTools={props.activeBot?.autoEnableNewMCPTools}
                            />
                        )}
                    </>
                ) : (
                    <Button
                        emphasis='tertiary'
                        size='x-small'
                        leadingIcon={<Icon glyph={<PencilOutlineIcon/>}/>}
                        data-testid='new-chat'
                        className='new-button'
                        onClick={() => {
                            props.setCurrentTab('new');
                            props.selectPost('');
                        }}
                    >
                        <FormattedMessage defaultMessage='New chat'/>
                    </Button>
                )}
            </RightControls>
        </Header>
    );
};

const CurrentViewTitle = styled.div`
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 0 8px;
    font-size: 12px;
    font-weight: 600;
    line-height: 16px;
    color: var(--center-channel-color);
`;

const Header = styled.div`
    display: flex;
    height: 38px;
    padding: 0 16px 0 12px;
	justify-content: space-between;
	align-items: center;
    box-sizing: border-box;
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    flex-wrap: wrap;
`;

const RightControls = styled.div`
    display: flex;
    align-items: center;
    gap: 4px;
    margin-left: auto;
`;

export default React.memo(RHSHeader);
