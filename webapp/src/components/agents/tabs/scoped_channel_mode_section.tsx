// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

import {Checkbox} from '@mattermost/compass-ui/components/checkbox';
import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';

import {SelectChannel} from '@/components/select';
import {useServerSupportsScopedSessions} from '@/hooks/use_server_supports_scoped_sessions';

type Props = {
    scopedChannelMode: boolean;
    scopedExtraChannelIds: string[];
    autoEnableNewMCPTools: boolean;

    /** The allowlist grants every tool on some server instead of naming them. */
    hasAllToolsGrants: boolean;

    /** Only system admins may change these settings. */
    editable: boolean;
    onChange: (updates: {scopedChannelMode?: boolean; scopedExtraChannelIds?: string[]}) => void;
};

const ScopedChannelModeSection = ({scopedChannelMode, scopedExtraChannelIds, autoEnableNewMCPTools, hasAllToolsGrants, editable, onChange}: Props) => {
    const serverSupported = useServerSupportsScopedSessions();
    const canTurnOn = editable && serverSupported && !autoEnableNewMCPTools && !hasAllToolsGrants;

    return (
        <Section data-testid='scoped-channel-mode-section'>
            <Checkbox
                id='mcp-scoped-channel-mode'
                checked={scopedChannelMode}
                disabled={scopedChannelMode ? !editable : !canTurnOn}
                onChange={(e) => onChange({scopedChannelMode: e.target.checked})}
            >
                <CheckboxText>
                    <CheckboxTitle>
                        <FormattedMessage defaultMessage='Run tools in channels without asking'/>
                    </CheckboxTitle>
                    <CheckboxHint>
                        <FormattedMessage defaultMessage="In channels, this agent's tools run without asking and their results are shared in the thread without a Share step. Mattermost tools read through a short-lived session for the agent's own account; tools that change data in Mattermost and tools from other plugins are not used. External MCP tools run with the service account and can do whatever the tools you pick allow there. Web search and URL fetch also run without asking when they are turned on for this agent. Direct messages are not affected."/>
                    </CheckboxHint>
                </CheckboxText>
            </Checkbox>
            {!serverSupported && (
                <SectionNotice
                    type='info'
                    title={<FormattedMessage defaultMessage='This setting needs Mattermost Server 12.2 or later.'/>}
                />
            )}
            {serverSupported && autoEnableNewMCPTools && !scopedChannelMode && (
                <SectionNotice
                    type='info'
                    title={<FormattedMessage defaultMessage='To use this setting, turn off "Automatically enable all MCP tools" and pick the tools this agent may use.'/>}
                />
            )}
            {serverSupported && !autoEnableNewMCPTools && hasAllToolsGrants && !scopedChannelMode && (
                <SectionNotice
                    type='info'
                    title={<FormattedMessage defaultMessage='To use this setting, replace the servers granted with all of their tools by the specific tools this agent may use.'/>}
                />
            )}
            {scopedChannelMode && hasAllToolsGrants && (
                <SectionNotice
                    type='warning'
                    title={<FormattedMessage defaultMessage='Pick each tool by name. While this setting is on, a server cannot be granted with all of its tools, and the agent cannot be saved until those grants are replaced.'/>}
                />
            )}
            {scopedChannelMode && (
                <>
                    <SectionNotice
                        type='info'
                        title={<FormattedMessage defaultMessage="Without asking, this agent can read the channel it's used in, all public channels on teams anyone can join, and the channels below, and run the external MCP tools picked below. Channel tool calling must also be turned on in the plugin settings."/>}
                    />
                    <ExtraChannels>
                        <ExtraChannelsLabel>
                            <FormattedMessage defaultMessage='Additional channels this agent can read'/>
                        </ExtraChannelsLabel>
                        <SelectChannel
                            channelIDs={scopedExtraChannelIds}
                            onChangeChannelIDs={(ids) => onChange({scopedExtraChannelIds: ids})}
                            disabled={!editable}
                        />
                    </ExtraChannels>
                    {scopedExtraChannelIds.length > 0 && (
                        <div role='status'>
                            <SectionNotice
                                type='warning'
                                title={<FormattedMessage defaultMessage='Content from these channels may appear in any channel where this agent is used, including to people who are not members of them.'/>}
                            />
                        </div>
                    )}
                </>
            )}
        </Section>
    );
};

const Section = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-m);
    padding-top: var(--spacing-m);
    border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
`;

const CheckboxText = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xxxs);
`;

const CheckboxTitle = styled.span`
    font-weight: 600;
`;

const CheckboxHint = styled.span`
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 12px;
`;

const ExtraChannels = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xs);
`;

const ExtraChannelsLabel = styled.span`
    font-weight: 600;
`;

export default ScopedChannelModeSection;
