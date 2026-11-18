// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {useSelector, useStore} from 'react-redux';
import styled from 'styled-components';

import {GlobalState} from '@mattermost/types/store';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {Button} from '@mattermost/compass-ui/components/button';
import {Checkbox} from '@mattermost/compass-ui/components/checkbox';
import {Modal} from '@mattermost/compass-ui/components/modal';
import {PinIcon, PinOutlineIcon} from '@mattermost/compass-icons/components';

import {
    ChannelArtifactEntry,
    createChannelArtifact,
} from '@/client';
import {getChannelArtifactsEnabled} from '@/selectors';

import {AnimatedModalShell} from '../animated_modal_shell';

import {canAddChannelBookmarks, pinLibraryArtifactBookmark} from './bookmark';
import {useChannelLibrary} from './library';

interface Props {
    fileId: string;
    fileName: string;
    postId: string;
    channelId: string;
}

function shaPrefix(sha?: string): string {
    if (!sha) {
        return '';
    }
    return sha.slice(0, 8);
}

function pickReplaceTarget(
    entries: ChannelArtifactEntry[],
    fileId: string,
): ChannelArtifactEntry | null {
    return entries.find((entry) => entry.source_file_id === fileId) || null;
}

const SaveToChannelButton = ({fileId, fileName, postId, channelId}: Props) => {
    const intl = useIntl();
    const store = useStore<GlobalState>();
    const enabled = useSelector(getChannelArtifactsEnabled);
    const canPin = useSelector<GlobalState, boolean>((state) => canAddChannelBookmarks(state, channelId));
    const library = useChannelLibrary(channelId);
    const [open, setOpen] = useState(false);
    const [title, setTitle] = useState('');
    const [replaceId, setReplaceId] = useState('');
    const [ackUpdate, setAckUpdate] = useState(false);
    const [pinBookmark, setPinBookmark] = useState(false);
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState('');

    const existing = library.entries;
    const savedId = pickReplaceTarget(existing, fileId)?.id ?? null;

    // The server decides who may save. Show the control once it says someone
    // may, and also after a failed load so a click can retry.
    if (!enabled || !channelId || !postId || !(library.status === 'error' || library.canAdd || library.canEdit)) {
        return null;
    }

    const defaultTitle = () => {
        const base = fileName.includes('.') ? fileName.replace(/\.[^.]+$/, '') : fileName;
        return (base || 'Artifact').slice(0, 64);
    };

    const openModal = (e: React.MouseEvent) => {
        if (!e.nativeEvent.isTrusted || saving) {
            return;
        }
        if (library.status === 'error') {
            library.reload();
            return;
        }
        const nextTitle = defaultTitle();
        setError('');
        setAckUpdate(false);
        setPinBookmark(false);

        const target = pickReplaceTarget(existing, fileId);
        if (target) {
            setReplaceId(target.id);
            setTitle(target.title || nextTitle);
        } else {
            setReplaceId('');
            setTitle(nextTitle);
        }
        setOpen(true);
    };

    const saveErrorMessage = (err: any): string => {
        switch (err?.status_code) {
        case 403:
            return intl.formatMessage({defaultMessage: "You don't have permission to save artifacts in this channel."});
        case 404:
            return intl.formatMessage({defaultMessage: 'The message or file is no longer available.'});
        case 409:
            return intl.formatMessage({defaultMessage: "This channel's artifact library is full. Remove one first."});
        case 413:
            return intl.formatMessage({defaultMessage: 'This artifact is too large to save.'});
        case 422:
            return intl.formatMessage({defaultMessage: 'Only HTML files posted by an agent can be saved.'});
        default:
            return intl.formatMessage({defaultMessage: 'Could not save artifact.'});
        }
    };

    const onSave = async () => {
        setSaving(true);
        setError('');
        const pinSaved = async (id: string, savedTitle: string) => {
            if (!pinBookmark) {
                setOpen(false);
                return;
            }
            try {
                const bookmark = await pinLibraryArtifactBookmark(store.getState(), channelId, id, savedTitle, store.dispatch);
                if (!bookmark) {
                    setError(intl.formatMessage({defaultMessage: 'Saved the artifact, but could not pin it to bookmarks.'}));
                    return;
                }
                setOpen(false);
            } catch {
                setError(intl.formatMessage({defaultMessage: 'Saved the artifact, but could not pin it to bookmarks.'}));
            }
        };
        try {
            const entry = await createChannelArtifact({
                channel_id: channelId,
                post_id: postId,
                file_id: fileId,
                title: title.trim() || defaultTitle(),
                ...(replaceId ? {replace_id: replaceId} : {}),
            });
            store.dispatch({type: 'CHANNEL_ARTIFACT_UPSERT', entry});
            await pinSaved(entry.id, entry.title);
        } catch (err: any) {
            if (typeof err?.message === 'string' && err.message.startsWith('duplicate:')) {
                const id = err.message.slice('duplicate:'.length);
                if (replaceId && id !== replaceId) {
                    setError(intl.formatMessage({defaultMessage: 'This file matches a different library entry.'}));
                    return;
                }
                const stored = existing.find((entry) => entry.id === id);
                await pinSaved(id, stored?.title || title.trim() || defaultTitle());
            } else {
                setError(saveErrorMessage(err));
            }
        } finally {
            setSaving(false);
        }
    };

    const updating = Boolean(replaceId);
    const replaceEntry = updating ? existing.find((e) => e.id === replaceId) : null;
    const canSubmit = Boolean(title.trim()) && (!updating || ackUpdate);
    const label = savedId ? intl.formatMessage({defaultMessage: 'Update channel artifact'}) : intl.formatMessage({defaultMessage: 'Save to channel artifacts'});

    return (
        <Wrap>
            <IconButton
                size='x-small'
                icon={<Icon glyph={savedId ? <PinIcon/> : <PinOutlineIcon/>}/>}
                aria-label={label}
                title={label}
                onClick={openModal}
                data-testid='html-artifact-save-to-channel'
            />
            <AnimatedModalShell
                show={open}
                onBackdropClick={() => !saving && setOpen(false)}
            >
                <Modal
                    open={open}
                    size='small'
                    title={(
                        <span id='channel-artifact-save-title'>
                            {updating ? (
                                <FormattedMessage defaultMessage='Update channel artifact'/>
                            ) : (
                                <FormattedMessage defaultMessage='Save to channel artifacts'/>
                            )}
                        </span>
                    )}
                    onClose={() => !saving && setOpen(false)}
                    closeOnEscape={false}
                    closeLabel={intl.formatMessage({defaultMessage: 'Close'})}
                    headerDivider={false}
                    footerDivider={false}
                >
                    <div data-testid='channel-artifact-save-modal'>
                        <ModalCopy>
                            {updating ? (
                                <FormattedMessage defaultMessage="This adds a new version to the saved artifact. Older versions stay available up to the server's version limit, for as long as the messages they came from exist."/>
                            ) : (
                                <FormattedMessage defaultMessage='Save this HTML app to the channel so everyone can open it later. The channel keeps a link to this message, not a copy, so the app is available only while this message exists. You can also pin it to channel bookmarks below.'/>
                            )}
                        </ModalCopy>
                        <FieldLabel htmlFor='channel-artifact-title'>
                            <FormattedMessage defaultMessage='Title'/>
                        </FieldLabel>
                        <FieldInput
                            id='channel-artifact-title'
                            value={title}
                            maxLength={64}
                            onChange={(e) => setTitle(e.target.value)}
                            data-testid='channel-artifact-title-input'
                            autoFocus={true}
                        />
                        {existing.length > 0 && library.canEdit && (
                            <>
                                <FieldLabel htmlFor='channel-artifact-replace'>
                                    <FormattedMessage defaultMessage='Library entry'/>
                                </FieldLabel>
                                <FieldSelect
                                    id='channel-artifact-replace'
                                    value={replaceId}
                                    onChange={(e) => {
                                        const id = e.target.value;
                                        setReplaceId(id);
                                        setAckUpdate(false);
                                        const match = existing.find((entry) => entry.id === id);
                                        if (match) {
                                            setTitle(match.title);
                                        }
                                    }}
                                    data-testid='channel-artifact-replace-select'
                                >
                                    <option value=''>
                                        {intl.formatMessage({defaultMessage: 'Create a new artifact'})}
                                    </option>
                                    {existing.map((entry) => (
                                        <option
                                            key={entry.id}
                                            value={entry.id}
                                        >
                                            {entry.title}
                                            {entry.version > 1 ? ` (v${entry.version})` : ''}
                                        </option>
                                    ))}
                                </FieldSelect>
                                <FieldHint>
                                    <FormattedMessage defaultMessage='Pick an existing entry to add a new version to it. Leave on “Create a new artifact” to save a separate one.'/>
                                </FieldHint>
                            </>
                        )}
                        {updating && replaceEntry && (
                            <SupersedeBox data-testid='channel-artifact-supersede-ack'>
                                <SupersedeTitle>
                                    <FormattedMessage defaultMessage='This will supersede'/>
                                </SupersedeTitle>
                                <SupersedeMeta>
                                    <strong>{replaceEntry.title}</strong>
                                    {' · '}
                                    {replaceEntry.source_agent_display_name ? `${replaceEntry.source_agent_display_name} · ` : ''}
                                    {new Date(replaceEntry.update_at || replaceEntry.create_at).toLocaleString()}
                                    {replaceEntry.sha256 ? ` · ${shaPrefix(replaceEntry.sha256)}` : ''}
                                </SupersedeMeta>
                                <Checkbox
                                    checked={ackUpdate}
                                    onChange={(e) => setAckUpdate(e.target.checked)}
                                    data-testid='channel-artifact-update-ack'
                                >
                                    <FormattedMessage defaultMessage='I understand this replaces the current version (older versions stay available up to the version limit)'/>
                                </Checkbox>
                            </SupersedeBox>
                        )}
                        {canPin && (
                            <PinRow>
                                <Checkbox
                                    checked={pinBookmark}
                                    onChange={(e) => setPinBookmark(e.target.checked)}
                                    data-testid='channel-artifact-pin-bookmark'
                                >
                                    <FormattedMessage defaultMessage='Pin to channel bookmarks'/>
                                </Checkbox>
                            </PinRow>
                        )}
                        {error && <ErrorText>{error}</ErrorText>}
                        <ModalActions>
                            <Button
                                emphasis='tertiary'
                                size='small'
                                onClick={() => setOpen(false)}
                                disabled={saving}
                            >
                                <FormattedMessage defaultMessage='Cancel'/>
                            </Button>
                            <Button
                                emphasis='primary'
                                size='small'
                                onClick={onSave}
                                disabled={saving || !canSubmit}
                                data-testid='channel-artifact-save-confirm'
                            >
                                {updating ? (
                                    <FormattedMessage defaultMessage='Update artifact'/>
                                ) : (
                                    <FormattedMessage defaultMessage='Save artifact'/>
                                )}
                            </Button>
                        </ModalActions>
                    </div>
                </Modal>
            </AnimatedModalShell>
        </Wrap>
    );
};

