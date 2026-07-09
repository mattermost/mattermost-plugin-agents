// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {ChangeEvent, useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import styled from 'styled-components';

import CloseIcon from '@mattermost/compass-icons/components/close';
import FileExcelOutlineIcon from '@mattermost/compass-icons/components/file-excel-outline';
import FileGenericOutlineIcon from '@mattermost/compass-icons/components/file-generic-outline';
import FilePdfOutlineIcon from '@mattermost/compass-icons/components/file-pdf-outline';
import FilePowerpointOutlineIcon from '@mattermost/compass-icons/components/file-powerpoint-outline';
import FileTextOutlineIcon from '@mattermost/compass-icons/components/file-text-outline';
import FileWordOutlineIcon from '@mattermost/compass-icons/components/file-word-outline';

import {getChannelContext, saveChannelContext, uploadChannelKnowledgeFiles} from '@/client';
import {ButtonIcon, TertiaryButton} from '@/components/assets/buttons';
import LoadingSpinner from '@/components/assets/loading_spinner';
import {
    ChannelContextState,
    ChannelKnowledgeFile,
    ChannelSettingsTabBodyProps,
    MAX_CHANNEL_INSTRUCTIONS,
    MAX_CHANNEL_KNOWLEDGE_FILES,
} from '@/types/channel_settings';

const ACCEPTED_FILE_TYPES = '.pdf,.doc,.docx,.xls,.xlsx,.ppt,.pptx,.txt,.md,.csv,.rtf';
const EMPTY_STATE: ChannelContextState = {customInstructions: '', files: []};

function cloneState(state: ChannelContextState): ChannelContextState {
    return {...state, files: [...state.files]};
}

function statesEqual(left: ChannelContextState, right: ChannelContextState): boolean {
    return left.customInstructions === right.customInstructions &&
        left.files.length === right.files.length &&
        left.files.every((file, index) => file.id === right.files[index].id);
}

function errorMessage(error: unknown, fallback: string): string {
    return error instanceof Error && error.message ? error.message : fallback;
}

function fileExtension(file: ChannelKnowledgeFile): string {
    const extension = file.name.includes('.') ? file.name.split('.').pop() : '';
    return extension?.toUpperCase() || file.mimeType || 'FILE';
}

function formatFileSize(size: number): string {
    if (size < 1024) {
        return `${size} B`;
    }
    if (size < 1024 * 1024) {
        return `${Math.round(size / 1024)} KB`;
    }
    return `${(size / (1024 * 1024)).toFixed(1)} MB`;
}

function instructionLength(value: string): number {
    return Array.from(value).length;
}

function truncateInstructions(value: string): string {
    return Array.from(value).slice(0, MAX_CHANNEL_INSTRUCTIONS).join('');
}

function FileIcon({file}: {file: ChannelKnowledgeFile}) {
    const extension = fileExtension(file);
    if (extension === 'PDF') {
        return <FilePdfOutlineIcon size={32}/>;
    }
    if (extension === 'DOC' || extension === 'DOCX') {
        return <FileWordOutlineIcon size={32}/>;
    }
    if (extension === 'XLS' || extension === 'XLSX' || extension === 'CSV') {
        return <FileExcelOutlineIcon size={32}/>;
    }
    if (extension === 'PPT' || extension === 'PPTX') {
        return <FilePowerpointOutlineIcon size={32}/>;
    }
    if (extension === 'TXT' || extension === 'MD' || extension === 'RTF') {
        return <FileTextOutlineIcon size={32}/>;
    }
    return <FileGenericOutlineIcon size={32}/>;
}

