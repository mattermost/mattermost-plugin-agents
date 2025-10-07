// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {TrashCanOutlineIcon, ChevronDownIcon, AlertOutlineIcon, ChevronUpIcon} from '@mattermost/compass-icons/components';

import IconAI from '../assets/icon_ai';
import {DangerPill, Pill} from '../pill';

import {ButtonIcon} from '../assets/buttons';

import {BooleanItem, ItemList, SelectionItem, SelectionItemOption, TextItem, ItemLabel, HelpText} from './item';
import AvatarItem from './avatar';
import {ChannelAccessLevelItem, UserAccessLevelItem} from './llm_access';

export type LLMService = {
    type: string
    apiURL: string
    apiKey: string
    orgId: string
    defaultModel: string
    tokenLimit: number
    streamingTimeoutSeconds: number
    sendUserId: boolean
    outputTokenLimit: number
    useResponsesAPI: boolean
    enabledNativeTools?: string[]
}

export enum ChannelAccessLevel {
    All = 0,
    Allow,
    Block,
    None,
}

export enum UserAccessLevel {
    All = 0,
    Allow,
    Block,
    None,
}

export type LLMBotConfig = {
    id: string
    name: string
    displayName: string
    service: LLMService
    customInstructions: string
    enableVision: boolean
    disableTools: boolean
    channelAccessLevel: ChannelAccessLevel
    channelIDs: string[]
    userAccessLevel: UserAccessLevel
    userIDs: string[]
    teamIDs: string[]
}

type Props = {
    bot: LLMBotConfig
    onChange: (bot: LLMBotConfig) => void
    onDelete: () => void
    changedAvatar: (image: File) => void
}

const mapServiceTypeToDisplayName = new Map<string, string>([
    ['openai', 'OpenAI'],
    ['openaicompatible', 'OpenAI Compatible'],
    ['azure', 'Azure'],
    ['anthropic', 'Anthropic'],
    ['cohere', 'Cohere'],
    ['asage', 'asksage (Experimental)'],
]);

function serviceTypeToDisplayName(serviceType: string): string {
    return mapServiceTypeToDisplayName.get(serviceType) || serviceType;
}

