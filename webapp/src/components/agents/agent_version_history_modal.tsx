// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useRef, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl, type IntlShape} from 'react-intl';

import {CheckboxMultipleMarkedOutlineIcon, CloseIcon, DotsHorizontalIcon, RestoreIcon, TrashCanOutlineIcon} from '@mattermost/compass-icons/components';

import {Button} from '@mattermost/compass-ui/components/button';
import {Checkbox} from '@mattermost/compass-ui/components/checkbox';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {MenuItem} from '@mattermost/compass-ui/components/menu-item';
import {Modal} from '@mattermost/compass-ui/components/modal';
import {Scrollbar} from '@mattermost/compass-ui/components/scrollbar';
import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';
import {Tag} from '@mattermost/compass-ui/components/tag';

import {deleteAgentVersions, getAgentVersion, restoreAgentVersion} from '@/client';
import {AgentVersionDetail, ServiceInfo, UserAgent} from '@/types/agents';
import {AnimatedModalShell, MODAL_SHEET_CLASS} from '@/components/animated_modal_shell';
import ConfirmationDialog from '@/components/confirmation_dialog';
import DotMenu, {DropdownMenuItem} from '@/components/dot_menu';
import {TooltipAboveModals} from '@/components/dropdown';
import {UserAccessLevel} from '@/components/system_console/bot';

import {changedFieldLabels, differingConfigFields, formatRelativeTime, versionSourceLabel} from './agent_version_labels';
import {AgentVersionHistory, errorMessage, hasVersions} from './use_agent_version_history';
import VersionSnapshot from './version_snapshot';

const RESTORE_DIALOG_TITLE_ID = 'restore-agent-version-title';
const DELETE_DIALOG_TITLE_ID = 'delete-agent-versions-title';

// The versions a confirmed delete removes: listed ones, or every previous one.
type PendingDelete = number[] | 'all';

type Props = {
    show: boolean;
    onClose: () => void;
    agentId: string;

    /** Shown beside the modal title. */
    agentName: string;
    services: ServiceInfo[];
    history: AgentVersionHistory;

    /** True when the editor has unsaved changes that a restore would discard. */
    isDirty: boolean;

    /** The saved agent's user access level, which a restore replaces. */
    currentUserAccessLevel: UserAccessLevel;

    /** Called with the agent returned by the restore endpoint. */
    onRestored: (agent: UserAgent) => void;
}

function formatTimestamp(intl: IntlShape, timestamp: number): string {
    return intl.formatDate(timestamp, {
        year: 'numeric',
        month: 'short',
        day: 'numeric',
        hour: 'numeric',
        minute: '2-digit',
    });
}

