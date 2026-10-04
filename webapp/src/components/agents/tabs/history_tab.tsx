// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useRef, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl, type IntlShape} from 'react-intl';

import {UserProfile} from '@mattermost/types/users';

import {getAgentVersion, getAgentVersions, getProfilesByIds, restoreAgentVersion} from '@/client';
import {AgentVersionDetail, AgentVersionList, ServiceInfo, UserAgent} from '@/types/agents';
import {PrimaryButton} from '@/components/assets/buttons';
import ConfirmationDialog from '@/components/confirmation_dialog';

import {changedFieldLabels, versionSourceLabel} from '../agent_version_labels';
import VersionSnapshot from '../version_snapshot';

const RESTORE_DIALOG_TITLE_ID = 'restore-agent-version-title';

type Props = {
    agentId: string;
    services: ServiceInfo[];

    /** True when the editor has unsaved changes that a restore would discard. */
    isDirty: boolean;

    /** Called with the agent returned by the restore endpoint. */
    onRestored: (agent: UserAgent) => void;
}

function userDisplayName(user: UserProfile): string {
    const fullName = [user.first_name, user.last_name].filter(Boolean).join(' ').trim();
    return user.nickname || fullName || user.username;
}

function errorMessage(e: unknown, fallback: string): string {
    const message = (e as {message?: unknown})?.message;
    return typeof message === 'string' && message.trim() ? message.trim() : fallback;
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

const HistoryTab = ({agentId, services, isDirty, onRestored}: Props) => {
    const intl = useIntl();

    // Effects below must not re-run when a new intl object is created.
    const intlRef = useRef(intl);
    intlRef.current = intl;

    const [list, setList] = useState<AgentVersionList | null>(null);
    const [listError, setListError] = useState('');
    const [selected, setSelected] = useState<number | null>(null);
    const [detail, setDetail] = useState<AgentVersionDetail | null>(null);
    const [detailError, setDetailError] = useState('');
    const [authors, setAuthors] = useState<Record<string, string>>({});
    const [confirmOpen, setConfirmOpen] = useState(false);
    const [restoring, setRestoring] = useState(false);
    const [restoreError, setRestoreError] = useState('');
    const [restoredNotice, setRestoredNotice] = useState('');
    const requestedAuthors = useRef(new Set<string>());
    const mountedRef = useRef(true);

    useEffect(() => {
        mountedRef.current = true;
        return () => {
            mountedRef.current = false;
        };
    }, []);

    const loadList = useCallback(async (selectNewest: boolean) => {
        try {
            setListError('');
            const result = await getAgentVersions(agentId);
            const versions = result.versions ?? [];
            setList({...result, versions});
            setSelected((prev) => {
                if (selectNewest || prev === null || !versions.some((v) => v.version === prev)) {
                    return result.currentVersion;
                }
                return prev;
            });
        } catch (e) {
            setListError(errorMessage(e, intlRef.current.formatMessage({defaultMessage: 'Failed to load version history.'})));
        }
    }, [agentId]);

    useEffect(() => {
        loadList(true);
    }, [loadList]);

    useEffect(() => {
        if (!list) {
            return () => {
                // Nothing to resolve before the list loads
            };
        }
        const ids = list.versions.
            map((v) => v.createdBy).
            filter((id) => id && !requestedAuthors.current.has(id));
        const unique = Array.from(new Set(ids));
        if (unique.length === 0) {
            return () => {
                // All authors already requested
            };
        }
        unique.forEach((id) => requestedAuthors.current.add(id));
        getProfilesByIds(unique).then((profiles) => {
            if (!mountedRef.current) {
                return;
            }
            setAuthors((prev) => {
                const next = {...prev};
                for (const profile of profiles) {
                    next[profile.id] = userDisplayName(profile);
                }
                return next;
            });
        }).catch(() => {
            // Unresolved authors fall back to a generic label.
            unique.forEach((id) => requestedAuthors.current.delete(id));
        });
        return () => {
            // Requests are tracked in requestedAuthors; results apply while mounted.
        };
    }, [list]);

    useEffect(() => {
        if (selected === null) {
            return () => {
                // No version selected yet
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
            }
        });
        return () => {
            cancelled = true;
        };
    }, [agentId, selected]);

    const authorLabel = (createdBy: string): string => {
        if (!createdBy) {
            return intl.formatMessage({defaultMessage: 'System'});
        }
        return authors[createdBy] ?? intl.formatMessage({defaultMessage: 'Unknown user'});
    };

    const handleSelect = (version: number) => {
        setSelected(version);
        setRestoreError('');
        setRestoredNotice('');
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
            setConfirmOpen(false);
            setRestoredNotice(intl.formatMessage(
                {defaultMessage: 'Version {version} was restored as the current version.'},
                {version: restoredVersion},
            ));
            onRestored(agent);
            await loadList(true);
        } catch (e) {
            setConfirmOpen(false);
            setRestoreError(errorMessage(e, intl.formatMessage({defaultMessage: 'Failed to restore this version. Please try again.'})));
        } finally {
            setRestoring(false);
        }
    };

    if (listError && !list) {
        return <ErrorBanner role='alert'>{listError}</ErrorBanner>;
    }

    if (!list) {
        return (
            <Muted>
                <FormattedMessage defaultMessage='Loading version history...'/>
            </Muted>
        );
    }

    const selectedSummary = list.versions.find((v) => v.version === selected);
    const isCurrentSelected = selected !== null && selected === list.currentVersion;

    return (
        <Layout>
            <VersionList
                aria-label={intl.formatMessage({defaultMessage: 'Version history'})}
                data-testid='version-list'
            >
                {list.versions.map((v) => {
                    const changed = changedFieldLabels(intl, v.changedFields);
                    return (
                        <li key={v.version}>
                            <VersionItem
                                type='button'
                                $selected={v.version === selected}
                                {...(v.version === selected ? {'aria-current': 'true' as const} : {})}
                                onClick={() => handleSelect(v.version)}
                            >
                                <VersionTitleRow>
                                    <VersionTitle>
                                        {intl.formatMessage({defaultMessage: 'Version {version}'}, {version: v.version})}
                                    </VersionTitle>
                                    {v.version === list.currentVersion && (
                                        <CurrentBadge>
                                            <FormattedMessage defaultMessage='Current'/>
                                        </CurrentBadge>
                                    )}
                                </VersionTitleRow>
                                <VersionMeta>{versionSourceLabel(intl, v)}</VersionMeta>
                                <VersionMeta>
                                    {intl.formatMessage(
                                        {defaultMessage: '{author} · {time}'},
                                        {author: authorLabel(v.createdBy), time: formatTimestamp(intl, v.createAt)},
                                    )}
                                </VersionMeta>
                                {changed.length > 0 && (
                                    <VersionMeta>
                                        {intl.formatMessage(
                                            {defaultMessage: 'Changed: {fields}'},
                                            {fields: changed.join(', ')},
                                        )}
                                    </VersionMeta>
                                )}
                            </VersionItem>
                        </li>
                    );
                })}
            </VersionList>

            <DetailPane>
                {restoredNotice && <SuccessBanner role='status'>{restoredNotice}</SuccessBanner>}
                {restoreError && <ErrorBanner role='alert'>{restoreError}</ErrorBanner>}
                {listError && <ErrorBanner role='alert'>{listError}</ErrorBanner>}
                {detailError && <ErrorBanner role='alert'>{detailError}</ErrorBanner>}

                {selectedSummary && (
                    <DetailHeader>
                        <div>
                            <DetailTitle>
                                {intl.formatMessage({defaultMessage: 'Version {version}'}, {version: selectedSummary.version})}
                                {isCurrentSelected && (
                                    <CurrentBadge>
                                        <FormattedMessage defaultMessage='Current'/>
                                    </CurrentBadge>
                                )}
                            </DetailTitle>
                            <VersionMeta>
                                {intl.formatMessage(
                                    {defaultMessage: '{source} · {author} · {time}'},
                                    {
                                        source: versionSourceLabel(intl, selectedSummary),
                                        author: authorLabel(selectedSummary.createdBy),
                                        time: formatTimestamp(intl, selectedSummary.createAt),
                                    },
                                )}
                            </VersionMeta>
                        </div>
                        {!isCurrentSelected && (
                            <PrimaryButton
                                type='button'
                                disabled={!detail || restoring}
                                onClick={() => setConfirmOpen(true)}
                            >
                                <FormattedMessage defaultMessage='Restore this version'/>
                            </PrimaryButton>
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
                        services={services}
                    />
                )}
            </DetailPane>

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
        </Layout>
    );
};

const Layout = styled.div`
    display: flex;
    gap: 24px;
    align-items: flex-start;
`;

const VersionList = styled.ol`
    list-style: none;
    margin: 0;
    padding: 0;
    flex: 0 0 300px;
    display: flex;
    flex-direction: column;
    gap: 8px;
`;

const VersionItem = styled.button<{$selected: boolean}>`
    display: flex;
    flex-direction: column;
    gap: 2px;
    width: 100%;
    padding: 10px 12px;
    text-align: left;
    cursor: pointer;
    border-radius: 4px;
    color: var(--center-channel-color);
    border: 1px solid ${(p) => (p.$selected ? 'var(--button-bg)' : 'rgba(var(--center-channel-color-rgb), 0.12)')};
    background: ${(p) => (p.$selected ? 'rgba(var(--button-bg-rgb, 28, 88, 217), 0.08)' : 'var(--center-channel-bg)')};

    &:hover {
        background: ${(p) => (p.$selected ? 'rgba(var(--button-bg-rgb, 28, 88, 217), 0.08)' : 'rgba(var(--center-channel-color-rgb), 0.04)')};
    }
`;

const VersionTitleRow = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
`;

const VersionTitle = styled.span`
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
`;

const VersionMeta = styled.div`
    font-size: 12px;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
`;

const CurrentBadge = styled.span`
    margin-left: 8px;
    padding: 1px 8px;
    border-radius: 10px;
    font-size: 11px;
    font-weight: 600;
    line-height: 16px;
    color: var(--button-bg);
    background: rgba(var(--button-bg-rgb, 28, 88, 217), 0.08);
`;

const DetailPane = styled.div`
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 16px;
`;

const DetailHeader = styled.div`
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: 16px;
`;

const DetailTitle = styled.h2`
    display: flex;
    align-items: center;
    margin: 0 0 4px;
    font-size: 18px;
    font-weight: 600;
    line-height: 24px;
    color: var(--center-channel-color);
`;

const Muted = styled.div`
    font-size: 14px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const ErrorBanner = styled.div`
    padding: 10px 12px;
    background: rgba(var(--dnd-indicator-rgb, 210, 75, 78), 0.08);
    border-radius: 4px;
    border: 1px solid rgba(var(--dnd-indicator-rgb, 210, 75, 78), 0.3);
    color: var(--dnd-indicator, #D24B4E);
    font-size: 14px;
`;

const SuccessBanner = styled.div`
    padding: 10px 12px;
    background: rgba(var(--online-indicator-rgb, 6, 214, 160), 0.08);
    border-radius: 4px;
    border: 1px solid rgba(var(--online-indicator-rgb, 6, 214, 160), 0.4);
    color: var(--center-channel-color);
    font-size: 14px;
`;

const WarningText = styled.p`
    margin: 0;
    font-weight: 600;
    color: var(--dnd-indicator, #D24B4E);
`;

export default HistoryTab;
