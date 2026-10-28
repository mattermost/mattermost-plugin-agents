// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useRef} from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';

import {Modal} from '@mattermost/compass-ui/components/modal';

import {AnimatedModalShell, MODAL_SHEET_CLASS} from '@/components/animated_modal_shell';
import type {ServiceInfo, UserAgent} from '@/types/agents';

import AgentConfigView from './agent_config_view';

interface AgentSettingsModalProps {
    show: boolean;
    agent: UserAgent;
    services: ServiceInfo[];
    onClose: () => void;
    onSaved: (agent: UserAgent) => void;
    onRestored: (agent: UserAgent) => void;
}

// An agent's settings over the chat it was opened from: the same editor as the All agents page, in a
// modal, so the chat (and the draft in its composer) stays where it is. Close, backdrop and Escape all
// ask the editor to close, which confirms first when there are unsaved changes.
const AgentSettingsModal = ({show, agent, services, onClose, onSaved, onRestored}: AgentSettingsModalProps) => {
    const intl = useIntl();
    const closeRequestRef = useRef<(() => void) | null>(null);
    const requestClose = () => closeRequestRef.current?.();

    return (
        <AnimatedModalShell
            show={show}
            onBackdropClick={requestClose}
            onEscape={requestClose}
            zIndex={2000}
        >
            <Sheet className={MODAL_SHEET_CLASS}>
                <SettingsModal
                    open={show}
                    closeOnEscape={false}
                    size='large'
                    title={intl.formatMessage({defaultMessage: 'Agent settings'})}
                    hideTitle={true}
                    onClose={requestClose}
                    closeLabel={intl.formatMessage({defaultMessage: 'Close'})}
                    headerDivider={false}
                    bodyPadding='none'
                    scrollable={false}
                >
                    <Body>
                        <AgentConfigView
                            mode='edit'
                            variant='modal'
                            agent={agent}
                            services={services}
                            closeRequestRef={closeRequestRef}
                            onBack={onClose}
                            onSaved={onSaved}
                            onRestored={onRestored}
                        />
                    </Body>
                </SettingsModal>
            </Sheet>
        </AnimatedModalShell>
    );
};

const Sheet = styled.div`
    display: flex;
    max-width: calc(100vw - 32px);
`;

// 80vh like the Custom Prompts modal; the editor scrolls inside it.
const SettingsModal = styled(Modal)`
    && {
        height: 80vh;
        max-width: 100%;
    }
`;

// The editor is a flex column that needs a definite height; the page version pads its column by the
// same 32px.
const Body = styled.div`
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    min-height: 0;
    padding: 0 var(--spacing-xxxl);
`;

export default AgentSettingsModal;
