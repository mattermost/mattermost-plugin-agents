// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {useSelector, useStore} from 'react-redux';
import styled from 'styled-components';

import {GlobalState} from '@mattermost/types/store';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {Button} from '@mattermost/compass-ui/components/button';
import {Checkbox} from '@mattermost/compass-ui/components/checkbox';
import {PinIcon, PinOutlineIcon} from '@mattermost/compass-icons/components';

import {
    ChannelArtifactEntry,
    createChannelArtifact,
    listChannelArtifacts,
} from '@/client';
import {getChannelArtifactsEnabled} from '@/selectors';

import {canAddChannelBookmarks, pinLibraryArtifactBookmark} from './bookmark';

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
    rootPostId: string,
): ChannelArtifactEntry | null {
    const byFile = entries.find((e) => e.source_file_id === fileId);
    if (byFile) {
        return byFile;
    }
    if (!rootPostId) {
        return null;
    }
    return entries.find((e) => e.source_root_post_id === rootPostId) || null;
}

const SaveToChannelButton = ({fileId, fileName, postId, channelId}: Props) => {
    const intl = useIntl();
    const store = useStore<GlobalState>();
    const enabled = useSelector(getChannelArtifactsEnabled);
    const rootPostId = useSelector<GlobalState, string>((state) => {
        const post = state.entities.posts?.posts?.[postId];
        return (post?.root_id || postId) || '';
    });
    const canPin = useSelector<GlobalState, boolean>((state) => canAddChannelBookmarks(state, channelId));
    const [open, setOpen] = useState(false);
    const [canManage, setCanManage] = useState(false);
    const [existing, setExisting] = useState<ChannelArtifactEntry[]>([]);
    const [title, setTitle] = useState('');
    const [replaceId, setReplaceId] = useState('');
    const [ackUpdate, setAckUpdate] = useState(false);
    const [pinBookmark, setPinBookmark] = useState(false);
    const [saving, setSaving] = useState(false);
    const [savedId, setSavedId] = useState<string | null>(null);
    const [error, setError] = useState('');

    useEffect(() => {
        if (!enabled || !channelId) {
            return undefined; // eslint-disable-line no-undefined
        }
        let cancelled = false;
        listChannelArtifacts(channelId).then((resp) => {
            if (cancelled) {
                return;
            }
            setCanManage(Boolean(resp.can_manage));
            const entries = resp.entries || [];
            setExisting(entries);
            const match = pickReplaceTarget(entries, fileId, rootPostId || postId);
            if (match) {
                setSavedId(match.id);
            }
        }, () => {
            if (!cancelled) {
                setCanManage(false);
            }
        });
        return () => {
            cancelled = true;
        };
    }, [enabled, channelId, fileId, postId, rootPostId]);

    if (!enabled || !channelId || !postId || !canManage) {
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
        const nextTitle = defaultTitle();
        setError('');
        setAckUpdate(false);
        setPinBookmark(false);

        const target = pickReplaceTarget(existing, fileId, rootPostId || postId);
        if (target) {
            setReplaceId(target.id);
            setTitle(target.title || nextTitle);
        } else {
            setReplaceId('');
            setTitle(nextTitle);
        }
        setOpen(true);
    };

    const onSave = async () => {
        setSaving(true);
        setError('');
        try {
            const entry = await createChannelArtifact({
                channel_id: channelId,
                post_id: postId,
                file_id: fileId,
                title: title.trim() || defaultTitle(),
                ...(replaceId ? {replace_id: replaceId} : {}),
            });
            setSavedId(entry.id);
            if (pinBookmark) {
                await pinLibraryArtifactBookmark(store.getState(), channelId, entry.id, entry.title, store.dispatch);
            }
            setOpen(false);
        } catch (err: any) {
            if (typeof err?.message === 'string' && err.message.startsWith('duplicate:')) {
                setSavedId(err.message.slice('duplicate:'.length));
                setOpen(false);
            } else if (err?.status_code === 403 || err?.status_code === 404) {
                setError(intl.formatMessage({defaultMessage: 'Only channel admins can save artifacts.'}));
            } else {
                setError(intl.formatMessage({defaultMessage: 'Could not save artifact.'}));
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
            {open && (
                <ModalBackdrop
                    data-testid='channel-artifact-save-modal'
                    onClick={() => !saving && setOpen(false)}
                    onKeyDown={(e) => {
                        if (e.key === 'Escape' && !saving) {
                            setOpen(false);
                        }
                    }}
                    role='presentation'
                >
                    {/* eslint-disable-next-line agents/click-events-have-key-events, agents/no-noninteractive-element-interactions -- only stops clicks inside the dialog from reaching the backdrop */}
                    <ModalCard
                        role='dialog'
                        aria-modal='true'
                        aria-labelledby='channel-artifact-save-title'
                        onClick={(e) => e.stopPropagation()}
                    >
                        <ModalHeading id='channel-artifact-save-title'>
                            {updating ? (
                                <FormattedMessage defaultMessage='Update channel artifact'/>
                            ) : (
                                <FormattedMessage defaultMessage='Save to channel artifacts'/>
                            )}
                        </ModalHeading>
                        <ModalCopy>
                            {updating ? (
                                <FormattedMessage defaultMessage='This creates a new version of the library copy. Prior versions stay recoverable from the channel library.'/>
                            ) : (
                                <FormattedMessage defaultMessage='Keep this HTML app in the channel library so everyone can open it later. Optionally pin it to channel bookmarks below, or add it later from Bookmarks → Add channel artifact. Channel admins can rename or replace it anytime.'/>
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
                        {existing.length > 0 && (
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
                                            {entry.version && entry.version > 1 ? ` (v${entry.version})` : ''}
                                        </option>
                                    ))}
                                </FieldSelect>
                                <FieldHint>
                                    <FormattedMessage defaultMessage='Pick an existing entry to refresh it in place (same id, version bumps). Leave on “Create a new artifact” to add another.'/>
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
                                    {replaceEntry.source_agent_display_name || replaceEntry.updated_by || 'Agent'}
                                    {' · '}
                                    {new Date(replaceEntry.update_at || replaceEntry.create_at).toLocaleString()}
                                    {replaceEntry.sha256 ? ` · ${shaPrefix(replaceEntry.sha256)}` : ''}
                                </SupersedeMeta>
                                <Checkbox
                                    checked={ackUpdate}
                                    onChange={(e) => setAckUpdate(e.target.checked)}
                                    data-testid='channel-artifact-update-ack'
                                >
                                    <FormattedMessage defaultMessage='I understand this supersedes the current library copy (prior versions stay recoverable)'/>
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
                    </ModalCard>
                </ModalBackdrop>
            )}
        </Wrap>
    );
};

export default SaveToChannelButton;

const Wrap = styled.span`
    display: inline-flex;
    align-items: center;
`;

const ModalBackdrop = styled.div`
    position: fixed;
    inset: 0;
    z-index: 11000;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: var(--spacing-xl, 24px);
    background: rgba(0, 0, 0, 0.48);
`;

const ModalCard = styled.div`
    width: min(440px, 100%);
    padding: var(--spacing-xl, 24px);
    border-radius: 12px;
    background: var(--center-channel-bg);
    color: var(--center-channel-color);
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.24);
`;

const ModalHeading = styled.h2`
    margin: 0 0 var(--spacing-xs, 8px);
    font-size: 18px;
    font-weight: 600;
    line-height: 24px;
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
