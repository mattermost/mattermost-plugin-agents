// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useRef} from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';

import {Modal} from '@mattermost/compass-ui/components/modal';

import {AnimatedModalShell} from '@/components/animated_modal_shell';
import type {ServiceInfo, UserAgent} from '@/types/agents';

import AgentConfigView, {type AgentConfigFrameParts} from './agent_config_view';

interface AgentSettingsModalProps {
    show: boolean;
    agent: UserAgent;
    services: ServiceInfo[];
    onClose: () => void;
    onSaved: (agent: UserAgent) => void;
    onRestored: (agent: UserAgent) => void;
}

// An agent's settings over the chat it was opened from: the same editor as the All agents page, in a
// modal, so the chat (and the draft in its composer) stays where it is. The compass Modal supplies the
// header (title, version line, close) and the footer (Cancel, Save); the editor supplies what goes in
// them. Close, backdrop and Escape all ask the editor to close, which confirms first when there are
// unsaved changes.
const AgentSettingsModal = ({show, agent, services, onClose, onSaved, onRestored}: AgentSettingsModalProps) => {
    const intl = useIntl();
    const closeRequestRef = useRef<(() => void) | null>(null);
    const requestClose = () => closeRequestRef.current?.();

    const renderFrame = ({title, subtitle, headerAction, tabs, body, footer}: AgentConfigFrameParts) => (
        <SettingsModal
            open={show}
            closeOnEscape={false}
            size='large'
            title={title}
            {...(subtitle ? {subtitle, subtitlePlacement: 'below' as const} : {})}
            headerAction={headerAction}
            onClose={requestClose}
            closeLabel={intl.formatMessage({defaultMessage: 'Close'})}
            headerDivider={false}
            headerSlot={tabs}
            bodyPadding='none'
            scrollable={false}
            footer={footer}
        >
            <Body>
                {body}
            </Body>
        </SettingsModal>
    );

    return (
        <AnimatedModalShell
            show={show}
            onBackdropClick={requestClose}
            onEscape={requestClose}
            zIndex={2000}
        >
            <Sheet>
                <AgentConfigView
                    mode='edit'
                    variant='modal'
                    agent={agent}
                    services={services}
                    closeRequestRef={closeRequestRef}
                    renderFrame={renderFrame}
                    onBack={onClose}
                    onSaved={onSaved}
                    onRestored={onRestored}
                />
            </Sheet>
        </AnimatedModalShell>
    );
};

const Sheet = styled.div`
    display: flex;
    max-width: calc(100vw - 32px);
`;

// 80vh like the Custom Prompts modal; the editor's body scrolls inside it.
const SettingsModal = styled(Modal)`
    && {
        height: 80vh;
        max-width: 100%;
    }

    /* The tab bar fills the header slot edge to edge; the tab list sets its own inset. (The class name is compass's generated one.) */
    && [class*='modal-header__slot'] {
        padding: 0;
    }
`;

// A flex column with a definite height, which the editor's scrolling panel fills.
const Body = styled.div`
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    min-height: 0;
    height: 100%;
`;

export default AgentSettingsModal;
