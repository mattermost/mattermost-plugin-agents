// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import {FormattedMessage} from 'react-intl';
import styled from 'styled-components';

import {ChannelAutoReplyMode} from '@/client';
import {useIsLicensedFor} from '@/license';

import {setChannelAutoReplySaveError, useChannelAutoReplyDraft} from './autoreply_state';

type Props = {
    informChange: (name: string, value: string) => void;
};

const AutoReplyModePicker = ({informChange}: Props) => {
    const draft = useChannelAutoReplyDraft();
    const autoReplyLicensed = useIsLicensedFor('channel_auto_reply');
    const [localMode, setLocalMode] = useState<ChannelAutoReplyMode | null>(null);

    if (!draft) {
        return null;
    }

    const selectedMode = localMode ?? draft.saved.mode;
    const showRootPosts = autoReplyLicensed || selectedMode === 'root_posts' || draft.saved.mode === 'root_posts';
    const showThreads = autoReplyLicensed || selectedMode === 'threads' || draft.saved.mode === 'threads';

    const selectMode = (mode: ChannelAutoReplyMode) => {
        if (mode !== 'off' && !autoReplyLicensed) {
            return;
        }
        setLocalMode(mode);
        setChannelAutoReplySaveError(null);
        informChange('mode', mode);
    };

    return (
        <Container>
            <Label>
                <FormattedMessage defaultMessage='Auto-reply mode'/>
            </Label>
            <HelpText>
                <FormattedMessage defaultMessage='An automatic reply behaves exactly as if the author had @-mentioned the agent.'/>
            </HelpText>
            <Options>
                <OptionRow>
                    <Radio
                        type='radio'
                        name='autoreply-mode'
                        checked={selectedMode === 'off'}
                        onChange={() => selectMode('off')}
                    />
                    <OptionCopy>
                        <OptionTitle>
                            <FormattedMessage defaultMessage='Off'/>
                        </OptionTitle>
                        <OptionHelp>
                            <FormattedMessage defaultMessage='The agent replies only when @-mentioned.'/>
                        </OptionHelp>
                    </OptionCopy>
                </OptionRow>
                {showRootPosts && (
                    <OptionRow>
                        <Radio
                            type='radio'
                            name='autoreply-mode'
                            checked={selectedMode === 'root_posts'}
                            disabled={!autoReplyLicensed && selectedMode !== 'root_posts'}
                            onChange={() => selectMode('root_posts')}
                        />
                        <OptionCopy>
                            <OptionTitle>
                                <FormattedMessage defaultMessage='Top-level posts only'/>
                            </OptionTitle>
                            <OptionHelp>
                                <FormattedMessage defaultMessage='The agent automatically replies to new top-level posts, starting a thread.'/>
                            </OptionHelp>
                        </OptionCopy>
                    </OptionRow>
                )}
                {showThreads && (
                    <OptionRow>
                        <Radio
                            type='radio'
                            name='autoreply-mode'
                            checked={selectedMode === 'threads'}
                            disabled={!autoReplyLicensed && selectedMode !== 'threads'}
                            onChange={() => selectMode('threads')}
                        />
                        <OptionCopy>
                            <OptionTitle>
                                <FormattedMessage defaultMessage='Threads too'/>
                            </OptionTitle>
                            <OptionHelp>
                                <FormattedMessage defaultMessage='The agent also automatically replies to replies in threads.'/>
                            </OptionHelp>
                        </OptionCopy>
                    </OptionRow>
                )}
            </Options>
        </Container>
    );
};

const Container = styled.div`
    display: flex;
    flex-direction: column;
    gap: 8px;
`;

const Label = styled.div`
    font-weight: 600;
    font-size: 14px;
`;

const HelpText = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 12px;
`;

const Options = styled.div`
    display: flex;
    flex-direction: column;
    gap: 8px;
`;

const OptionRow = styled.label`
    display: grid;
    grid-template-columns: auto 1fr;
    grid-column-gap: 8px;
    align-items: start;
`;

const Radio = styled.input`
    margin-top: 3px;
`;

const OptionCopy = styled.div`
    display: flex;
    flex-direction: column;
    gap: 2px;
`;

const OptionTitle = styled.div`
    font-size: 14px;
    line-height: 20px;
`;

const OptionHelp = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 12px;
`;

export default AutoReplyModePicker;
