// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage} from 'react-intl';
import styled, {keyframes} from 'styled-components';

import {PrimaryButton, SecondaryButton, TertiaryButton} from '../../assets/buttons';

import {HelpText, ItemLabel} from '../item';

import {JobStatusType, StatusMessageType, HealthCheckResultType} from './types';

const ButtonContainer = styled.div`
    margin-top: 24px;
    padding-top: 24px;
    border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    grid-column: 1 / -1;
`;

const ActionContainer = styled.div`
    display: grid;
    grid-template-columns: minmax(auto, 275px) 1fr;
    grid-column-gap: 16px;
`;

const SuccessHelpText = styled(HelpText)`
    margin-top: 8px;
    color: var(--online-indicator);
`;

const ErrorHelpText = styled(HelpText)`
    margin-top: 8px;
    color: var(--error-text);
`;

const ProgressContainer = styled.div`
    margin-top: 8px;
    width: 100%;
    background-color: rgba(var(--center-channel-color-rgb), 0.08);
    border-radius: 4px;
    height: 8px;
    overflow: hidden;
`;

const ProgressBar = styled.div<{progress: number}>`
    height: 100%;
    width: ${(props) => props.progress}%;
    background-color: var(--button-bg);
    transition: width 0.3s ease-in-out;
`;

const indeterminateSlide = keyframes`
    0% {
        transform: translateX(-100%);
    }
    100% {
        transform: translateX(250%);
    }
`;

const IndeterminateProgressBar = styled.div`
    height: 100%;
    width: 40%;
    background-color: var(--button-bg);
    animation: ${indeterminateSlide} 1.5s ease-in-out infinite;
`;

const ProgressText = styled(HelpText)`
    margin-top: 8px;
    margin-bottom: 12px;
    font-size: 12px;
`;

const ButtonGroup = styled.div`
    display: flex;
    gap: 8px;
`;

const WarningBanner = styled.div`
    background-color: rgba(var(--away-indicator-rgb), 0.1);
    border: 1px solid var(--away-indicator);
    border-radius: 4px;
    padding: 12px 16px;
    margin-bottom: 16px;
    display: flex;
    align-items: flex-start;
    gap: 8px;
`;

const WarningIcon = styled.span`
    color: var(--away-indicator);
    font-size: 16px;
`;

const WarningText = styled.div`
    color: var(--center-channel-color);
    font-size: 14px;
`;

const NoteBanner = styled.div`
    background-color: rgba(var(--center-channel-color-rgb), 0.04);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    padding: 12px 16px;
    margin-bottom: 16px;
    display: flex;
    align-items: flex-start;
    gap: 8px;
`;

const NoteText = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.88);
    font-size: 14px;
`;

const HealthCheckCard = styled.div`
    background-color: rgba(var(--center-channel-color-rgb), 0.04);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    border-radius: 4px;
    padding: 12px 16px;
    margin-top: 12px;
    margin-bottom: 12px;
`;

const HealthCheckRow = styled.div`
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 4px 0;
    font-size: 13px;
`;

const HealthCheckLabel = styled.span`
    color: rgba(var(--center-channel-color-rgb), 0.72);
`;

const HealthCheckValue = styled.span`
    color: var(--center-channel-color);
    font-weight: 500;
`;

const StatusBadge = styled.span<{status: string}>`
    display: inline-block;
    padding: 2px 8px;
    border-radius: 10px;
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    background-color: ${(props) => {
        switch (props.status) {
        case 'healthy':
            return 'rgba(var(--online-indicator-rgb), 0.16)';
        case 'mismatch':
            return 'rgba(var(--away-indicator-rgb), 0.16)';
        case 'needs_reindex':
        case 'error':
            return 'rgba(var(--error-text-color-rgb), 0.16)';
        default:
            return 'rgba(var(--center-channel-color-rgb), 0.08)';
        }
    }};
    color: ${(props) => {
        switch (props.status) {
        case 'healthy':
            return 'var(--online-indicator)';
        case 'mismatch':
            return 'var(--away-indicator)';
        case 'needs_reindex':
        case 'error':
            return 'var(--error-text)';
        default:
            return 'var(--center-channel-color)';
        }
    }};
`;

const SectionDivider = styled.div`
    margin-top: 24px;
    padding-top: 24px;
    border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
