// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {CloseIcon} from '@mattermost/compass-icons/components';

import {DatePicker} from '@/mm_webapp';

const ModalOverlay = styled.div`
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    background-color: rgba(0, 0, 0, 0.64);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 2000;
`;

const ModalContainer = styled.div`
    background-color: var(--center-channel-bg);
    border-radius: 12px;
    width: 600px;
    display: flex;
    flex-direction: column;
    box-shadow: 0px 8px 24px rgba(0, 0, 0, 0.12);
`;

const ModalHeader = styled.div`
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    padding: 24px 32px;
`;

const HeaderContent = styled.div`
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: 12px;
`;

const ModalTitle = styled.h2`
    font-family: 'Metropolis', sans-serif;
    font-weight: 600;
    font-size: 22px;
    line-height: 28px;
    color: var(--center-channel-color);
    margin: 0;
`;

const ModalSubtitle = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
    color: rgba(var(--center-channel-color-rgb), 0.75);
    font-family: 'Open Sans', sans-serif;
    font-size: 12px;
    line-height: 20px;
    border-left: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    padding-left: 12px;
`;

const CloseButton = styled.button`
    background: none;
    border: none;
    cursor: pointer;
    padding: 0;
    border-radius: 4px;
    color: var(--center-channel-color);
    opacity: 0.64;
    display: flex;
    align-items: center;
    justify-content: center;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.08);
    }
`;

const ModalBody = styled.div`
    padding: 0 32px 24px;
    display: flex;
    flex-direction: column;
    gap: 24px;
`;

const Description = styled.p`
    font-family: 'Open Sans', sans-serif;
    font-size: 14px;
    line-height: 20px;
    color: rgba(var(--center-channel-color-rgb), 0.75);
    margin: 0;
`;

const DateInputsContainer = styled.div`
    display: flex;
    gap: 16px;
    width: 100%;
`;

const DateInputGroup = styled.div`
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 4px;
    position: relative;
`;

const DateLabel = styled.label`
    position: absolute;
    top: -8px;
    left: 12px;
    background-color: var(--center-channel-bg);
    padding: 0 4px;
    font-size: 10px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    z-index: 1;
`;

const DateInput = styled.input`
    width: 100%;
    padding: 10px 16px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    background-color: var(--center-channel-bg);
    color: var(--center-channel-color);
    font-family: 'Open Sans', sans-serif;
    font-size: 14px;
    line-height: 20px;
    outline: none;

    &:focus {
        border-color: var(--button-bg);
        box-shadow: 0 0 0 1px var(--button-bg);
    }

    &::-webkit-calendar-picker-indicator {
        filter: invert(0.5);
        cursor: pointer;
    }
`;

const ModalFooter = styled.div`
    display: flex;
    justify-content: flex-end;
    align-items: center;
    padding: 24px 32px;
    gap: 8px;
`;

const CancelButton = styled.button`
    background: rgba(var(--button-bg-rgb), 0.08);
    color: var(--button-bg);
    border: none;
    border-radius: 4px;
    padding: 10px 20px;
    font-weight: 600;
    font-size: 14px;
    cursor: pointer;
    font-family: 'Open Sans', sans-serif;

    &:hover {
        background: rgba(var(--button-bg-rgb), 0.12);
    }
`;

const SummarizeButton = styled.button`
    background: var(--button-bg);
    color: var(--button-color);
    border: none;
    border-radius: 4px;
    padding: 10px 20px;
    font-weight: 600;
    font-size: 14px;
    cursor: pointer;
    font-family: 'Open Sans', sans-serif;

    &:hover {
        background: rgba(var(--button-bg-rgb), 0.88);
    }