export default SaveToChannelButton;

const Wrap = styled.span`
    display: inline-flex;
    align-items: center;
`;

const ModalCopy = styled.p`
    margin: 0 0 var(--spacing-md, 16px);
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 14px;
    line-height: 20px;
`;

const FieldLabel = styled.label`
    display: block;
    margin: 0 0 var(--spacing-xxs, 4px);
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 12px;
    font-weight: 600;
`;

const FieldHint = styled.p`
    margin: calc(var(--spacing-xxs, 4px) * -1) 0 var(--spacing-sm, 12px);
    color: rgba(var(--center-channel-color-rgb), 0.56);
    font-size: 12px;
    line-height: 16px;
`;

const FieldInput = styled.input`
    width: 100%;
    margin-bottom: var(--spacing-sm, 12px);
    padding: var(--spacing-xs, 8px) var(--spacing-sm, 12px);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.24);
    border-radius: 4px;
    background: var(--center-channel-bg);
    color: var(--center-channel-color);
    font-size: 14px;
    line-height: 20px;
`;

const FieldSelect = styled.select`
    width: 100%;
    margin-bottom: var(--spacing-sm, 12px);
    padding: var(--spacing-xs, 8px) var(--spacing-sm, 12px);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.24);
    border-radius: 4px;
    background: var(--center-channel-bg);
    color: var(--center-channel-color);
    font-size: 14px;
    line-height: 20px;
`;

const SupersedeBox = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xs, 8px);
    margin-bottom: var(--spacing-sm, 12px);
    padding: var(--spacing-sm, 12px);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 8px;
    background: rgba(var(--center-channel-color-rgb), 0.04);
`;

const SupersedeTitle = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 12px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.02em;
`;

const SupersedeMeta = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 13px;
    line-height: 18px;
`;

const PinRow = styled.div`
    margin-bottom: var(--spacing-sm, 12px);
`;

const ModalActions = styled.div`
    display: flex;
    justify-content: flex-end;
    gap: var(--spacing-xs, 8px);
    margin-top: var(--spacing-xs, 8px);
`;

const ErrorText = styled.div`
    margin-bottom: var(--spacing-xs, 8px);
    color: var(--error-text);
    font-size: 13px;
    line-height: 18px;
`;