`;

const JobInfoCard = styled.div`
    background-color: rgba(var(--center-channel-color-rgb), 0.04);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    border-radius: 4px;
    padding: 8px 12px;
    margin-top: 8px;
    font-size: 12px;
`;

const JobInfoRow = styled.div`
    display: flex;
    justify-content: space-between;
    padding: 2px 0;
`;

const JobInfoLabel = styled.span`
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const JobInfoValue = styled.span`
    color: var(--center-channel-color);
`;

const StaleBanner = styled.div`
    background-color: rgba(var(--error-text-color-rgb), 0.1);
    border: 1px solid var(--error-text);
    border-radius: 4px;
    padding: 12px 16px;
    margin-bottom: 16px;
    display: flex;
    align-items: flex-start;
    gap: 8px;
`;

const StaleText = styled.div`
    color: var(--center-channel-color);
    font-size: 14px;
    flex: 1;
`;

const StaleActions = styled.div`
    margin-top: 8px;
    display: flex;
    gap: 8px;
`;

// Unknown phases get fallback text (may come from a newer plugin version).
const renderVectorIndexPhase = (phase: string) => {
    switch (phase) {
    case 'dropped':
        return <FormattedMessage defaultMessage='Dropped for bulk load — search unavailable'/>;
    case 'building':
        return <FormattedMessage defaultMessage='Rebuilding — search unavailable'/>;
    case 'repairing':
        return <FormattedMessage defaultMessage='Re-indexing posts edited during rebuild'/>;
    default:
        return <FormattedMessage defaultMessage='Unknown state'/>;
    }
};

interface ReindexSectionProps {
    jobStatus: JobStatusType | null;
    statusMessage: StatusMessageType;
    healthCheckResult: HealthCheckResultType | null;
    healthCheckLoading: boolean;
    hasLocalModelMismatch: boolean;
    localMismatchReason: string;
    hasLocalHNSWMismatch: boolean;
    hasLocalRetentionWiden: boolean;
    hasUnsavedRetentionWiden: boolean;
    hasLocalRetentionTighten: boolean;
    isJobStale: boolean;
    onReindexClick: () => void;
    onCancelJob: () => void;
    onCatchUpClick: () => void;
    onRebuildVectorIndexClick: () => void;
    onHealthCheck: () => void;
    onResumeClick: () => void;
}

