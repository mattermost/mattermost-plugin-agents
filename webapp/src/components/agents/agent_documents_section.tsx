// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useMemo, useRef, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {AgentDocument, MaxAgentDocumentBytes, MaxAgentDocuments, MaxAgentDocumentsTextRunes} from '@/types/agents';
import {TertiaryButton} from '@/components/assets/buttons';
import {
    FieldErrorText,
    FormRow,
    HelpText,
    ItemLabel,
    StyledInput,
    TextFieldContainer,
} from '@/components/system_console/item';
import {downloadAgentDocument} from '@/utils/download_agent_document';

import {
    AgentDocumentAccept,
    documentNameError,
    documentTypeLabel,
    formatDocumentSize,
    totalDocumentTextRunes,
} from './agent_documents';
import type {DocumentUploadItem} from './use_agent_document_uploads';

type Props = {
    documents: AgentDocument[];
    uploads: DocumentUploadItem[];

    /** Set for existing agents; downloads need a saved agent. */
    agentId?: string;

    /** Documents that are already saved on the agent (downloadable), by id. */
    savedDocumentIds: string[];
    error?: string;
    onChange: (documents: AgentDocument[]) => void;
    onUpload: (files: File[]) => void;
    onDismissUpload: (key: string) => void;
}

const AgentDocumentsSection = (props: Props) => {
    const {documents, uploads, agentId, savedDocumentIds, error, onChange, onUpload, onDismissUpload} = props;
    const intl = useIntl();
    const fileInputRef = useRef<HTMLInputElement>(null);
    const [renamingId, setRenamingId] = useState<string | null>(null);
    const [renameValue, setRenameValue] = useState('');
    const [renameError, setRenameError] = useState('');
    const [downloadError, setDownloadError] = useState('');
    const [downloadingId, setDownloadingId] = useState<string | null>(null);

    const usedRunes = useMemo(() => totalDocumentTextRunes(documents), [documents]);
    const overBudget = usedRunes > MaxAgentDocumentsTextRunes;

    const startRename = (doc: AgentDocument) => {
        setRenamingId(doc.id);
        setRenameValue(doc.name);
        setRenameError('');
    };

    const cancelRename = () => {
        setRenamingId(null);
        setRenameError('');
    };

    const commitRename = () => {
        if (renamingId === null) {
            return;
        }
        const problem = documentNameError(intl, renameValue);
        if (problem) {
            setRenameError(problem);
            return;
        }
        const name = renameValue.trim();
        const current = documents.find((doc) => doc.id === renamingId);
        if (current && current.name !== name) {
            onChange(documents.map((doc) => (doc.id === renamingId ? {...doc, name} : doc)));
        }
        cancelRename();
    };

    const handleRemove = (id: string) => {
        if (renamingId === id) {
            cancelRename();
        }
        onChange(documents.filter((doc) => doc.id !== id));
    };

    const handleDownload = async (doc: AgentDocument) => {
        if (!agentId) {
            return;
        }
        setDownloadError('');
        setDownloadingId(doc.id);
        try {
            await downloadAgentDocument(agentId, doc.id, doc.name);
        } catch (e) {
            const message = (e as {message?: unknown})?.message;
            setDownloadError(typeof message === 'string' && message.trim() ? message.trim() : intl.formatMessage({defaultMessage: 'Failed to download the document. Please try again.'}));
        } finally {
            setDownloadingId(null);
        }
    };

    const handleFilesChosen = (e: React.ChangeEvent<HTMLInputElement>) => {
        const files = Array.from(e.target.files ?? []);

        // Let choosing the same file again (e.g. after removing it) fire another change.
        e.target.value = '';
        if (files.length > 0) {
            onUpload(files);
        }
    };

    return (
        <FormRow data-testid='agent-documents'>
            <SectionLabel as='span'>
                {intl.formatMessage({defaultMessage: 'Reference documents'})}
            </SectionLabel>
            <TextFieldContainer>
                {error && <FieldErrorText role='alert'>{error}</FieldErrorText>}

                {documents.length > 0 && (
                    <DocumentList aria-label={intl.formatMessage({defaultMessage: 'Reference documents'})}>
                        {documents.map((doc) => {
                            const renaming = renamingId === doc.id;
                            const canDownload = Boolean(agentId) && savedDocumentIds.includes(doc.id);
                            return (
                                <DocumentRow key={doc.id}>
                                    <DocumentMain>
                                        {renaming ? (
                                            <RenameRow>
                                                <StyledInput
                                                    autoFocus={true}
                                                    aria-label={intl.formatMessage({defaultMessage: 'Document name'})}
                                                    value={renameValue}
                                                    onChange={(e) => {
                                                        setRenameValue(e.target.value);
                                                        setRenameError('');
                                                    }}
                                                    onKeyDown={(e) => {
                                                        if (e.key === 'Enter') {
                                                            e.preventDefault();
                                                            commitRename();
                                                        } else if (e.key === 'Escape') {
                                                            e.preventDefault();
                                                            cancelRename();
                                                        }
                                                    }}
                                                />
                                                <RowButton
                                                    type='button'
                                                    onClick={commitRename}
                                                >
                                                    <FormattedMessage defaultMessage='Done'/>
                                                </RowButton>
                                                <RowButton
                                                    type='button'
                                                    onClick={cancelRename}
                                                >
                                                    <FormattedMessage defaultMessage='Cancel'/>
                                                </RowButton>
                                            </RenameRow>
                                        ) : (
                                            <DocumentName title={doc.name}>{doc.name}</DocumentName>
                                        )}
                                        {renaming && renameError && <FieldErrorText role='alert'>{renameError}</FieldErrorText>}
                                        <DocumentMeta>
                                            {[
                                                documentTypeLabel(intl, doc),
                                                formatDocumentSize(intl, doc.size),
                                                intl.formatMessage(
                                                    {defaultMessage: '{count, plural, one {# character extracted} other {# characters extracted}}'},
                                                    {count: doc.textRunes},
                                                ),
                                            ].join(' · ')}
                                        </DocumentMeta>
                                    </DocumentMain>
                                    <DocumentActions>
                                        {!renaming && (
                                            <RowButton
                                                type='button'
                                                aria-label={intl.formatMessage({defaultMessage: 'Rename {name}'}, {name: doc.name})}
                                                onClick={() => startRename(doc)}
                                            >
                                                <FormattedMessage defaultMessage='Rename'/>
                                            </RowButton>
                                        )}
                                        {canDownload && (
                                            <RowButton
                                                type='button'
                                                aria-label={intl.formatMessage({defaultMessage: 'Download {name}'}, {name: doc.name})}
                                                disabled={downloadingId === doc.id}
                                                onClick={() => handleDownload(doc)}
                                            >
                                                <FormattedMessage defaultMessage='Download'/>
                                            </RowButton>
                                        )}
                                        <RowButton
                                            type='button'
                                            aria-label={intl.formatMessage({defaultMessage: 'Remove {name}'}, {name: doc.name})}
                                            onClick={() => handleRemove(doc.id)}
                                        >
                                            <FormattedMessage defaultMessage='Remove'/>
                                        </RowButton>
                                    </DocumentActions>
                                </DocumentRow>
                            );
                        })}
                    </DocumentList>
                )}

                {uploads.length > 0 && (
                    <DocumentList aria-label={intl.formatMessage({defaultMessage: 'Document uploads'})}>
                        {uploads.map((item) => (
                            <DocumentRow key={item.key}>
                                <DocumentMain>
                                    <DocumentName title={item.name}>{item.name}</DocumentName>
                                    {item.status === 'uploading' ? (
                                        <DocumentMeta>
                                            <FormattedMessage defaultMessage='Uploading...'/>
                                        </DocumentMeta>
                                    ) : (
                                        <FieldErrorText role='alert'>{item.error}</FieldErrorText>
                                    )}
                                </DocumentMain>
                                {item.status === 'error' && (
                                    <DocumentActions>
                                        <RowButton
                                            type='button'
                                            aria-label={intl.formatMessage({defaultMessage: 'Dismiss error for {name}'}, {name: item.name})}
                                            onClick={() => onDismissUpload(item.key)}
                                        >
                                            <FormattedMessage defaultMessage='Dismiss'/>
                                        </RowButton>
                                    </DocumentActions>
                                )}
                            </DocumentRow>
                        ))}
                    </DocumentList>
                )}

                {downloadError && <FieldErrorText role='alert'>{downloadError}</FieldErrorText>}

                <UploadRow>
                    <TertiaryButton
                        type='button'
                        onClick={() => fileInputRef.current?.click()}
                    >
                        <FormattedMessage defaultMessage='Upload documents'/>
                    </TertiaryButton>
                    <HiddenFileInput
                        ref={fileInputRef}
                        type='file'
                        multiple={true}
                        accept={AgentDocumentAccept}
                        aria-label={intl.formatMessage({defaultMessage: 'Upload documents'})}
                        data-testid='agent-documents-input'
                        onChange={handleFilesChosen}
                    />
                    <UsageText
                        $warning={overBudget}
                        data-testid='agent-documents-usage'
                    >
                        {intl.formatMessage(
                            {defaultMessage: '{used} of {max} characters of extracted text used'},
                            {used: intl.formatNumber(usedRunes), max: intl.formatNumber(MaxAgentDocumentsTextRunes)},
                        )}
                    </UsageText>
                </UploadRow>

                <HelpText>
                    {intl.formatMessage(
                        {defaultMessage: 'Documents are added to the agent\'s context on every request, so keep them focused. You can add up to {max} PDF, text, Markdown, CSV, or JSON files of up to {maxSize} each. Changes take effect when you save and create a new version.'},
                        {max: MaxAgentDocuments, maxSize: formatDocumentSize(intl, MaxAgentDocumentBytes)},
                    )}
                </HelpText>
            </TextFieldContainer>
        </FormRow>
    );
};