const Bot = (props: Props) => {
    const [open, setOpen] = useState(false);
    const intl = useIntl();
    const missingInfo = props.bot.name === '' ||
		props.bot.displayName === '' ||
		props.bot.service.type === '' ||
		(props.bot.service.type !== 'openaicompatible' && props.bot.service.type !== 'azure' && props.bot.service.type !== 'asage' && props.bot.service.apiKey === '') ||
		((props.bot.service.type === 'openaicompatible' || props.bot.service.type === 'azure') && props.bot.service.apiURL === '');

    const invalidUsername = props.bot.name !== '' && (!(/^[a-z0-9.\-_]+$/).test(props.bot.name) || !(/[a-z]/).test(props.bot.name.charAt(0)));
    const invalidMaxTokens = props.bot.service.type === 'anthropic' && props.bot.service?.outputTokenLimit === 0;
    return (
        <BotContainer>
            <HeaderContainer onClick={() => setOpen((o) => !o)}>
                <IconAI/>
                <Title>
                    <NameText>
                        {props.bot.displayName}
                    </NameText>
                    <VerticalDivider/>
                    <ServiceTypeText>
                        {serviceTypeToDisplayName(props.bot.service.type)}
                    </ServiceTypeText>
                </Title>
                <Spacer/>
                {missingInfo && (
                    <DangerPill>
                        <AlertOutlineIcon/>
                        <FormattedMessage defaultMessage='Missing information'/>
                    </DangerPill>
                )}
                {invalidUsername && (
                    <DangerPill>
                        <AlertOutlineIcon/>
                        <FormattedMessage defaultMessage='Invalid Username'/>
                    </DangerPill>
                )}
                {invalidMaxTokens && (
                    <DangerPill>
                        <AlertOutlineIcon/>
                        <FormattedMessage defaultMessage='Output token limit must be greater than 0'/>
                    </DangerPill>
                )}

                <ButtonIcon
                    onClick={props.onDelete}
                >
                    <TrashIcon/>
                </ButtonIcon>
                {open ? <ChevronUpIcon/> : <ChevronDownIcon/>}
            </HeaderContainer>
            {open && (
                <ItemListContainer>
                    <ItemList>
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'Display name'})}
                            value={props.bot.displayName}
                            onChange={(e) => props.onChange({...props.bot, displayName: e.target.value})}
                        />
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'Bot Username'})}
                            helptext={intl.formatMessage({defaultMessage: 'Team members can mention this bot with this username'})}
                            maxLength={22}
                            value={props.bot.name}
                            onChange={(e) => props.onChange({...props.bot, name: e.target.value})}
                        />
                        <AvatarItem
                            botusername={props.bot.name}
                            changedAvatar={props.changedAvatar}
                        />
                        <SelectionItem
                            label={intl.formatMessage({defaultMessage: 'Service'})}
                            value={props.bot.service.type}
                            onChange={(e) => props.onChange({...props.bot, service: {...props.bot.service, type: e.target.value}})}
                        >
                            <SelectionItemOption value='openai'>{'OpenAI'}</SelectionItemOption>
                            <SelectionItemOption value='openaicompatible'>{'OpenAI Compatible'}</SelectionItemOption>
                            <SelectionItemOption value='azure'>{'Azure'}</SelectionItemOption>
                            <SelectionItemOption value='anthropic'>{'Anthropic'}</SelectionItemOption>
                            <SelectionItemOption value='cohere'>{'Cohere'}</SelectionItemOption>
                            <SelectionItemOption value='asage'>{'asage (Experimental)'}</SelectionItemOption>
                        </SelectionItem>
                        <ServiceItem
                            service={props.bot.service}
                            onChange={(service) => props.onChange({...props.bot, service})}
                        />
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'Custom instructions'})}
                            placeholder={intl.formatMessage({defaultMessage: 'How would you like the AI to respond?'})}
                            multiline={true}
                            value={props.bot.customInstructions}
                            onChange={(e) => props.onChange({...props.bot, customInstructions: e.target.value})}
                        />
                        {(props.bot.service.type === 'openai' || props.bot.service.type === 'openaicompatible' || props.bot.service.type === 'azure' || props.bot.service.type === 'anthropic' || props.bot.service.type === 'cohere') && (
                            <>
                                <BooleanItem
                                    label={
                                        <Horizontal>
                                            <FormattedMessage defaultMessage='Enable Vision'/>
                                            <Pill><FormattedMessage defaultMessage='BETA'/></Pill>
                                        </Horizontal>
                                    }
                                    value={props.bot.enableVision}
                                    onChange={(to: boolean) => props.onChange({...props.bot, enableVision: to})}
                                    helpText={intl.formatMessage({defaultMessage: 'Enable Vision to allow the bot to process images. Requires a compatible model.'})}
                                />
                                <BooleanItem
                                    label={
                                        <FormattedMessage defaultMessage='Enable Tools'/>
                                    }
                                    value={!props.bot.disableTools}
                                    onChange={(to: boolean) => props.onChange({...props.bot, disableTools: !to})}
                                    helpText={intl.formatMessage({defaultMessage: 'By default some tool use is enabled to allow for features such as integrations with JIRA. Disabling this allows use of models that do not support or are not very good at tool use. Some features will not work without tools.'})}
                                />
                            </>
                        )}
                        <ChannelAccessLevelItem
                            label={intl.formatMessage({defaultMessage: 'Channel access'})}
                            level={props.bot.channelAccessLevel ?? ChannelAccessLevel.All}
                            onChangeLevel={(to: ChannelAccessLevel) => props.onChange({...props.bot, channelAccessLevel: to})}
                            channelIDs={props.bot.channelIDs ?? []}
                            onChangeChannelIDs={(channels: string[]) => props.onChange({...props.bot, channelIDs: channels})}
                        />
                        <UserAccessLevelItem
                            label={intl.formatMessage({defaultMessage: 'User access'})}
                            level={props.bot.userAccessLevel ?? ChannelAccessLevel.All}
                            onChangeLevel={(to: UserAccessLevel) => props.onChange({...props.bot, userAccessLevel: to})}
                            userIDs={props.bot.userIDs ?? []}
                            teamIDs={props.bot.teamIDs ?? []}
                            onChangeIDs={(userIds: string[], teamIds: string[]) => props.onChange({...props.bot, userIDs: userIds, teamIDs: teamIds})}
                        />

                    </ItemList>
                </ItemListContainer>
            )}
        </BotContainer>
    );
};