const AgentVersionHistoryModal = ({show, onClose, agentId, agentName, services, history, isDirty, currentUserAccessLevel, onRestored}: Props) => {
    const intl = useIntl();
    const {list, listError, loadingOlder, olderError, refresh, loadOlder, authorLabel} = history;

    // Effects below must not re-run when a new intl object is created.
    const intlRef = useRef(intl);
    intlRef.current = intl;

    const [selected, setSelected] = useState<number | null>(null);
    const [detail, setDetail] = useState<AgentVersionDetail | null>(null);
    const [detailError, setDetailError] = useState('');
    const [currentDetail, setCurrentDetail] = useState<AgentVersionDetail | null>(null);
    const [confirmOpen, setConfirmOpen] = useState(false);
    const [restoreRequested, setRestoreRequested] = useState<number | null>(null);
    const [restoring, setRestoring] = useState(false);
    const [restoreError, setRestoreError] = useState('');
    const [restoredNotice, setRestoredNotice] = useState('');
    const [pendingDelete, setPendingDelete] = useState<PendingDelete | null>(null);
    const [deleting, setDeleting] = useState(false);
    const [deleteError, setDeleteError] = useState('');
    const [deletedNotice, setDeletedNotice] = useState('');
    const [menuVersion, setMenuVersion] = useState<number | null>(null);
    const [selecting, setSelecting] = useState(false);
    const [checkedVersions, setCheckedVersions] = useState<number[]>([]);
    const mountedRef = useRef(true);

    useEffect(() => {
        mountedRef.current = true;
        return () => {
            mountedRef.current = false;
        };
    }, []);

    const currentVersion = hasVersions(list) ? list.currentVersion : null;

    // Each time the modal opens it starts on the current version.
    useEffect(() => {
        if (!show) {
            return;
        }
        setSelected(currentVersion);
        setRestoreError('');
        setRestoredNotice('');
        setDeleteError('');
        setDeletedNotice('');
        setRestoreRequested(null);
        setSelecting(false);
        setCheckedVersions([]);

        // Only re-run on open: later list changes are handled below.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [show]);

    // Keep the selection on a version that still exists (e.g. after a delete).
    useEffect(() => {
        if (!hasVersions(list)) {
            setSelected(null);
            return;
        }
        setSelected((prev) => (prev !== null && list.versions.some((v) => v.version === prev) ? prev : list.currentVersion));
    }, [list]);

    useEffect(() => {
        if (!show || selected === null) {
            return () => {
                // Nothing to load
            };
        }
        let cancelled = false;
        setDetail(null);
        setDetailError('');
        getAgentVersion(agentId, selected).then((result) => {
            if (!cancelled) {
                setDetail(result);
            }
        }).catch((e) => {
            if (!cancelled) {
                setDetailError(errorMessage(e, intlRef.current.formatMessage({defaultMessage: 'Failed to load this version.'})));
                setRestoreRequested(null);
            }
        });
        return () => {
            cancelled = true;
        };
    }, [show, agentId, selected]);

    // A restore requested from a row menu waits for that version to load, so
    // the dialog can say what the restore changes.
    useEffect(() => {
        if (restoreRequested !== null && detail?.version === restoreRequested) {
            setRestoreRequested(null);
            setConfirmOpen(true);
        }
    }, [restoreRequested, detail]);

    // The restore dialog compares the selected version with the current one.
    useEffect(() => {
        if (!confirmOpen || currentVersion === null) {
            return () => {
                // Nothing to compare
            };
        }
        let cancelled = false;
        setCurrentDetail(null);
        getAgentVersion(agentId, currentVersion).then((result) => {
            if (!cancelled) {
                setCurrentDetail(result);
            }
        }).catch(() => {
            // The dialog falls back to the generic message.
        });
        return () => {
            cancelled = true;
        };
    }, [confirmOpen, agentId, currentVersion]);

    const clearNotices = () => {
        setRestoreError('');
        setRestoredNotice('');
        setDeleteError('');
        setDeletedNotice('');
    };

    const toggleChecked = (version: number) => {
        setCheckedVersions((prev) => (prev.includes(version) ? prev.filter((v) => v !== version) : [...prev, version]));
    };

    const stopSelecting = () => {
        setSelecting(false);
        setCheckedVersions([]);
    };

    // Escape backs out of select-multiple mode first and closes the modal on the next press.
    const handleEscape = () => {
        if (selecting) {
            stopSelecting();
            return;
        }
        onClose();
    };

    const handleSelect = (version: number) => {
        setSelected(version);
        setRestoreRequested(null);
        clearNotices();
    };

    const requestRestore = (version: number) => {
        clearNotices();
        if (version === selected && detail?.version === version) {
            setConfirmOpen(true);
            return;
        }
        setRestoreRequested(version);
        setSelected(version);
    };

    const handleDeleteConfirm = async () => {
        if (pendingDelete === null || deleting) {
            return;
        }
        setDeleting(true);
        setDeleteError('');
        try {
            const deleted = await deleteAgentVersions(agentId, pendingDelete);
            if (!mountedRef.current) {
                return;
            }
            setPendingDelete(null);
            setSelecting(false);
            setCheckedVersions([]);
            setRestoredNotice('');
            setDeletedNotice(intl.formatMessage(
                {defaultMessage: 'Deleted {count, plural, one {# version} other {# versions}}.'},
                {count: deleted},
            ));
            await refresh();
        } catch (e) {
            if (mountedRef.current) {
                setPendingDelete(null);
                setDeleteError(errorMessage(e, intl.formatMessage({defaultMessage: 'Failed to delete versions. Please try again.'})));
            }
        } finally {
            if (mountedRef.current) {
                setDeleting(false);
            }
        }
    };

    const handleRestoreConfirm = async () => {
        if (selected === null || restoring) {
            return;
        }
        const restoredVersion = selected;
        setRestoring(true);
        setRestoreError('');
        try {
            const agent = await restoreAgentVersion(agentId, restoredVersion);
            if (!mountedRef.current) {
                return;
            }
            setConfirmOpen(false);
            setRestoredNotice(intl.formatMessage(
                {defaultMessage: 'Version {version} was restored as the current version.'},
                {version: restoredVersion},
            ));
            setDeletedNotice('');
            onRestored(agent);
            const next = await refresh();
            if (next && mountedRef.current) {
                setSelected(next.currentVersion);
            }
        } catch (e) {
            if (mountedRef.current) {
                setConfirmOpen(false);
                setRestoreError(errorMessage(e, intl.formatMessage({defaultMessage: 'Failed to restore this version. Please try again.'})));
            }
        } finally {
            if (mountedRef.current) {
                setRestoring(false);
            }
        }
    };

    const handleMenuOpenChange = useCallback((version: number, open: boolean) => {
        setMenuVersion((prev) => {
            if (open) {
                return version;
            }
            return prev === version ? null : prev;
        });
    }, []);

    const hasPreviousVersions = Boolean(list && (list.hasMore || list.versions.some((v) => v.version !== list.currentVersion)));

    const renderBody = () => {
        // The editor only offers the modal once a version exists.
        if (!hasVersions(list)) {
            return null;
        }

        const selectedSummary = list.versions.find((v) => v.version === selected);
        const isCurrentSelected = selected !== null && selected === list.currentVersion;

        return (
            <Layout>
                <ListColumn>
                    <ScrollArea>
                        <ListContent>
                            <ListToolbar>
                                {selecting ? (
                                    <>
                                        <ListTitle role='status'>
                                            {intl.formatMessage({defaultMessage: '{count} selected'}, {count: checkedVersions.length})}
                                        </ListTitle>
                                        <ToolbarButtons>
                                            <TooltipAboveModals title={intl.formatMessage({defaultMessage: 'Cancel'})}>
                                                <IconButton
                                                    icon={<Icon glyph={<CloseIcon/>}/>}
                                                    size='small'
                                                    onClick={stopSelecting}
                                                    aria-label={intl.formatMessage({defaultMessage: 'Cancel'})}
                                                />
                                            </TooltipAboveModals>
                                            <TooltipAboveModals title={intl.formatMessage({defaultMessage: 'Delete selected'})}>
                                                <IconButton
                                                    icon={<Icon glyph={<TrashCanOutlineIcon/>}/>}
                                                    size='small'
                                                    destructive={true}
                                                    disabled={checkedVersions.length === 0 || deleting}
                                                    onClick={() => setPendingDelete([...checkedVersions].sort((a, b) => a - b))}
                                                    aria-label={intl.formatMessage({defaultMessage: 'Delete selected'})}
                                                />
                                            </TooltipAboveModals>
                                        </ToolbarButtons>
                                    </>
                                ) : (
                                    <>
                                        <ListTitle>
                                            {list.hasMore ? intl.formatMessage(
                                                {defaultMessage: '{count}+ versions'},
                                                {count: list.versions.length},
                                            ) : intl.formatMessage(
                                                {defaultMessage: '{count, plural, one {# version} other {# versions}}'},
                                                {count: list.versions.length},
                                            )}
                                        </ListTitle>
                                        {hasPreviousVersions && (
                                            <DotMenu
                                                icon={<DotsHorizontalIcon/>}
                                                title={intl.formatMessage({defaultMessage: 'More version history actions'})}
                                                placement='bottom-end'
                                            >
                                                <DropdownMenuItem
                                                    icon={<CheckboxMultipleMarkedOutlineIcon/>}
                                                    label={<FormattedMessage defaultMessage='Select multiple versions'/>}
                                                    disabled={deleting || restoring}
                                                    onClick={() => setSelecting(true)}
                                                />
                                                <DropdownMenuItem
                                                    icon={<TrashCanOutlineIcon/>}
                                                    label={<FormattedMessage defaultMessage='Delete all previous versions'/>}
                                                    destructive={true}
                                                    disabled={deleting || restoring}
                                                    onClick={() => setPendingDelete('all')}
                                                />
                                            </DotMenu>
                                        )}
                                    </>
                                )}
                            </ListToolbar>
                            <VersionList
                                aria-label={intl.formatMessage({defaultMessage: 'Version history'})}
                                data-testid='version-list'
                            >
                                {list.versions.map((v) => {
                                    const isCurrent = v.version === list.currentVersion;
                                    return (
                                        <VersionRow key={v.version}>
                                            <VersionMenuItem
                                                label={(
                                                    <VersionLabel>
                                                        {intl.formatMessage({defaultMessage: 'Version {version}'}, {version: v.version})}
                                                        {isCurrent && <CurrentTag/>}
                                                    </VersionLabel>
                                                )}
                                                secondaryLabel={intl.formatMessage(
                                                    {defaultMessage: 'by {author} · {time}'},
                                                    {author: authorLabel(v.createdBy), time: formatRelativeTime(intl, v.createAt)},
                                                )}
                                                leadingElement={false}
                                                active={v.version === selected}
                                                $reserveMenu={!isCurrent && !selecting}
                                                $reserveCheckbox={selecting}
                                                title={formatTimestamp(intl, v.createAt)}
                                                {...(v.version === selected ? {'aria-current': 'true' as const} : {})}
                                                onClick={() => handleSelect(v.version)}
                                            />
                                            {selecting && !isCurrent && (
                                                <CheckboxSlot>
                                                    <Checkbox
                                                        checked={checkedVersions.includes(v.version)}
                                                        onChange={() => toggleChecked(v.version)}
                                                        aria-label={intl.formatMessage({defaultMessage: 'Select version {version}'}, {version: v.version})}
                                                    />
                                                </CheckboxSlot>
                                            )}
                                            {!isCurrent && !selecting && (
                                                <RowMenu $visible={menuVersion === v.version}>
                                                    <DotMenu
                                                        icon={<DotsHorizontalIcon/>}
                                                        title={intl.formatMessage({defaultMessage: 'Actions for version {version}'}, {version: v.version})}
                                                        placement='bottom-end'
                                                        onOpenChange={(open) => handleMenuOpenChange(v.version, open)}
                                                    >
                                                        <DropdownMenuItem
                                                            icon={<RestoreIcon/>}
                                                            label={<FormattedMessage defaultMessage='Restore this version'/>}
                                                            disabled={restoring || deleting}
                                                            onClick={() => requestRestore(v.version)}
                                                        />
                                                        <DropdownMenuItem
                                                            icon={<TrashCanOutlineIcon/>}
                                                            label={<FormattedMessage defaultMessage='Delete this version'/>}
                                                            destructive={true}
                                                            disabled={restoring || deleting}
                                                            onClick={() => setPendingDelete([v.version])}
                                                        />
                                                    </DotMenu>
                                                </RowMenu>
                                            )}
                                        </VersionRow>
                                    );
                                })}
                            </VersionList>
                            {(list.hasMore || olderError) && (
                                <ListFooter>
                                    {list.hasMore && (
                                        <Button
                                            emphasis='tertiary'
                                            loading={loadingOlder}
                                            onClick={loadOlder}
                                        >
                                            <FormattedMessage defaultMessage='Load older versions'/>
                                        </Button>
                                    )}
                                    {olderError && (
                                        <SectionNotice
                                            type='danger'
                                            title={olderError}
                                            role='alert'
                                        />
                                    )}
                                </ListFooter>
                            )}
                        </ListContent>
                    </ScrollArea>
                </ListColumn>

                <DetailPane>
                    <ScrollArea>
                        <DetailContent>
                            {[restoredNotice, deletedNotice].filter(Boolean).map((message) => (
                                <SectionNotice
                                    key={message}
                                    type='success'
                                    title={message}
                                    role='status'
                                />
                            ))}
                            {[restoreError, deleteError, listError, detailError].filter(Boolean).map((message) => (
                                <SectionNotice
                                    key={message}
                                    type='danger'
                                    title={message}
                                    role='alert'
                                />
                            ))}

                            {selectedSummary && (
                                <DetailHeader>
                                    <div>
                                        <DetailTitle>
                                            {intl.formatMessage({defaultMessage: 'Version {version}'}, {version: selectedSummary.version})}
                                            {isCurrentSelected && <CurrentTag/>}
                                        </DetailTitle>
                                        <VersionMeta>
                                            {[
                                                selectedSummary.source === 'create' ? '' : versionSourceLabel(intl, selectedSummary),
                                                intl.formatMessage(
                                                    {defaultMessage: 'Created by {author}'},
                                                    {author: authorLabel(selectedSummary.createdBy)},
                                                ),
                                                formatTimestamp(intl, selectedSummary.createAt),
                                            ].filter(Boolean).join(' · ')}
                                        </VersionMeta>
                                    </div>
                                    {!isCurrentSelected && (
                                        <Button
                                            emphasis='primary'
                                            size='small'
                                            leadingIcon={<Icon glyph={<RestoreIcon/>}/>}
                                            disabled={!detail || restoring || deleting}
                                            onClick={() => requestRestore(selectedSummary.version)}
                                        >
                                            <FormattedMessage defaultMessage='Restore this version'/>
                                        </Button>
                                    )}
                                </DetailHeader>
                            )}

                            {!detail && !detailError && (
                                <Muted>
                                    <FormattedMessage defaultMessage='Loading version...'/>
                                </Muted>
                            )}
                            {detail && (
                                <VersionSnapshot
                                    config={detail.config ?? {}}
                                    changedFields={detail.changedFields}
                                    services={services}
                                />
                            )}
                        </DetailContent>
                    </ScrollArea>
                </DetailPane>
            </Layout>
        );
    };

    const restoresRemovedPolicy = detail?.config?.userAccessLevel === UserAccessLevel.AttributeBased &&
        currentUserAccessLevel !== UserAccessLevel.AttributeBased;
    const restoredChanges = detail && currentDetail ? changedFieldLabels(intl, differingConfigFields(
        detail.config ?? {},
        currentDetail.config ?? {},
    )) : null;
    const deleteCount = pendingDelete === 'all' ? 0 : pendingDelete?.length ?? 0;
    const deleteVersions = pendingDelete === 'all' ? '' : intl.formatList((pendingDelete ?? []).map(String), {type: 'conjunction'});

    return (
        <>
            <AnimatedModalShell
                show={show}
                onBackdropClick={onClose}
                onEscape={handleEscape}
                zIndex={2000}
            >
                <Sheet className={MODAL_SHEET_CLASS}>
                    <HistoryModal
                        open={show}
                        closeOnEscape={false}
                        size='medium'
                        title={<FormattedMessage defaultMessage='Version history'/>}
                        subtitle={agentName}
                        subtitlePlacement='beside'
                        onClose={onClose}
                        closeLabel={intl.formatMessage({defaultMessage: 'Close'})}
                        closeButtonProps={{title: intl.formatMessage({defaultMessage: 'Close'})}}
                        headerDivider={true}
                        bodyPadding='none'
                        scrollable={false}
                    >
                        <ModalBody>
                            {renderBody()}
                        </ModalBody>
                    </HistoryModal>
                </Sheet>
            </AnimatedModalShell>
            <ConfirmationDialog
                show={confirmOpen}
                titleId={RESTORE_DIALOG_TITLE_ID}
                title={<FormattedMessage defaultMessage='Restore this version?'/>}
                message={(
                    <>
                        <p>
                            {intl.formatMessage(
                                {defaultMessage: 'Version {version} will be saved as a new version and the agent will start using it right away. The existing history is kept.'},
                                {version: selected ?? 0},
                            )}
                        </p>
                        {restoredChanges !== null && restoredChanges.length > 0 && (
                            <>
                                <p>
                                    <FormattedMessage defaultMessage='Compared with the current version, this changes:'/>
                                </p>
                                <ChangeList data-testid='restore-changes'>
                                    {restoredChanges.map((label) => (
                                        <li key={label}>{label}</li>
                                    ))}
                                </ChangeList>
                            </>
                        )}
                        {restoredChanges !== null && restoredChanges.length === 0 && (
                            <p>
                                <FormattedMessage defaultMessage='Its settings are the same as the current version.'/>
                            </p>
                        )}
                        {restoresRemovedPolicy && (
                            <WarningText role='alert'>
                                <FormattedMessage defaultMessage='This version uses attribute-based access. Its access policy was removed when access was changed, so nobody will be able to use the agent until you create a new policy on the Access tab.'/>
                            </WarningText>
                        )}
                        {isDirty && (
                            <WarningText role='alert'>
                                <FormattedMessage defaultMessage='You have unsaved changes in the editor. Restoring will discard them.'/>
                            </WarningText>
                        )}
                    </>
                )}
                confirmButtonText={restoring ? <FormattedMessage defaultMessage='Restoring...'/> : <FormattedMessage defaultMessage='Restore'/>}
                onConfirm={handleRestoreConfirm}
                onCancel={() => setConfirmOpen(false)}
                confirmPending={restoring}
                managedAccessibility={true}
                zIndex={2100}
            />
            <ConfirmationDialog
                show={pendingDelete !== null}
                titleId={DELETE_DIALOG_TITLE_ID}
                title={pendingDelete === 'all' ? (
                    <FormattedMessage defaultMessage='Delete all previous versions?'/>
                ) : intl.formatMessage(
                    {defaultMessage: '{count, plural, one {Delete version {versions}?} other {Delete # versions?}}'},
                    {count: deleteCount, versions: deleteVersions},
                )}
                message={pendingDelete === 'all' ? (
                    <FormattedMessage defaultMessage='Every version except the current one will be permanently deleted. The agent keeps its current configuration. This cannot be undone.'/>
                ) : intl.formatMessage(
                    {defaultMessage: '{count, plural, one {Version {versions}} other {Versions {versions}}} will be permanently deleted from the history. The agent keeps its current configuration. This cannot be undone.'},
                    {count: deleteCount, versions: deleteVersions},
                )}
                confirmButtonText={deleting ? <FormattedMessage defaultMessage='Deleting...'/> : <FormattedMessage defaultMessage='Delete'/>}
                onConfirm={handleDeleteConfirm}
                onCancel={() => setPendingDelete(null)}
                confirmPending={deleting}
                isDestructive={true}
                managedAccessibility={true}
                zIndex={2100}
            />
        </>
    );
};

const Sheet = styled.div`
    display: flex;
    max-width: calc(100vw - 32px);
`;

const HistoryModal = styled(Modal)`
    && {
        width: 880px;
        max-width: 100%;
        height: 80vh;
    }
`;

const ModalBody = styled.div`
    display: flex;
    flex-direction: column;
    height: 100%;
    min-height: 0;
`;

const Layout = styled.div`
    display: flex;
    flex: 1;
    min-height: 0;
`;

const ListColumn = styled.div`
    position: relative;
    flex: 0 0 300px;
    min-height: 0;
    background: rgba(var(--center-channel-color-rgb), 0.04);
`;

// Fills its pane and scrolls inside it; the scrollbar only appears on hover.
const ScrollArea = styled(Scrollbar)`
    && {
        position: absolute;
        top: 0;
        right: 0;
        bottom: 0;
        left: 0;
        width: auto;
        height: auto;
    }
`;

const ListContent = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xs);
    padding: var(--spacing-s) var(--spacing-xxl);
`;

const ListFooter = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-m);
    margin-top: var(--spacing-xxxs);
`;

const VersionList = styled.ol`
    list-style: none;
    margin: 0 calc(-1 * var(--spacing-m));
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xxxxs);
`;

const VersionLabel = styled.span`
    display: inline-flex;
    align-items: center;
    gap: var(--spacing-xs);
`;

// Room on the right so a row's text never runs under its menu button.
const VersionMenuItem = styled(MenuItem)<{$reserveMenu: boolean; $reserveCheckbox: boolean}>`
    && {
        background-color: transparent;
        padding-top: var(--spacing-xs);
        padding-bottom: var(--spacing-xs);
        padding-left: ${(p) => (p.$reserveCheckbox ? 'var(--spacing-xxxxl)' : 'var(--spacing-l)')};
        padding-right: ${(p) => (p.$reserveMenu ? 'calc(var(--spacing-xxxxl) + var(--spacing-m))' : 'var(--spacing-l)')};
        border-radius: 4px;
    }
`;

// The row's menu button sits over the menu item rather than inside it, so
// hovering the button would drop the item's own :hover. The row owns the tint
// instead, which keeps it while the pointer is anywhere on the row.
const VersionRow = styled.li`
    position: relative;

    &:hover ${VersionMenuItem}:not(:disabled) {
        background-color: rgba(var(--center-channel-color-rgb), 0.08);
    }

    &:hover ${VersionMenuItem}[aria-current='true']:not(:disabled) {
        background-color: rgba(var(--button-bg-rgb), 0.12);
    }
`;

// Spans the same width as the version list (which bleeds past the sidebar's
// padding) and insets the right edge like a row's menu button, so the header
// buttons line up horizontally with the row buttons beneath them.
const ListToolbar = styled.div`
    display: flex;
    margin: 0 calc(-1 * var(--spacing-m));
    padding: 0 calc(var(--spacing-xxs) + var(--spacing-xs)) 0 var(--spacing-m);
    align-items: center;
    justify-content: space-between;
    gap: var(--spacing-xs);
    min-height: 32px;
`;

const ToolbarButtons = styled.div`
    display: flex;
    gap: var(--spacing-xs);
`;

const ListTitle = styled.span`
    padding-left: var(--spacing-xs);
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    color: var(--center-channel-color);
`;

const CheckboxSlot = styled.div`
    position: absolute;
    top: 50%;
    left: var(--spacing-m);
    transform: translateY(-50%);
`;

// Hidden until the row is hovered or holds focus, but always in the tab order.
const RowMenu = styled.div<{$visible: boolean}>`
    position: absolute;
    top: 50%;
    right: calc(var(--spacing-xxs) + var(--spacing-xs));
    transform: translateY(-50%);
    opacity: ${(p) => (p.$visible ? 1 : 0)};

    ${VersionRow}:hover & {
        opacity: 1;
    }

    /* Keyboard focus only: a click leaves focus on the row or on the button
       after its menu closes, and :focus-within would keep the button showing. */
    ${VersionRow}:has(:focus-visible) & {
        opacity: 1;
    }

    @media (hover: none) {
        opacity: 1;
    }
`;

const VersionMeta = styled.div`
    font-size: 12px;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
`;

const CurrentTag = () => (
    <Tag
        type='info'
        label={<FormattedMessage defaultMessage='Current'/>}
    />
);

const DetailPane = styled.div`
    position: relative;
    flex: 1;
    min-width: 0;
    min-height: 0;
`;

const DetailContent = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-l);
    padding: var(--spacing-xxxl) var(--spacing-xxxxl);
`;

const DetailHeader = styled.div`
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: var(--spacing-l);
`;

const DetailTitle = styled.h2`
    display: flex;
    align-items: center;
    gap: var(--spacing-xs);
    margin: 0 0 var(--spacing-xxxs);
    font-size: 18px;
    font-weight: 600;
    line-height: 24px;
    color: var(--center-channel-color);
`;

const Muted = styled.div`
    font-size: 14px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const ChangeList = styled.ul`
    margin: 0 0 var(--spacing-m);
    padding-left: var(--spacing-xl);
`;

const WarningText = styled.p`
    margin: 0;
    font-weight: 600;
    color: var(--dnd-indicator, #D24B4E);

    & + & {
        margin-top: var(--spacing-xs);
    }
`;

export default AgentVersionHistoryModal;
