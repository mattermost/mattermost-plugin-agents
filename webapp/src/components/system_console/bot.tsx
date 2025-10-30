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
import {LLMService} from './service';

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
    serviceID: string
    customInstructions: string
    enableVision: boolean
    disableTools: boolean
    channelAccessLevel: ChannelAccessLevel
    channelIDs: string[]
    userAccessLevel: UserAccessLevel
    userIDs: string[]
    teamIDs: string[]
    enabledNativeTools?: string[]
}

// Component for configuring native tools (OpenAI/Anthropic)
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

type Props = {
    bot: LLMBotConfig
    services: LLMService[]
    onChange: (bot: LLMBotConfig) => void
    onDelete: () => void
    changedAvatar: (image: File) => void
}

const Bot = (props: Props) => {
    const [open, setOpen] = useState(false);
    const intl = useIntl();

    const missingUsername = !props.bot.name || props.bot.name.trim() === '';
    const invalidUsername = props.bot.name !== '' && (!(/^[a-z0-9.\-_]+$/).test(props.bot.name) || !(/[a-z]/).test(props.bot.name.charAt(0)));
    const missingService = !props.bot.serviceID || !props.services.find((s) => s.id === props.bot.serviceID);

    return (
        <BotContainer>
            <HeaderContainer onClick={() => setOpen((o) => !o)}>
                <IconAI/>
                <Title>
                    <NameText>
                        {props.bot.displayName}
                    </NameText>
                </Title>
                <Spacer/>
                {missingService && (
                    <DangerPill>
                        <AlertOutlineIcon/>
                        <FormattedMessage defaultMessage='No Service Selected'/>
                    </DangerPill>
                )}
                {missingUsername && (
                    <DangerPill>
                        <AlertOutlineIcon/>
                        <FormattedMessage defaultMessage='No Username'/>
                    </DangerPill>
                )}
                {invalidUsername && (
                    <DangerPill>
                        <AlertOutlineIcon/>
                        <FormattedMessage defaultMessage='Invalid Username'/>
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
                            label={intl.formatMessage({defaultMessage: 'AI Service'})}
                            value={props.bot.serviceID}
                            onChange={(e) => props.onChange({...props.bot, serviceID: e.target.value})}
                        >
                            <SelectionItemOption value=''>
                                {intl.formatMessage({defaultMessage: 'Select a service'})}
                            </SelectionItemOption>
                            {props.services.map((service) => (
                                <SelectionItemOption
                                    key={service.id}
                                    value={service.id}
                                >
                                    {service.name || service.type}
                                </SelectionItemOption>
                            ))}
                        </SelectionItem>
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'Custom instructions'})}
                            placeholder={intl.formatMessage({defaultMessage: 'How would you like the AI to respond?'})}
                            multiline={true}
                            value={props.bot.customInstructions}
                            onChange={(e) => props.onChange({...props.bot, customInstructions: e.target.value})}
                        />
                        {(() => {
                            const selectedService = props.services.find((s) => s.id === props.bot.serviceID);
                            const supportsVisionAndTools = selectedService &&
                                ['openai', 'openaicompatible', 'azure', 'anthropic', 'cohere'].includes(selectedService.type);

                            if (!supportsVisionAndTools) {
                                return null;
                            }

                            return (
                                <>
                                    <BooleanItem
                                        label={intl.formatMessage({defaultMessage: 'Enable Vision'})}
                                        value={props.bot.enableVision}
                                        onChange={(to: boolean) => props.onChange({...props.bot, enableVision: to})}
                                        helpText={intl.formatMessage({defaultMessage: 'Enable Vision to allow the bot to process images. Requires a compatible model.'})}
                                    />
                                    <BooleanItem
                                        label={intl.formatMessage({defaultMessage: 'Enable Tools'})}
                                        value={!props.bot.disableTools}
                                        onChange={(to: boolean) => props.onChange({...props.bot, disableTools: !to})}
                                        helpText={intl.formatMessage({defaultMessage: 'By default some tool use is enabled to allow for features such as integrations with JIRA. Disabling this allows use of models that do not support or are not very good at tool use. Some features will not work without tools.'})}
                                    />
                                    {(() => {
                                        // Show native tools for Anthropic or OpenAI-based services with ResponsesAPI enabled
                                        const isAnthropic = selectedService.type === 'anthropic';
                                        const isOpenAIWithResponses = ['openai', 'openaicompatible', 'azure'].includes(selectedService.type) && selectedService.useResponsesAPI;

                                        if (isAnthropic) {
                                            return (
                                                <NativeToolsItem
                                                    enabledTools={props.bot.enabledNativeTools || []}
                                                    onChange={(tools: string[]) => props.onChange({...props.bot, enabledNativeTools: tools})}
                                                    provider='anthropic'
                                                />
                                            );
                                        }

                                        if (isOpenAIWithResponses) {
                                            return (
                                                <NativeToolsItem
                                                    enabledTools={props.bot.enabledNativeTools || []}
                                                    onChange={(tools: string[]) => props.onChange({...props.bot, enabledNativeTools: tools})}
                                                    provider='openai'
                                                />
                                            );
                                        }

                                        return null;
                                    })()}
                                </>
                            );
                        })()}
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

const Spacer = styled.div`
	flex-grow: 1;
`;

const TrashIcon = styled(TrashCanOutlineIcon)`
	width: 16px;
	height: 16px;
	color: #D24B4E;
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

const Horizontal = styled.div`
	display: flex;
	flex-direction: row;
	align-items: center;
	gap: 8px;
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
