// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {Button} from '@mattermost/compass-ui/components/button';
import {Modal} from '@mattermost/compass-ui/components/modal';
import {TextInput} from '@mattermost/compass-ui/components/text-input';

import {AnimatedModalShell, MODAL_SHEET_CLASS} from '@/components/animated_modal_shell';
import {DatePicker} from '@/mm_webapp';

const Sheet = styled.div`
    display: flex;
    max-width: calc(100vw - 32px);
`;

// The host DatePicker's calendar is not portaled, so the dialog must not clip it.
const SummarizeModal = styled(Modal)`
    && {
        max-width: 100%;
        overflow: visible;
    }
`;

const ModalBody = styled.div`
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

interface Props {
    show: boolean;
    onClose: () => void;
    onSummarize: (startDate: string, endDate: string) => void;
    channelName?: string;
}

const START_DATE_INPUT_ID = 'summarize-range-start-date';
const END_DATE_INPUT_ID = 'summarize-range-end-date';

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

    useEffect(() => {
        if (show) {
            setStartDate(null);
            setEndDate(null);
            setIsStartDateOpen(false);
            setIsEndDateOpen(false);
        }
    }, [show]);

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
                <DateLabel htmlFor={START_DATE_INPUT_ID}>
                    <FormattedMessage defaultMessage='Start date'/>
                </DateLabel>
                <TextInput
                    id={START_DATE_INPUT_ID}
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
                <DateLabel htmlFor={END_DATE_INPUT_ID}>
                    <FormattedMessage defaultMessage='End date'/>
                </DateLabel>
                <TextInput
                    id={END_DATE_INPUT_ID}
                    type='date'
                    value={formatDateToString(endDate)}
                    onChange={(e) => setEndDate(parseDateString(e.target.value))}
                />
            </>
        );
    };

    return (
        <AnimatedModalShell
            show={show}
            onBackdropClick={onClose}
            zIndex={2000}
        >
            <Sheet
                className={MODAL_SHEET_CLASS}
                onClick={handleModalClick}
            >
                <SummarizeModal
                    size='small'
                    title={<FormattedMessage defaultMessage='Summarize channel'/>}
                    subtitle={channelName}
                    subtitlePlacement='beside'
                    onClose={onClose}
                    headerDivider={false}
                    footerDivider={false}
                    scrollable={false}
                    footer={(
                        <>
                            <Button
                                emphasis='tertiary'
                                onClick={onClose}
                            >
                                <FormattedMessage defaultMessage='Cancel'/>
                            </Button>
                            <Button
                                emphasis='primary'
                                onClick={handleSummarize}
                            >
                                <FormattedMessage defaultMessage='Summarize'/>
                            </Button>
                        </>
                    )}
                >
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
                </SummarizeModal>
            </Sheet>
        </AnimatedModalShell>
    );
};