export const ReindexSection = ({
    jobStatus,
    statusMessage,
    healthCheckResult,
    healthCheckLoading,
    hasLocalModelMismatch,
    localMismatchReason,
    hasLocalHNSWMismatch,
    hasLocalRetentionWiden,
    hasUnsavedRetentionWiden,
    hasLocalRetentionTighten,
    isJobStale,
    onReindexClick,
    onCancelJob,
    onCatchUpClick,
    onRebuildVectorIndexClick,
    onHealthCheck,
    onResumeClick,
}: ReindexSectionProps) => {
    // cancel_requested is non-terminal: the worker is still running until it
    // observes the request and writes canceled.
    const isReindexing = jobStatus?.status === 'running' || jobStatus?.status === 'cancel_requested';

    const hasProgress = (jobStatus?.processed_rows ?? 0) > 0;
    const isRebuildJob = jobStatus?.operation === 'rebuild_vector_index';
    const embeddingIdentityMismatch = hasLocalModelMismatch || healthCheckResult?.model_compatible === false;

    // Resume is for embed reindex jobs with progress. Rebuilds are not resumable.
    const canResume = !isRebuildJob &&
        (jobStatus?.status === 'failed' || jobStatus?.status === 'canceled') &&
        (jobStatus?.processed_rows ?? 0) > 0;

    const retentionCatchUpNeeded = (!hasUnsavedRetentionWiden && hasLocalRetentionWiden) ||
        Boolean(healthCheckResult?.needs_catch_up);
    const indexHoles = Boolean(healthCheckResult &&
        healthCheckResult.indexed_post_count > 0 &&
        (healthCheckResult.missing_posts > 0 ||
         healthCheckResult.status === 'mismatch' ||
         healthCheckResult.status === 'needs_reindex'));
    const showCatchUp = retentionCatchUpNeeded || indexHoles;

    const formatTimestamp = (timestamp: string | undefined) => {
        if (!timestamp) {
            return '-';
        }
        const date = new Date(timestamp);
        return date.toLocaleString();
    };

    const getStatusLabel = (status: string) => {
        switch (status) {
        case 'healthy':
            return <FormattedMessage defaultMessage='Healthy'/>;
        case 'mismatch':
            return <FormattedMessage defaultMessage='Minor Mismatch'/>;
        case 'needs_reindex':
            return <FormattedMessage defaultMessage='Needs Reindex'/>;
        case 'error':
            return <FormattedMessage defaultMessage='Error'/>;
        default:
            return status;
        }
    };

    return (
        <ButtonContainer>
            {/* Stale Job Warning */}
            {isJobStale && isReindexing && (
                <StaleBanner>
                    <WarningIcon>{'⚠️'}</WarningIcon>
                    <StaleText>
                        <strong><FormattedMessage defaultMessage='Job May Be Stale'/></strong>
                        <br/>
                        <FormattedMessage
                            defaultMessage='The reindex job has not updated in over 10 minutes. The node running it ({nodeId}) may have crashed. Start a new run to take over from where it left off.'
                            values={{nodeId: jobStatus?.node_id || 'unknown'}}
                        />
                        <StaleActions>
                            {!isRebuildJob && hasProgress && (
                                <SecondaryButton onClick={onResumeClick}>
                                    <FormattedMessage defaultMessage='Resume from checkpoint'/>
                                </SecondaryButton>
                            )}
                            {isRebuildJob && (
                                <SecondaryButton
                                    onClick={onRebuildVectorIndexClick}
                                    disabled={embeddingIdentityMismatch}
                                >
                                    <FormattedMessage defaultMessage='Rebuild vector index'/>
                                </SecondaryButton>
                            )}
                            <SecondaryButton onClick={onReindexClick}>
                                <FormattedMessage defaultMessage='Reindex from scratch'/>
                            </SecondaryButton>
                        </StaleActions>
                    </StaleText>
                </StaleBanner>
            )}

            {/* Model Compatibility Warning - show when form values differ from stored index values */}
            {hasLocalModelMismatch && (
                <WarningBanner>
                    <WarningIcon>{'⚠️'}</WarningIcon>
                    <WarningText>
                        <strong><FormattedMessage defaultMessage='Embedding Model Changed'/></strong>
                        <br/>
                        <FormattedMessage
                            defaultMessage='The embedding model configuration has changed ({reason}). Search functionality is disabled until you run a full reindex.'
                            values={{reason: localMismatchReason}}
                        />
                    </WarningText>
                </WarningBanner>
            )}

            {hasLocalHNSWMismatch && !hasLocalModelMismatch && (
                <WarningBanner>
                    <WarningIcon>{'⚠️'}</WarningIcon>
                    <WarningText>
                        <strong><FormattedMessage defaultMessage='HNSW M Changed'/></strong>
                        <br/>
                        <FormattedMessage defaultMessage='HNSW M has changed. Use Rebuild vector index to apply it — not Full Reindex. Search keeps working until you rebuild; the new M takes effect after the rebuild.'/>
                    </WarningText>
                </WarningBanner>
            )}

            {hasUnsavedRetentionWiden && !hasLocalModelMismatch && (
                <WarningBanner>
                    <WarningIcon>{'⚠️'}</WarningIcon>
                    <WarningText>
                        <strong><FormattedMessage defaultMessage='Index retention increased'/></strong>
                        <br/>
                        <FormattedMessage defaultMessage='Save the configuration before running Catch Up. Catch Up uses the saved retention window, not this unsaved value.'/>
                    </WarningText>
                </WarningBanner>
            )}

            {hasLocalRetentionWiden && !hasUnsavedRetentionWiden && !hasLocalModelMismatch && (
                <WarningBanner>
                    <WarningIcon>{'⚠️'}</WarningIcon>
                    <WarningText>
                        <strong><FormattedMessage defaultMessage='Index retention increased'/></strong>
                        <br/>
                        <FormattedMessage defaultMessage='The index now looks further back. Run Catch Up to embed older posts that are not already in the index. Search stays available — do not Full Reindex unless you also changed the embedding model or vector precision.'/>
                    </WarningText>
                </WarningBanner>
            )}

            {hasLocalRetentionTighten && !hasLocalModelMismatch && !hasLocalRetentionWiden && (
                <NoteBanner>
                    <NoteText>
                        <FormattedMessage defaultMessage='Lowering this does not remove already-indexed posts. Search still returns whatever is in the index. The new window applies to live indexing and the next Full Reindex or Catch Up.'/>
                    </NoteText>
                </NoteBanner>
            )}

            {/* Reindex Section */}
            <ActionContainer>
                <ItemLabel>
                    <FormattedMessage defaultMessage='Reindex All Posts'/>
                </ItemLabel>
                <div>
                    {/* Show running job UI */}
                    {isReindexing && (
                        <>
                            <ButtonGroup>
                                <SecondaryButton
                                    onClick={onCancelJob}
                                    disabled={jobStatus?.status === 'cancel_requested'}
                                >
                                    {jobStatus?.status === 'cancel_requested' ? (
                                        <FormattedMessage defaultMessage='Canceling…'/>
                                    ) : (
                                        <FormattedMessage defaultMessage='Cancel Reindexing'/>
                                    )}
                                </SecondaryButton>
                            </ButtonGroup>

                            {jobStatus && (
                                <>
                                    {jobStatus.phase === 'building_index' ? (
                                        <ProgressText>
                                            <FormattedMessage defaultMessage='Bulk load complete — building the vector index. This can take a while on large workspaces; search stays unavailable until it finishes.'/>
                                        </ProgressText>
                                    ) : (
                                        <ProgressText>
                                            <FormattedMessage
                                                defaultMessage='Processing: {processed} of {total} posts ({percent}%)'
                                                values={{
                                                    processed: jobStatus.processed_rows.toLocaleString(),
                                                    total: jobStatus.total_rows.toLocaleString(),
                                                    percent: jobStatus.total_rows ? Math.min(Math.floor((jobStatus.processed_rows / jobStatus.total_rows) * 100), 100) : 0,
                                                }}
                                            />
                                        </ProgressText>
                                    )}
                                    <ProgressContainer>
                                        {jobStatus.phase === 'building_index' ? (
                                            <IndeterminateProgressBar/>
                                        ) : (
                                            <ProgressBar
                                                progress={jobStatus.total_rows ? Math.min((jobStatus.processed_rows / jobStatus.total_rows) * 100, 100) : 0}
                                            />
                                        )}
                                    </ProgressContainer>
                                    <JobInfoCard>
                                        {jobStatus.node_id && (
                                            <JobInfoRow>
                                                <JobInfoLabel>
                                                    <FormattedMessage defaultMessage='Running on node'/>
                                                </JobInfoLabel>
                                                <JobInfoValue>{jobStatus.node_id}</JobInfoValue>
                                            </JobInfoRow>
                                        )}
                                        {jobStatus.last_updated_at && (
                                            <JobInfoRow>
                                                <JobInfoLabel>
                                                    <FormattedMessage defaultMessage='Last heartbeat'/>
                                                </JobInfoLabel>
                                                <JobInfoValue>{formatTimestamp(jobStatus.last_updated_at)}</JobInfoValue>
                                            </JobInfoRow>
                                        )}
                                    </JobInfoCard>
                                </>
                            )}
                        </>
                    )}

                    {/* Show resume UI when job failed or canceled with progress */}
                    {!isReindexing && canResume && jobStatus && (
                        <>
                            <ButtonGroup>
                                <PrimaryButton onClick={onResumeClick}>
                                    <FormattedMessage defaultMessage='Resume Reindex'/>
                                </PrimaryButton>
                                <SecondaryButton onClick={onReindexClick}>
                                    <FormattedMessage defaultMessage='Start Over'/>
                                </SecondaryButton>
                            </ButtonGroup>
                            <ProgressText>
                                <FormattedMessage
                                    defaultMessage='Previous progress: {processed} of {total} posts ({percent}%) - Resume to continue from checkpoint'
                                    values={{
                                        processed: jobStatus.processed_rows.toLocaleString(),
                                        total: jobStatus.total_rows.toLocaleString(),
                                        percent: jobStatus.total_rows ? Math.min(Math.floor((jobStatus.processed_rows / jobStatus.total_rows) * 100), 100) : 0,
                                    }}
                                />
                            </ProgressText>
                        </>
                    )}

                    {/* Show default buttons when no job is running and resume is not available */}
                    {!isReindexing && !canResume && (
                        <ButtonGroup>
                            <PrimaryButton onClick={onReindexClick}>
                                <FormattedMessage defaultMessage='Full Reindex'/>
                            </PrimaryButton>
                            {showCatchUp && (
                                <TertiaryButton
                                    onClick={onCatchUpClick}
                                    disabled={embeddingIdentityMismatch}
                                >
                                    <FormattedMessage defaultMessage='Catch Up'/>
                                </TertiaryButton>
                            )}
                            <TertiaryButton
                                onClick={onRebuildVectorIndexClick}
                                disabled={embeddingIdentityMismatch}
                            >
                                <FormattedMessage defaultMessage='Rebuild vector index'/>
                            </TertiaryButton>
                        </ButtonGroup>
                    )}

                    {statusMessage.message && (
                        statusMessage.success ? (
                            <SuccessHelpText>
                                {statusMessage.message}
                            </SuccessHelpText>
                        ) : (
                            <ErrorHelpText>
                                {statusMessage.message}
                            </ErrorHelpText>
                        )
                    )}

                    <HelpText>
                        <FormattedMessage defaultMessage='Full Reindex clears the index and rebuilds from scratch. Catch Up fills holes in the current retention window (posts not already in the index) without disabling search. Rebuild vector index rebuilds the HNSW graph without re-embedding posts. Changing the retention window while a job is running does not change the running job window; abort and start a new job if you want the new bounds.'/>
                    </HelpText>
                </div>
            </ActionContainer>

            {/* Health Check Section */}
            <SectionDivider>
                <ActionContainer>
                    <ItemLabel>
                        <FormattedMessage defaultMessage='Index Health'/>
                    </ItemLabel>
                    <div>
                        <TertiaryButton
                            onClick={onHealthCheck}
                            disabled={healthCheckLoading}
                        >
                            {healthCheckLoading ? (
                                <FormattedMessage defaultMessage='Refreshing...'/>
                            ) : (
                                <FormattedMessage defaultMessage='Refresh'/>
                            )}
                        </TertiaryButton>

                        {healthCheckResult && (
                            <HealthCheckCard>
                                <HealthCheckRow>
                                    <HealthCheckLabel>
                                        <FormattedMessage defaultMessage='Status'/>
                                    </HealthCheckLabel>
                                    <StatusBadge status={healthCheckResult.status}>
                                        {getStatusLabel(healthCheckResult.status)}
                                    </StatusBadge>
                                </HealthCheckRow>
                                <HealthCheckRow>
                                    <HealthCheckLabel>
                                        <FormattedMessage defaultMessage='Posts in Database'/>
                                    </HealthCheckLabel>
                                    <HealthCheckValue>
                                        {healthCheckResult.db_post_count.toLocaleString()}
                                    </HealthCheckValue>
                                </HealthCheckRow>
                                <HealthCheckRow>
                                    <HealthCheckLabel>
                                        <FormattedMessage defaultMessage='Posts in Index'/>
                                    </HealthCheckLabel>
                                    <HealthCheckValue>
                                        {healthCheckResult.indexed_post_count.toLocaleString()}
                                    </HealthCheckValue>
                                </HealthCheckRow>
                                {healthCheckResult.missing_posts > 0 && (
                                    <HealthCheckRow>
                                        <HealthCheckLabel>
                                            <FormattedMessage defaultMessage='Missing Posts'/>
                                        </HealthCheckLabel>
                                        <HealthCheckValue>
                                            {healthCheckResult.missing_posts.toLocaleString()}
                                        </HealthCheckValue>
                                    </HealthCheckRow>
                                )}
                                {healthCheckResult.vector_index_state && (
                                    <HealthCheckRow>
                                        <HealthCheckLabel>
                                            <FormattedMessage defaultMessage='Vector Index'/>
                                        </HealthCheckLabel>
                                        <HealthCheckValue>
                                            {renderVectorIndexPhase(healthCheckResult.vector_index_state.phase)}
                                        </HealthCheckValue>
                                    </HealthCheckRow>
                                )}
                                {healthCheckResult.error && (
                                    <ErrorHelpText>
                                        {healthCheckResult.error}
                                    </ErrorHelpText>
                                )}
                            </HealthCheckCard>
                        )}
                    </div>
                </ActionContainer>
            </SectionDivider>

        </ButtonContainer>
    );
};
