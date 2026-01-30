// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useRef, useEffect} from 'react';
import styled, {css} from 'styled-components';
import {FormattedMessage} from 'react-intl';

import {ChevronDownIcon, SendIcon} from '@mattermost/compass-icons/components';

import {LLMBot} from '@/bots';

import {BotDropdown, BotSelectorContainer} from './bot_selector';
import IconAI from './assets/icon_ai';
import {GrayPill} from './pill';
import {SummarizeDateRangeModal} from './summarize_date_range_modal';

const PopoverContainer = styled.div`
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    padding: 8px 0;
    background: var(--center-channel-bg);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    width: 328px;
    box-shadow: 0px 8px 24px rgba(0, 0, 0, 0.12);
`;

const InputContainer = styled.div`
    padding: 4px 12px;
    width: 100%;
    box-sizing: border-box;
`;

interface AIInputProps {
    isFocused: boolean;
    hasValue: boolean;
}

const AIInputWrapper = styled.div<AIInputProps>`
    display: flex;
    align-items: center;
    padding: 10px 12px;
    gap: 8px;
    background: var(--center-channel-bg);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    width: 100%;
    box-sizing: border-box;
    cursor: text;
    position: relative;

    ${({isFocused, hasValue}) => (isFocused || hasValue) && css`
        border: 2px solid var(--button-bg);
        padding: 9px 11px; /* Adjust padding to account for border width change */
    `}

    &:hover {
        border-color: ${({isFocused, hasValue}) => ((isFocused || hasValue) ? 'var(--button-bg)' : 'rgba(var(--center-channel-color-rgb), 0.24)')};
    }
`;

const StyledInput = styled.input`
    font-family: 'Open Sans', sans-serif;
    font-size: 14px;
    line-height: 20px;
    color: var(--center-channel-color);
    border: none;
    background: transparent;
    width: 100%;
    padding: 0;
    outline: none;

    &::placeholder {
        color: rgba(var(--center-channel-color-rgb), 0.64);
    }
`;

const IconWrapper = styled.div`
    display: flex;
    align-items: center;
    justify-content: center;
    width: 16px;
    height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    flex-shrink: 0;
`;

const TrailingIconWrapper = styled.div<{isActive: boolean}>`
    display: flex;
    align-items: center;
    justify-content: center;
    width: 16px;
    height: 16px;
    color: ${({isActive}) => (isActive ? 'var(--button-bg)' : 'rgba(var(--center-channel-color-rgb), 0.32)')};
    flex-shrink: 0;
    cursor: ${({isActive}) => (isActive ? 'pointer' : 'default')};
`;

const Divider = styled.div`
    height: 1px;
    width: 100%;
    background: rgba(var(--center-channel-color-rgb), 0.08);
    margin: 8px 0;
`;

const MenuList = styled.div`
    display: flex;
    flex-direction: column;
    width: 100%;
    padding-top: 4px;
`;

const MenuItem = styled.div`
    display: flex;
    align-items: center;
    padding: 6px 20px;
    width: 100%;
    cursor: pointer;
    box-sizing: border-box;

    font-family: 'Open Sans', sans-serif;
    font-size: 14px;
    line-height: 20px;
    color: var(--center-channel-color);

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.08);
    }
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
    const [inputValue, setInputValue] = useState('');
    const [isFocused, setIsFocused] = useState(false);
    const inputRef = useRef<HTMLInputElement>(null);
    const [showDateModal, setShowDateModal] = useState(false);

    useEffect(() => {
        if (inputRef.current) {
            inputRef.current.focus();
        }
    }, []);

    const handleInputClick = () => {
        if (inputRef.current) {
            inputRef.current.focus();
        }
    };

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
                    <AIInputWrapper
                        isFocused={isFocused}
                        hasValue={inputValue.length > 0}
                        onClick={handleInputClick}
                    >
                        <IconWrapper>
                            <IconAI/>
                        </IconWrapper>
                        <FormattedMessage defaultMessage='Ask Agents about this channel...'>
                            {(placeholder) => (
                                <StyledInput
                                    ref={inputRef}
                                    type='text'
                                    placeholder={placeholder as unknown as string}
                                    value={inputValue}
                                    onChange={(e) => setInputValue(e.target.value)}
                                    onFocus={() => setIsFocused(true)}
                                    onBlur={() => setIsFocused(false)}
                                    onKeyDown={handleKeyDown}
                                />
                            )}
                        </FormattedMessage>
                        <TrailingIconWrapper
                            data-testid='send-custom-prompt-button'
                            isActive={inputValue.length > 0}
                            onClick={(e) => {
                                e.stopPropagation();
                                handleInputSubmit();
                            }}
                        >
                            <SendIcon size={16}/>
                        </TrailingIconWrapper>
                    </AIInputWrapper>
                </InputContainer>
                <Divider/>
                <MenuList>
                    <MenuItem onClick={handleSummarizeUnreads}>
                        <FormattedMessage defaultMessage='Summarize unreads'/>
                    </MenuItem>
                    <MenuItem onClick={() => handleSummarizeDays(7)}>
                        <FormattedMessage defaultMessage='Summarize last 7 days'/>
                    </MenuItem>
                    <MenuItem onClick={() => handleSummarizeDays(14)}>
                        <FormattedMessage defaultMessage='Summarize last 14 days'/>
                    </MenuItem>
                    <MenuItem onClick={handleDateRangeSelect}>
                        <FormattedMessage defaultMessage='Select date range to summarize'/>
                    </MenuItem>
                </MenuList>
                <Divider/>
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
                                {activeBot?.displayName}
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
