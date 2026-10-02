// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useRef, useEffect} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {ChevronDownIcon, SendIcon} from '@mattermost/compass-icons/components';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {MenuItem} from '@mattermost/compass-ui/components/menu-item';
import {PopoverMenu, PopoverMenuDivider} from '@mattermost/compass-ui/components/popover-menu';
import {TextInput} from '@mattermost/compass-ui/components/text-input';

import {LLMBot} from '@/bots';

import {BotDropdown, BotSelectorContainer} from './bot_selector';
import IconAI from './assets/icon_ai';
import {GrayPill} from './pill';
import {SummarizeDateRangeModal} from './summarize_date_range_modal';

const PopoverContainer = styled(PopoverMenu)`
    && {
        width: 328px;
    }
`;

const InputContainer = styled.div`
    padding: 4px 12px;
    width: 100%;
    box-sizing: border-box;
`;

const BotSelectorWrapper = styled.div`
    width: 100%;
    padding: 6px 20px 6px 4px;
    box-sizing: border-box;
`;

const StyledBotSelectorContainer = styled(BotSelectorContainer)`
    margin: 0 16px;
    width: 100%;
`;

const SelectMessage = styled.div`
    font-size: 12px;
    font-weight: 600;
    line-height: 16px;
    letter-spacing: 0.24px;
    text-transform: uppercase;
`;

const BotPill = styled(GrayPill)`
    font-size: 11px;
    padding: 2px 6px;
    gap: 0;
    color: var(--center-channel-color);
    font-weight: 600;
    max-width: 128px;

    svg {
        flex-shrink: 0;
    }
`;

const BotPillName = styled.span`
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    min-width: 0;
`;

interface Props {
    bots: LLMBot[];
    activeBot: LLMBot | null;
    setActiveBot: (bot: LLMBot) => void;
    channelName: string;
    onSummarize: (options: any) => void;
    lastViewedAt: number;
}

export const ChannelSummarizePopover = ({bots, activeBot, setActiveBot, channelName, onSummarize, lastViewedAt}: Props) => {
    const intl = useIntl();
    const [inputValue, setInputValue] = useState('');
    const inputRef = useRef<HTMLInputElement>(null);
    const [showDateModal, setShowDateModal] = useState(false);

    useEffect(() => {
        if (inputRef.current) {
            inputRef.current.focus();
        }
    }, []);

    const handleDateRangeSelect = () => {
        setShowDateModal(true);
    };

    const handleSummarizeDateRange = (startDate: string, endDate: string) => {
        onSummarize({
            analysis_type: 'date_range',
            since: startDate,
            until: endDate,
        });
    };

    const handleSummarizeUnreads = () => {
        onSummarize({
            analysis_type: 'summarize_unreads',
            since: new Date(lastViewedAt).toISOString(),
        });
    };

    const handleSummarizeDays = (days: number) => {
        onSummarize({
            analysis_type: 'days',
            days,
        });
    };

    const handleInputSubmit = () => {
        if (inputValue.trim()) {
            onSummarize({
                analysis_type: 'custom',
                prompt: inputValue,
            });
        }
    };

    const handleKeyDown = (e: React.KeyboardEvent) => {
        if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault();
            handleInputSubmit();
        }
    };

    return (
        <>
            <PopoverContainer>
                <InputContainer>
                    <TextInput
                        ref={inputRef}
                        type='text'
                        placeholder={intl.formatMessage({defaultMessage: 'Ask Agents about this channel...'})}
                        value={inputValue}
                        onChange={(e) => setInputValue(e.target.value)}
                        onKeyDown={handleKeyDown}
                        leadingIcon={<Icon glyph={<IconAI/>}/>}
                        trailingIcon={(
                            <IconButton
                                data-testid='send-custom-prompt-button'
                                size='x-small'
                                icon={<Icon glyph={<SendIcon/>}/>}
                                aria-label={intl.formatMessage({defaultMessage: 'Send'})}
                                disabled={inputValue.length === 0}
                                onClick={handleInputSubmit}
                            />
                        )}
                    />
                </InputContainer>
                <PopoverMenuDivider/>
                <MenuItem
                    label={<FormattedMessage defaultMessage='Summarize unreads'/>}
                    leadingElement={false}
                    onClick={handleSummarizeUnreads}
                />
                <MenuItem
                    label={<FormattedMessage defaultMessage='Summarize last 7 days'/>}
                    leadingElement={false}
                    onClick={() => handleSummarizeDays(7)}
                />
                <MenuItem
                    label={<FormattedMessage defaultMessage='Summarize last 14 days'/>}
                    leadingElement={false}
                    onClick={() => handleSummarizeDays(14)}
                />
                <MenuItem
                    label={<FormattedMessage defaultMessage='Select date range to summarize'/>}
                    leadingElement={false}
                    onClick={handleDateRangeSelect}
                />
                <PopoverMenuDivider/>
                <BotSelectorWrapper>
                    <BotDropdown
                        bots={bots}
                        activeBot={activeBot}
                        setActiveBot={setActiveBot}
                        container={StyledBotSelectorContainer}
                    >
                        <>
                            <SelectMessage>
                                <FormattedMessage defaultMessage='GENERATE WITH:'/>
                            </SelectMessage>
                            <BotPill>
                                <BotPillName>{activeBot?.displayName}</BotPillName>
                                <ChevronDownIcon size={12}/>
                            </BotPill>
                        </>
                    </BotDropdown>
                </BotSelectorWrapper>
            </PopoverContainer>
            <SummarizeDateRangeModal
                show={showDateModal}
                onClose={() => setShowDateModal(false)}
                onSummarize={handleSummarizeDateRange}
                channelName={channelName}
            />
        </>
    );
};