const ChannelContextSettings = ({channel, setUnsaved, registerHandlers}: ChannelSettingsTabBodyProps) => {
    const intl = useIntl();
    const fileInputRef = useRef<HTMLInputElement>(null);
    const channelIDRef = useRef(channel.id);
    const uploadGenerationRef = useRef(0);
    channelIDRef.current = channel.id;

    const [baseline, setBaseline] = useState<ChannelContextState | null>(null);
    const [draft, setDraft] = useState<ChannelContextState>(EMPTY_STATE);
    const [loading, setLoading] = useState(true);
    const [uploading, setUploading] = useState(false);
    const [loadError, setLoadError] = useState('');
    const [actionError, setActionError] = useState('');

    const dirty = useMemo(
        () => uploading || (baseline !== null && !statesEqual(draft, baseline)),
        [baseline, draft, uploading],
    );

    useEffect(() => {
        setUnsaved(dirty);
    }, [dirty, setUnsaved]);

    useEffect(() => {
        return () => setUnsaved(false);
    }, [setUnsaved]);

    useEffect(() => {
        let active = true;
        uploadGenerationRef.current++;
        setUploading(false);
        setLoading(true);
        setBaseline(null);
        setDraft(EMPTY_STATE);
        setLoadError('');
        setActionError('');

        getChannelContext(channel.id).then((state) => {
            if (!active) {
                return;
            }
            setBaseline(cloneState(state));
            setDraft(cloneState(state));
            setLoading(false);
        }).catch((error: unknown) => {
            if (!active) {
                return;
            }
            setLoadError(errorMessage(
                error,
                intl.formatMessage({defaultMessage: 'Could not load channel AI context.'}),
            ));
            setLoading(false);
        });

        return () => {
            active = false;
            uploadGenerationRef.current++;
        };
    }, [channel.id, intl]);

    const save = useCallback(async () => {
        if (loadError || baseline === null) {
            const message = intl.formatMessage({defaultMessage: 'Channel AI context has not loaded.'});
            setActionError(message);
            throw new Error(message);
        }
        if (uploading) {
            const message = intl.formatMessage({defaultMessage: 'Wait for file uploads to finish before saving.'});
            setActionError(message);
            throw new Error(message);
        }

        setActionError('');
        try {
            const saveChannelID = channel.id;
            const saved = await saveChannelContext(channel.id, {
                customInstructions: draft.customInstructions,
                fileIDs: draft.files.map((file) => file.id),
            });
            if (channelIDRef.current !== saveChannelID) {
                return;
            }
            setBaseline(cloneState(saved));
            setDraft(cloneState(saved));
        } catch (error: unknown) {
            setActionError(errorMessage(
                error,
                intl.formatMessage({defaultMessage: 'Could not save channel AI context.'}),
            ));
            throw error;
        }
    }, [baseline, channel.id, draft, intl, loadError, uploading]);

    const reset = useCallback(() => {
        uploadGenerationRef.current++;
        setUploading(false);
        if (baseline !== null) {
            setDraft(cloneState(baseline));
        }
        setActionError('');
    }, [baseline]);

    const handlersRef = useRef({save, reset});
    handlersRef.current = {save, reset};

    useEffect(() => {
        registerHandlers({
            save: () => handlersRef.current.save(),
            reset: () => handlersRef.current.reset(),
        });
        return () => registerHandlers(null);
    }, [registerHandlers]);

    const handleFiles = useCallback(async (event: ChangeEvent<HTMLInputElement>) => {
        const selectedFiles = Array.from(event.target.files ?? []);
        event.target.value = '';
        if (selectedFiles.length === 0) {
            return;
        }

        const available = MAX_CHANNEL_KNOWLEDGE_FILES - draft.files.length;
        if (selectedFiles.length > available) {
            setActionError(intl.formatMessage(
                {defaultMessage: 'A channel can have up to {count} knowledge files.'},
                {count: MAX_CHANNEL_KNOWLEDGE_FILES},
            ));
            return;
        }

        const uploadChannelID = channel.id;
        const uploadGeneration = ++uploadGenerationRef.current;
        setActionError('');
        setUploading(true);
        try {
            const uploaded = await uploadChannelKnowledgeFiles(uploadChannelID, selectedFiles);
            if (channelIDRef.current !== uploadChannelID || uploadGenerationRef.current !== uploadGeneration) {
                return;
            }
            setDraft((current) => {
                const existing = new Set(current.files.map((file) => file.id));
                return {
                    ...current,
                    files: [...current.files, ...uploaded.filter((file) => !existing.has(file.id))],
                };
            });
        } catch (error: unknown) {
            setActionError(errorMessage(
                error,
                intl.formatMessage({defaultMessage: 'Could not upload knowledge files.'}),
            ));
        } finally {
            if (channelIDRef.current === uploadChannelID && uploadGenerationRef.current === uploadGeneration) {
                setUploading(false);
            }
        }
    }, [channel.id, draft.files.length, intl]);

    const removeFile = useCallback((fileID: string) => {
        setDraft((current) => ({
            ...current,
            files: current.files.filter((file) => file.id !== fileID),
        }));
        setActionError('');
    }, []);

    if (loading) {
        return (
            <LoadingContainer>
                <LoadingSpinner/>
                <FormattedMessage defaultMessage='Loading channel AI context…'/>
            </LoadingContainer>
        );
    }

    if (loadError) {
        return <ErrorMessage role='alert'>{loadError}</ErrorMessage>;
    }

    return (
        <Container>
            <FieldGroup>
                <FieldLabel htmlFor='agents-channel-custom-instructions'>
                    <FormattedMessage defaultMessage='Custom instructions'/>
                </FieldLabel>
                <HelpText>
                    <FormattedMessage defaultMessage='Add context or instructions that agents should follow in this channel, including threads.'/>
                </HelpText>
                <Instructions
                    id='agents-channel-custom-instructions'
                    value={draft.customInstructions}
                    rows={8}
                    placeholder={intl.formatMessage({defaultMessage: 'Add channel-specific context or instructions…'})}
                    onChange={(event) => setDraft((current) => ({
                        ...current,
                        customInstructions: truncateInstructions(event.target.value),
                    }))}
                />
                <CharacterCount>
                    {instructionLength(draft.customInstructions).toLocaleString()}{'/'}
                    {MAX_CHANNEL_INSTRUCTIONS.toLocaleString()}
                </CharacterCount>
            </FieldGroup>

            <FieldGroup>
                <FieldLabel>
                    <FormattedMessage defaultMessage='Knowledge base files'/>
                </FieldLabel>
                <HelpText>
                    <FormattedMessage defaultMessage='Agents can read these files on demand when they are relevant to a request.'/>
                </HelpText>
                <UploadButton
                    type='button'
                    disabled={uploading || draft.files.length >= MAX_CHANNEL_KNOWLEDGE_FILES}
                    onClick={() => fileInputRef.current?.click()}
                >
                    {uploading ? (
                        <FormattedMessage defaultMessage='Uploading…'/>
                    ) : (
                        <FormattedMessage defaultMessage='Upload files'/>
                    )}
                </UploadButton>
                <HiddenFileInput
                    ref={fileInputRef}
                    type='file'
                    multiple={true}
                    accept={ACCEPTED_FILE_TYPES}
                    aria-label={intl.formatMessage({defaultMessage: 'Upload knowledge base files'})}
                    onChange={handleFiles}
                />

                <FileList>
                    {draft.files.map((file) => (
                        <FileCard key={file.id}>
                            <FileIconContainer>
                                <FileIcon file={file}/>
                            </FileIconContainer>
                            <FileDetails>
                                <FileName title={file.name}>{file.name}</FileName>
                                <FileMetadata>{fileExtension(file)} {formatFileSize(file.size)}</FileMetadata>
                            </FileDetails>
                            <RemoveButton
                                type='button'
                                aria-label={intl.formatMessage(
                                    {defaultMessage: 'Remove {filename}'},
                                    {filename: file.name},
                                )}
                                onClick={() => removeFile(file.id)}
                            >
                                <CloseIcon size={18}/>
                            </RemoveButton>
                        </FileCard>
                    ))}
                </FileList>
            </FieldGroup>

            {actionError && <ErrorMessage role='alert'>{actionError}</ErrorMessage>}
        </Container>
    );
};