const SectionLabel = styled(ItemLabel)`
    cursor: default;
`;

const DocumentList = styled.ul`
    margin: 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: 8px;
`;

const DocumentRow = styled.li`
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 8px 12px;
    border-radius: 4px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    background: var(--center-channel-bg);
`;

const DocumentMain = styled.div`
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
    flex: 1;
`;

const DocumentName = styled.span`
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    color: var(--center-channel-color);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
`;

const DocumentMeta = styled.span`
    font-size: 12px;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
`;

const DocumentActions = styled.div`
    display: flex;
    align-items: center;
    gap: 4px;
    flex-shrink: 0;
`;

const RenameRow = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
`;

const RowButton = styled.button`
    padding: 4px 8px;
    border: none;
    border-radius: 4px;
    background: none;
    font-size: 12px;
    font-weight: 600;
    color: var(--button-bg);
    cursor: pointer;

    &:hover:not(:disabled) {
        background: rgba(var(--button-bg-rgb), 0.08);
    }

    &:disabled {
        color: rgba(var(--center-channel-color-rgb), 0.32);
        cursor: not-allowed;
    }
`;

const UploadRow = styled.div`
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 12px;
`;

const HiddenFileInput = styled.input`
    display: none;
`;

const UsageText = styled.span<{$warning: boolean}>`
    font-size: 12px;
    line-height: 16px;
    font-weight: ${({$warning}) => ($warning ? 600 : 400)};
    color: ${({$warning}) => ($warning ? 'var(--dnd-indicator, #D24B4E)' : 'rgba(var(--center-channel-color-rgb), 0.72)')};
`;

export default AgentDocumentsSection;