const Horizontal = styled.div`
	display: flex;
	flex-direction: row;
	align-items: center;
	gap: 8px;
`;

// Component for configuring native OpenAI tools
type NativeToolsItemProps = {
    enabledTools: string[]
    onChange: (tools: string[]) => void
    provider?: 'openai' | 'anthropic'
}

const NativeToolsItem = (props: NativeToolsItemProps) => {
    const intl = useIntl();
    const provider = props.provider || 'openai';

    const availableNativeTools = [
        {
            id: 'web_search',
            label: intl.formatMessage({defaultMessage: 'Web Search'}),
            helpText: provider === 'anthropic' ?
                intl.formatMessage({defaultMessage: 'Enable Claude\'s built-in web search capability'}) :
                intl.formatMessage({defaultMessage: 'Enable OpenAI\'s built-in web search capability'}),
        },

    ];

    const toggleTool = (toolId: string) => {
        const currentTools = props.enabledTools || [];
        if (currentTools.includes(toolId)) {
            props.onChange(currentTools.filter((t) => t !== toolId));
        } else {
            props.onChange([...currentTools, toolId]);
        }
    };

    const titleMessage = provider === 'anthropic' ?
        intl.formatMessage({defaultMessage: 'Native Claude Tools'}) :
        intl.formatMessage({defaultMessage: 'Native OpenAI Tools'});

    return (
        <>
            <ItemLabel>
                <Horizontal>
                    {titleMessage}
                    <Pill><FormattedMessage defaultMessage='EXPERIMENTAL'/></Pill>
                </Horizontal>
            </ItemLabel>
            <div>
                {availableNativeTools.map((tool) => (
                    <NativeToolContainer key={tool.id}>
                        <StyledCheckbox
                            type='checkbox'
                            checked={props.enabledTools.includes(tool.id)}
                            onChange={() => toggleTool(tool.id)}
                        />
                        <NativeToolLabel>
                            <div>{tool.label}</div>
                            <HelpText>{tool.helpText}</HelpText>
                        </NativeToolLabel>
                    </NativeToolContainer>
                ))}
            </div>
        </>
    );
};

type ServiceItemProps = {
    service: LLMService
    onChange: (service: LLMService) => void
}