`;

interface Props {
    show: boolean;
    onClose: () => void;
    onSummarize: (startDate: string, endDate: string) => void;
    channelName?: string;
}

// Helper to format Date to YYYY-MM-DD string
const formatDateToString = (date: Date | null): string => {
    if (!date) {
        return '';
    }
    const year = date.getFullYear();
    const month = String(date.getMonth() + 1).padStart(2, '0');
    const day = String(date.getDate()).padStart(2, '0');
    return `${year}-${month}-${day}`;
};

// Helper to parse YYYY-MM-DD string to Date (returns null for empty string)
const parseDateString = (dateStr: string): Date | null => {
    if (!dateStr) {
        return null;
    }
    const [year, month, day] = dateStr.split('-').map(Number);
    return new Date(year, month - 1, day);
};

// Helper to convert null to undefined for react-day-picker compatibility
const nullToUndefined = <T, >(value: T | null): T | undefined => (value === null ? [][0] : value);

export const SummarizeDateRangeModal = ({show, onClose, onSummarize, channelName}: Props) => {
    const intl = useIntl();
    const [startDate, setStartDate] = React.useState<Date | null>(null);
    const [endDate, setEndDate] = React.useState<Date | null>(null);
    const [isStartDateOpen, setIsStartDateOpen] = React.useState(false);
    const [isEndDateOpen, setIsEndDateOpen] = React.useState(false);

    if (!show) {
        return null;
    }

    const handleSummarize = () => {
        onSummarize(formatDateToString(startDate), formatDateToString(endDate));
        onClose();
    };

    // Prevent clicks inside modal from closing it
    const handleModalClick = (e: React.MouseEvent) => {
        e.stopPropagation();
    };

    const handleStartDateSelect = (day: Date | undefined) => {
        setStartDate(day ?? null);
        setIsStartDateOpen(false);
    };

    const handleEndDateSelect = (day: Date | undefined) => {
        setEndDate(day ?? null);
        setIsEndDateOpen(false);
    };

    const locale = intl.locale || 'en';

    const renderStartDateInput = () => {
        if (DatePicker) {
            return (
                <DatePicker
                    isPopperOpen={isStartDateOpen}
                    handlePopperOpenState={setIsStartDateOpen}
                    locale={locale}
                    label={intl.formatMessage({defaultMessage: 'Start date'})}
                    value={startDate?.toLocaleDateString(locale)}
                    datePickerProps={{
                        mode: 'single',
                        selected: nullToUndefined(startDate),
                        onSelect: handleStartDateSelect,
                    }}
                >
                    <span>
                        <FormattedMessage defaultMessage='Select start date'/>
                    </span>
                </DatePicker>
            );
        }

        // Fallback to native date input for older Mattermost versions
        return (
            <>
                <DateLabel>
                    <FormattedMessage defaultMessage='Start date'/>
                </DateLabel>
                <DateInput
                    type='date'
                    value={formatDateToString(startDate)}
                    onChange={(e) => setStartDate(parseDateString(e.target.value))}
                />
            </>
        );
    };

    const renderEndDateInput = () => {
        if (DatePicker) {
            return (
                <DatePicker
                    isPopperOpen={isEndDateOpen}
                    handlePopperOpenState={setIsEndDateOpen}
                    locale={locale}
                    label={intl.formatMessage({defaultMessage: 'End date'})}
                    value={endDate?.toLocaleDateString(locale)}
                    datePickerProps={{
                        mode: 'single',
                        selected: nullToUndefined(endDate),
                        onSelect: handleEndDateSelect,
                    }}
                >
                    <span>
                        <FormattedMessage defaultMessage='Select end date'/>
                    </span>
                </DatePicker>
            );
        }

        // Fallback to native date input for older Mattermost versions
        return (
            <>
                <DateLabel>
                    <FormattedMessage defaultMessage='End date'/>
                </DateLabel>
                <DateInput
                    type='date'
                    value={formatDateToString(endDate)}
                    onChange={(e) => setEndDate(parseDateString(e.target.value))}
                />
            </>
        );
    };

    return (
        <ModalOverlay onClick={onClose}>
            <ModalContainer onClick={handleModalClick}>
                <ModalHeader>
                    <HeaderContent>
                        <ModalTitle>
                            <FormattedMessage defaultMessage='Summarize channel'/>
                        </ModalTitle>
                        {channelName && (
                            <ModalSubtitle>
                                {channelName}
                            </ModalSubtitle>
                        )}
                    </HeaderContent>
                    <CloseButton onClick={onClose}>
                        <CloseIcon size={20}/>
                    </CloseButton>
                </ModalHeader>
                <ModalBody>
                    <Description>
                        <FormattedMessage defaultMessage='Select a date range to summarize messages in this channel.'/>
                    </Description>
                    <DateInputsContainer>
                        <DateInputGroup>
                            {renderStartDateInput()}
                        </DateInputGroup>
                        <DateInputGroup>
                            {renderEndDateInput()}
                        </DateInputGroup>
                    </DateInputsContainer>
                </ModalBody>
                <ModalFooter>
                    <CancelButton onClick={onClose}>
                        <FormattedMessage defaultMessage='Cancel'/>
                    </CancelButton>
                    <SummarizeButton onClick={handleSummarize}>
                        <FormattedMessage defaultMessage='Summarize'/>
                    </SummarizeButton>
                </ModalFooter>
            </ModalContainer>
        </ModalOverlay>
    );
};

