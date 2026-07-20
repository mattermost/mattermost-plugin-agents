// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useRef, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';
import {ArrowLeftIcon} from '@mattermost/compass-icons/components';

import {PrimaryButton, TertiaryButton} from '@/components/assets/buttons';
import ConfirmationDialog from '@/components/confirmation_dialog';

export type ConfigViewTab = {
    id: string;
    label: React.ReactNode;
    disabled?: boolean;
    title?: string;
};

type Props = {
    title: React.ReactNode;
    backAriaLabel: string;
    tabs: ConfigViewTab[];
    activeTabId: string;
    onTabChange: (id: string) => void;
    onBack: () => void;
    onSave: () => void;
    isDirty: boolean;
    saving?: boolean;
    saveLabel?: React.ReactNode;
    savingLabel?: React.ReactNode;
    discardTitleId: string;
    error?: React.ReactNode;
    children: React.ReactNode;
};

const ConfigViewShell = ({
    title,
    backAriaLabel,
    tabs,
    activeTabId,
    onTabChange,
    onBack,
    onSave,
    isDirty,
    saving = false,
    saveLabel,
    savingLabel,
    discardTitleId,
    error,
    children,
}: Props) => {
    const [showDiscardDialog, setShowDiscardDialog] = useState(false);
    const showDiscardDialogRef = useRef(false);
    showDiscardDialogRef.current = showDiscardDialog;

    const requestBack = useCallback(() => {
        if (saving) {
            return;
        }
        if (showDiscardDialogRef.current) {
            return;
        }
        if (isDirty) {
            setShowDiscardDialog(true);
            return;
        }
        onBack();
    }, [isDirty, onBack, saving]);

    const handleDiscardConfirm = useCallback(() => {
        setShowDiscardDialog(false);
        onBack();
    }, [onBack]);

    const handleDiscardCancel = useCallback(() => {
        setShowDiscardDialog(false);
    }, []);

    useEffect(() => {
        const handler = (e: KeyboardEvent) => {
            if (e.key !== 'Escape') {
                return;
            }
            if (showDiscardDialogRef.current) {
                return;
            }
            e.preventDefault();
            requestBack();
        };
        document.addEventListener('keydown', handler);
        return () => document.removeEventListener('keydown', handler);
    }, [requestBack]);

    return (
        <>
            <ViewContainer>
                <ViewHeader>
                    <HeaderLeading>
                        <BackButton
                            type='button'
                            onClick={requestBack}
                            disabled={saving}
                            aria-label={backAriaLabel}
                        >
                            <ArrowLeftIcon size={20}/>
                        </BackButton>
                        <ViewTitle>{title}</ViewTitle>
                    </HeaderLeading>
                </ViewHeader>

                <TabsContainer>
                    {tabs.map((tab) => (
                        <TabButton
                            key={tab.id}
                            type='button'
                            $active={activeTabId === tab.id}
                            disabled={tab.disabled}
                            title={tab.title}
                            onClick={() => {
                                if (!tab.disabled) {
                                    onTabChange(tab.id);
                                }
                            }}
                        >
                            {tab.label}
                        </TabButton>
                    ))}
                </TabsContainer>

                <ViewBody>
                    {error && <ErrorBanner>{error}</ErrorBanner>}
                    {children}
                </ViewBody>

                <ViewFooter>
                    <CancelButton
                        type='button'
                        onClick={requestBack}
                        disabled={saving}
                    >
                        <FormattedMessage defaultMessage='Cancel'/>
                    </CancelButton>
                    <SaveButton
                        onClick={onSave}
                        disabled={saving}
                    >
                        {saving ? (savingLabel ?? <FormattedMessage defaultMessage='Saving...'/>) : (saveLabel ?? <FormattedMessage defaultMessage='Save'/>)}
                    </SaveButton>
                </ViewFooter>
            </ViewContainer>
            <ConfirmationDialog
                show={showDiscardDialog}
                titleId={discardTitleId}
                title={<FormattedMessage defaultMessage='Discard changes?'/>}
                message={(
                    <FormattedMessage defaultMessage='You have unsaved changes. If you close now, those changes will be lost.'/>
                )}
                confirmButtonText={<FormattedMessage defaultMessage='Discard'/>}
                cancelButtonText={<FormattedMessage defaultMessage='Keep editing'/>}
                onConfirm={handleDiscardConfirm}
                onCancel={handleDiscardCancel}
                isDestructive={true}
                managedAccessibility={true}
                zIndex={2100}
            />
        </>
    );
};

const ViewContainer = styled.div`
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
    width: 100%;
`;

const ViewHeader = styled.div`
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 48px 0 16px 0;
    flex-shrink: 0;
`;

const HeaderLeading = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
`;

const ViewTitle = styled.h1`
    font-family: 'Metropolis', sans-serif;
    font-weight: 600;
    font-size: 22px;
    line-height: 28px;
    color: var(--center-channel-color);
    margin: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
`;

const BackButton = styled.button`
    background: none;
    border: none;
    cursor: pointer;
    padding: 8px;
    margin-left: -8px;
    border-radius: 4px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    display: flex;
    align-items: center;
    justify-content: center;

    &:hover:not(:disabled) {
        background: rgba(var(--center-channel-color-rgb), 0.08);
        color: var(--center-channel-color);
    }

    &:disabled {
        cursor: not-allowed;
        opacity: 0.4;
    }
`;

const TabsContainer = styled.div`
    display: flex;
    box-sizing: border-box;
    width: 100%;
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    flex-shrink: 0;
`;

const TabButton = styled.button<{$active: boolean}>`
    padding: 12px 16px;
    border: none;
    background: none;
    cursor: pointer;
    font-size: 14px;
    font-weight: 600;
    color: ${(p) => (p.$active ? 'var(--button-bg)' : 'rgba(var(--center-channel-color-rgb), 0.64)')};
    border-bottom: 2px solid ${(p) => (p.$active ? 'var(--button-bg)' : 'transparent')};
    transition: color 0.2s ease, border-color 0.2s ease;
    margin-bottom: -1px;

    &:hover:not(:disabled) {
        color: ${(p) => (p.$active ? 'var(--button-bg)' : 'var(--center-channel-color)')};
    }

    &:disabled {
        opacity: 0.4;
        cursor: not-allowed;
    }
`;

const ViewBody = styled.div`
    padding: 32px 16px;
    flex: 1;
    min-height: 0;
    overflow-y: auto;
`;

const ErrorBanner = styled.div`
    padding: 10px 12px;
    margin-bottom: 16px;
    background: rgba(var(--dnd-indicator-rgb, 210, 75, 78), 0.08);
    border-radius: 4px;
    border: 1px solid rgba(var(--dnd-indicator-rgb, 210, 75, 78), 0.3);
    color: var(--dnd-indicator, #D24B4E);
    font-size: 14px;
`;

const ViewFooter = styled.div`
    display: flex;
    justify-content: flex-end;
    align-items: center;
    padding: 16px 0;
    gap: 8px;
    flex-shrink: 0;
    background: var(--center-channel-bg);
    border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
`;

const CancelButton = styled(TertiaryButton)`
    height: 40px;
`;

const SaveButton = styled(PrimaryButton)`
    height: 40px;
`;

export default ConfigViewShell;