const Container = styled.div`
    display: flex;
    flex-direction: column;
    gap: 28px;
    color: var(--center-channel-color);
`;

const FieldGroup = styled.div`
    display: flex;
    flex-direction: column;
    align-items: flex-start;
`;

const FieldLabel = styled.label`
    margin: 0 0 4px;
    color: var(--center-channel-color);
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
`;

const HelpText = styled.div`
    margin-bottom: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    font-size: 12px;
    line-height: 16px;
`;

const Instructions = styled.textarea`
    width: 100%;
    min-height: 144px;
    resize: vertical;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    background: var(--center-channel-bg);
    color: var(--center-channel-color);
    font-size: 14px;
    line-height: 20px;
    padding: 10px 12px;

    &::placeholder {
        color: rgba(var(--center-channel-color-rgb), 0.56);
    }

    &:focus {
        border-color: var(--button-bg);
        box-shadow: inset 0 0 0 1px var(--button-bg);
        outline: none;
    }
`;

const CharacterCount = styled.div`
    width: 100%;
    margin-top: 4px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    font-size: 12px;
    text-align: end;
`;

const UploadButton = styled(TertiaryButton)`
    height: 32px;
    padding: 0 16px;
`;

const HiddenFileInput = styled.input`
    display: none;
`;

const FileList = styled.div`
    display: flex;
    width: 100%;
    flex-direction: column;
    gap: 8px;
    margin-top: 8px;
`;

const FileCard = styled.div`
    display: flex;
    width: 100%;
    min-height: 64px;
    align-items: center;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    background: var(--center-channel-bg);
    box-shadow: 0 1px 2px rgba(0, 0, 0, 0.08);
    padding: 8px 12px;
`;

const FileIconContainer = styled.div`
    display: flex;
    width: 40px;
    height: 40px;
    flex: 0 0 40px;
    align-items: center;
    justify-content: center;
    color: var(--button-bg);
`;

const FileDetails = styled.div`
    min-width: 0;
    flex: 1;
    margin-inline-start: 10px;
`;

const FileName = styled.div`
    overflow: hidden;
    color: var(--center-channel-color);
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    text-overflow: ellipsis;
    white-space: nowrap;
`;

const FileMetadata = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.56);
    font-size: 11px;
    line-height: 16px;
`;

const RemoveButton = styled(ButtonIcon)`
    flex: 0 0 28px;
    margin-inline-start: 8px;
`;

const LoadingContainer = styled.div`
    display: flex;
    min-height: 180px;
    align-items: center;
    justify-content: center;
    gap: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
`;

const ErrorMessage = styled.div`
    width: 100%;
    border-radius: 4px;
    background: rgba(var(--error-text-color-rgb), 0.08);
    color: var(--error-text);
    font-size: 13px;
    line-height: 18px;
    padding: 10px 12px;
`;

export default ChannelContextSettings;
