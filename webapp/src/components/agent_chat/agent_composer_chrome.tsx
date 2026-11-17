// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useLayoutEffect, useRef, useState} from 'react';
import {createPortal} from 'react-dom';
import {useIntl} from 'react-intl';
import {useSelector} from 'react-redux';
import styled, {css} from 'styled-components';

import SquareIcon from '@mattermost/compass-icons/components/square';

import {ErrorMessage} from '@mattermost/compass-ui/components/error-message';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';

import {WithTooltip} from '@mattermost/shared/components/tooltip';

import {GlobalState} from '@mattermost/types/store';

import manifest from '@/manifest';
import {doStopGenerating} from '@/client';
import {TooltipTrigger} from '@/components/dropdown';
import {VisuallyHidden} from '@/components/visually_hidden';

import {agentComposerHostStyles} from './agent_composer_mode';
import AgentComposerMenu from './agent_composer_menu';
import {
    clickFormattingToggle,
    formattingBarIsRendered,
    HOST_EDITOR_BODY,
    setHostSendDisabled,
} from './agent_composer_host';
import {markGenerationStopped, noteLivePost, useGeneratingPostId} from './use_thread_generating';

interface BotLike {
    id: string;
    dmChannelID?: string;
}

interface AgentComposerChromeProps {
    children: React.ReactNode;

    // The thread root. Empty for a new chat, which has nothing to stop.
    rootId: string;
    channelId?: string;
    botId?: string;

    // The thread pane fills its parent. A new chat does not.
    fill?: boolean;
}

// Wraps the host composer in agent chat: one +, send that becomes stop while
// this thread generates, and the box treatment in agentComposerHostStyles.
// The host editor stays mounted, so WYSIWYG keeps working.
const AgentComposerChrome = ({children, rootId, channelId: channelIdProp, botId: botIdProp, fill = false}: AgentComposerChromeProps) => {
    const intl = useIntl();
    const rootRef = useRef<HTMLDivElement>(null);
    const openedHostBar = useRef(false);
    const [body, setBody] = useState<HTMLElement | null>(null);
    const [formattingOpen, setFormattingOpen] = useState(false);
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState('');
    const [status, setStatus] = useState('');
    const postId = useGeneratingPostId(rootId);

    const channelFromPost = useSelector((state: GlobalState) => {
        const posts = state.entities?.posts?.posts as Record<string, {channel_id?: string} | undefined> | undefined;
        return (rootId && posts?.[rootId]?.channel_id) || '';
    });
    const channelId = channelIdProp || channelFromPost;

    const botFromChannel = useSelector((state: GlobalState) => {
        const plugin = (state as unknown as {[_key: string]: {bots?: BotLike[] | null} | undefined})[`plugins-${manifest.id}`];
        return plugin?.bots?.find((bot) => Boolean(bot.dmChannelID) && bot.dmChannelID === channelId)?.id ?? '';
    });
    const botId = botIdProp || botFromChannel;

    useLayoutEffect(() => {
        setBody(rootRef.current?.querySelector<HTMLElement>(HOST_EDITOR_BODY) ?? null);
    }, []);

    useEffect(() => () => {
        if (openedHostBar.current) {
            clickFormattingToggle(rootRef.current);
            openedHostBar.current = false;
        }
    }, []);

    useEffect(() => {
        const root = rootRef.current;
        setHostSendDisabled(root, busy);
        return () => setHostSendDisabled(root, false);
    }, [busy]);

    const toggleFormatting = () => {
        const opening = !formattingOpen;
        if (opening && !formattingBarIsRendered(rootRef.current)) {
            // The host only renders the bar when the shared preference is on. Opening it here
            // clicks that toggle, and closing the bar (or leaving the chat) clicks it back.
            openedHostBar.current = clickFormattingToggle(rootRef.current);
        } else if (!opening && openedHostBar.current) {
            clickFormattingToggle(rootRef.current);
            openedHostBar.current = false;
        }
        setFormattingOpen(opening);
    };

    const stop = async () => {
        if (!postId) {
            return;
        }
        const id = postId;
        markGenerationStopped(id);
        setError('');
        try {
            await doStopGenerating(id);
        } catch {
            noteLivePost(id);
            setStatus('');
            setError(intl.formatMessage({defaultMessage: 'Couldn\'t stop generating. Try again.'}));
        }
    };

    const controls = (
        <>
            <AgentComposerMenu
                rootRef={rootRef}
                formattingOpen={formattingOpen}
                onToggleFormatting={toggleFormatting}
                channelId={channelId}
                botId={botId}
                busy={busy}
                onBusy={setBusy}
                onError={setError}
                onStatus={setStatus}
            />
            {postId && (
                <WithTooltip title={intl.formatMessage({defaultMessage: 'Stop'})}>
                    <TooltipTrigger>
                        <IconButton
                            icon={<Icon glyph={<SquareIcon/>}/>}
                            aria-label={intl.formatMessage({defaultMessage: 'Stop generating'})}
                            rounded={true}
                            size='small'
                            data-testid='agent-composer-stop'
                            onClick={stop}
                        />
                    </TooltipTrigger>
                </WithTooltip>
            )}
        </>
    );

    return (
        <Root
            ref={rootRef}
            $fill={fill}
            data-agent-composer=''
            data-agent-formatting={formattingOpen ? 'open' : 'closed'}
            data-agent-generating={postId ? 'true' : 'false'}
            data-testid='agent-composer'
        >
            {children}
            {body ? createPortal(<ControlBar>{controls}</ControlBar>, body) : <FallbackBar>{controls}</FallbackBar>}
            <VisuallyHidden
                role='status'
                aria-live='polite'
            >
                {status}
            </VisuallyHidden>
            {error && (
                <ErrorLine>
                    <ErrorMessage message={error}/>
                </ErrorLine>
            )}
        </Root>
    );
};

const Root = styled.div<{$fill: boolean}>`
    position: relative;

    ${({$fill}) => $fill && css`
        display: flex;
        flex: 1 1 auto;
        flex-direction: column;
        min-height: 0;
        height: 100%;
    `}

    ${agentComposerHostStyles}
`;

// One above the host's action row (z-index 2). No z-index token. The bar itself
// ignores clicks so it does not cover the text; the buttons take them.
const ControlBar = styled.div`
    position: absolute;
    z-index: 3;
    right: var(--spacing-xs);
    bottom: var(--spacing-xs);
    left: var(--spacing-xs);
    display: flex;
    justify-content: space-between;
    pointer-events: none;

    > * {
        pointer-events: auto;
    }
`;

const FallbackBar = styled.div`
    display: flex;
    justify-content: space-between;
    padding: var(--spacing-xs);
`;

const ErrorLine = styled.div`
    margin-top: var(--spacing-xs);
`;

export default AgentComposerChrome;