const ServiceItem = (props: ServiceItemProps) => {
    const type = props.service.type;
    const intl = useIntl();
    const isOpenAIType = type === 'openai' || type === 'openaicompatible' || type === 'azure' || type === 'cohere';
    const isCohere = type === 'cohere';

    const getDefaultOutputTokenLimit = () => {
        switch (type) {
        case 'anthropic':
            return '8192';
        default:
            return '0';
        }
    };

    return (
        <>
            {(type === 'openaicompatible' || type === 'azure' || type === 'asage') && (
                <TextItem
                    label={intl.formatMessage({defaultMessage: 'API URL'})}
                    value={props.service.apiURL}
                    onChange={(e) => props.onChange({...props.service, apiURL: e.target.value})}
                />
            )}
            <TextItem
                label={intl.formatMessage({defaultMessage: 'API Key'})}
                type='password'
                value={props.service.apiKey}
                onChange={(e) => props.onChange({...props.service, apiKey: e.target.value})}
            />
            {isOpenAIType && (
                <>
                    {!isCohere && (
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'Organization ID'})}
                            value={props.service.orgId}
                            onChange={(e) => props.onChange({...props.service, orgId: e.target.value})}
                        />
                    )}
                    <BooleanItem
                        label={intl.formatMessage({defaultMessage: 'Send User ID'})}
                        value={props.service.sendUserId}
                        onChange={(to: boolean) => props.onChange({...props.service, sendUserId: to})}
                        helpText={intl.formatMessage({defaultMessage: 'Sends the Mattermost user ID to the upstream LLM.'})}
                    />
                    {(type === 'openai' || type === 'openaicompatible' || type === 'azure') && (
                        <>
                            <BooleanItem
                                label={intl.formatMessage({defaultMessage: 'Use Responses API'})}
                                value={props.service.useResponsesAPI ?? false}
                                onChange={(to: boolean) => props.onChange({...props.service, useResponsesAPI: to})}
                                helpText={intl.formatMessage({defaultMessage: 'Use the new OpenAI Responses API with support for reasoning summaries and other advanced features. Disable for legacy Completions API compatibility.'})}
                            />
                            {props.service.useResponsesAPI && (
                                <NativeToolsItem
                                    enabledTools={props.service.enabledNativeTools || []}
                                    onChange={(tools: string[]) => props.onChange({...props.service, enabledNativeTools: tools})}
                                    provider='openai'
                                />
                            )}
                        </>
                    )}
                </>
            )}
            {type === 'anthropic' && (
                <NativeToolsItem
                    enabledTools={props.service.enabledNativeTools || []}
                    onChange={(tools: string[]) => props.onChange({...props.service, enabledNativeTools: tools})}
                    provider='anthropic'
                />
            )}
            <TextItem
                label={intl.formatMessage({defaultMessage: 'Default model'})}
                value={props.service.defaultModel}
                onChange={(e) => props.onChange({...props.service, defaultModel: e.target.value})}
            />
            <TextItem
                label={intl.formatMessage({defaultMessage: 'Input token limit'})}
                type='number'
                value={props.service.tokenLimit.toString()}
                onChange={(e) => {
                    const value = parseInt(e.target.value, 10);
                    const tokenLimit = isNaN(value) ? 0 : value;
                    props.onChange({...props.service, tokenLimit});
                }}
            />
            <TextItem
                label={intl.formatMessage({defaultMessage: 'Output token limit'})}
                type='number'
                value={props.service.outputTokenLimit?.toString() || getDefaultOutputTokenLimit()}
                onChange={(e) => {
                    const value = parseInt(e.target.value, 10);
                    const outputTokenLimit = isNaN(value) ? 0 : value;
                    props.onChange({...props.service, outputTokenLimit});
                }}
            />
            {isOpenAIType && (
                <TextItem
                    label={intl.formatMessage({defaultMessage: 'Streaming Timeout Seconds'})}
                    type='number'
                    value={props.service.streamingTimeoutSeconds?.toString() || '0'}
                    onChange={(e) => {
                        const value = parseInt(e.target.value, 10);
                        const streamingTimeoutSeconds = isNaN(value) ? 0 : value;
                        props.onChange({...props.service, streamingTimeoutSeconds});
                    }}
                />
            )}
        </>
    );
};

const ItemListContainer = styled.div`
	padding: 24px 20px;
	padding-right: 76px;
`;

const Title = styled.div`
	display: flex;
	flex-direction: row;
	align-items: center;
	gap: 8px;
`;

const NameText = styled.div`
	font-size: 14px;
	font-weight: 600;
`;

const ServiceTypeText = styled.div`
	font-size: 14px;
	font-weight: 400;
	color: rgba(var(--center-channel-color-rgb), 0.72);
`;

const Spacer = styled.div`
	flex-grow: 1;
`;

const TrashIcon = styled(TrashCanOutlineIcon)`
	width: 16px;
	height: 16px;
	color: #D24B4E;
`;

const VerticalDivider = styled.div`
	width: 1px;
	border-left: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
	height: 24px;
`;

const BotContainer = styled.div`
	display: flex;
	flex-direction: column;

	border-radius: 4px;
	border: 1px solid rgba(var(--center-channel-color-rgb), 0.12);

	&:hover {
		box-shadow: 0px 2px 3px 0px rgba(0, 0, 0, 0.08);
	}
`;

const HeaderContainer = styled.div`
	display: flex;
	flex-direction: row;
	justify-content: space-between;
	align-items: center;
	gap: 16px;
	padding: 12px 16px 12px 20px;
	border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
	cursor: pointer;
`;

const NativeToolContainer = styled.div`
	display: flex;
	flex-direction: row;
	align-items: flex-start;
	gap: 8px;
	margin-bottom: 12px;
`;

const NativeToolLabel = styled.label`
	display: flex;
	flex-direction: column;
	gap: 4px;
	cursor: pointer;

	div:first-child {
		font-size: 14px;
		font-weight: 400;
		line-height: 20px;
	}
`;

const StyledCheckbox = styled.input`
	margin-top: 2px;
	cursor: pointer;
`;

export default Bot;
