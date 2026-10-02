// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import styled from 'styled-components';

import {Button} from '@mattermost/compass-ui/components/button';
import {ProgressBar} from '@mattermost/compass-ui/components/progress-bar';
import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';
import {Tag, type TagType} from '@mattermost/compass-ui/components/tag';

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

const JobProgressBar = styled(ProgressBar)`
    margin-top: 8px;
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

const Banner = styled(SectionNotice)`
    margin-bottom: 16px;
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

const healthStatusTagType = (status: string): TagType => {
    switch (status) {
    case 'healthy':
        return 'success';
    case 'mismatch':
        return 'warning';
    case 'needs_reindex':
    case 'error':
        return 'danger';
    default:
        return 'default';
    }
};

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
    const intl = useIntl();

    // cancel_requested is non-terminal: the worker is still running until it
    // observes the request and writes canceled.
    const isReindexing = jobStatus?.status === 'running' || jobStatus?.status === 'cancel_requested';

    const hasProgress = (jobStatus?.processed_rows ?? 0) > 0;
    const isRebuildJob = jobStatus?.operation === 'rebuild_vector_index';
    const embeddingIdentityMismatch = hasLocalModelMismatch || healthCheckResult?.model_compatible === false;

    const reindexFromScratchLabel = <FormattedMessage defaultMessage='Reindex from scratch'/>;
    let staleRecovery: {label: React.ReactNode; onClick: () => void; disabled: boolean} | null = null;
    if (isRebuildJob) {
        staleRecovery = {
            label: <FormattedMessage defaultMessage='Rebuild vector index'/>,
            onClick: onRebuildVectorIndexClick,
            disabled: embeddingIdentityMismatch,
        };
    } else if (hasProgress) {
        staleRecovery = {
            label: <FormattedMessage defaultMessage='Resume from checkpoint'/>,
            onClick: onResumeClick,
            disabled: false,
        };
    }

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
                <Banner
                    type='danger'
                    title={<FormattedMessage defaultMessage='Job May Be Stale'/>}
                    description={(
                        <FormattedMessage
                            defaultMessage='The reindex job has not updated in over 10 minutes. The node running it ({nodeId}) may have crashed. Start a new run to take over from where it left off.'
                            values={{nodeId: jobStatus?.node_id || 'unknown'}}
                        />
                    )}
                    primaryButtonLabel={staleRecovery?.label ?? reindexFromScratchLabel}
                    onPrimaryAction={staleRecovery?.onClick ?? onReindexClick}
                    primaryActionDisabled={staleRecovery?.disabled}
                    secondaryButtonLabel={staleRecovery ? reindexFromScratchLabel : null}
                    onSecondaryAction={onReindexClick}
                />
            )}

            {/* Model Compatibility Warning - show when form values differ from stored index values */}
            {hasLocalModelMismatch && (
                <Banner
                    type='warning'
                    title={<FormattedMessage defaultMessage='Embedding Model Changed'/>}
                    description={(
                        <FormattedMessage
                            defaultMessage='The embedding model configuration has changed ({reason}). Search functionality is disabled until you run a full reindex.'
                            values={{reason: localMismatchReason}}
                        />
                    )}
                />
            )}

            {hasLocalHNSWMismatch && !hasLocalModelMismatch && (
                <Banner
                    type='warning'
                    title={<FormattedMessage defaultMessage='HNSW M Changed'/>}
                    description={<FormattedMessage defaultMessage='HNSW M has changed. Use Rebuild vector index to apply it — not Full Reindex. Search keeps working until you rebuild; the new M takes effect after the rebuild.'/>}
                />
            )}

            {hasUnsavedRetentionWiden && !hasLocalModelMismatch && (
                <Banner
                    type='warning'
                    title={<FormattedMessage defaultMessage='Index retention increased'/>}
                    description={<FormattedMessage defaultMessage='Save the configuration before running Catch Up. Catch Up uses the saved retention window, not this unsaved value.'/>}
                />
            )}

            {hasLocalRetentionWiden && !hasUnsavedRetentionWiden && !hasLocalModelMismatch && (
                <Banner
                    type='warning'
                    title={<FormattedMessage defaultMessage='Index retention increased'/>}
                    description={<FormattedMessage defaultMessage='The index now looks further back. Run Catch Up to embed older posts that are not already in the index. Search stays available — do not Full Reindex unless you also changed the embedding model or vector precision.'/>}
                />
            )}

            {hasLocalRetentionTighten && !hasLocalModelMismatch && !hasLocalRetentionWiden && (
                <Banner
                    type='info'
                    title={<FormattedMessage defaultMessage='Lowering this does not remove already-indexed posts. Search still returns whatever is in the index. The new window applies to live indexing and the next Full Reindex or Catch Up.'/>}
                />
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
                                <Button
                                    emphasis='secondary'
                                    onClick={onCancelJob}
                                    disabled={jobStatus?.status === 'cancel_requested'}
                                >
                                    {jobStatus?.status === 'cancel_requested' ? (
                                        <FormattedMessage defaultMessage='Canceling…'/>
                                    ) : (
                                        <FormattedMessage defaultMessage='Cancel Reindexing'/>
                                    )}
                                </Button>
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
                                    <JobProgressBar
                                        size='small'
                                        indeterminate={jobStatus.phase === 'building_index'}
                                        value={jobStatus.total_rows ? Math.min((jobStatus.processed_rows / jobStatus.total_rows) * 100, 100) : 0}
                                        aria-label={intl.formatMessage({defaultMessage: 'Reindex progress'})}
                                    />
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
                                <Button
                                    emphasis='primary'
                                    onClick={onResumeClick}
                                >
                                    <FormattedMessage defaultMessage='Resume Reindex'/>
                                </Button>
                                <Button
                                    emphasis='secondary'
                                    onClick={onReindexClick}
                                >
                                    <FormattedMessage defaultMessage='Start Over'/>
                                </Button>
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
                            <Button
                                emphasis='primary'
                                onClick={onReindexClick}
                            >
                                <FormattedMessage defaultMessage='Full Reindex'/>
                            </Button>
                            {showCatchUp && (
                                <Button
                                    emphasis='tertiary'
                                    onClick={onCatchUpClick}
                                    disabled={embeddingIdentityMismatch}
                                >
                                    <FormattedMessage defaultMessage='Catch Up'/>
                                </Button>
                            )}
                            <Button
                                emphasis='tertiary'
                                onClick={onRebuildVectorIndexClick}
                                disabled={embeddingIdentityMismatch}
                            >
                                <FormattedMessage defaultMessage='Rebuild vector index'/>
                            </Button>
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
                        <Button
                            emphasis='tertiary'
                            onClick={onHealthCheck}
                            loading={healthCheckLoading}
                        >
                            {healthCheckLoading ? (
                                <FormattedMessage defaultMessage='Refreshing...'/>
                            ) : (
                                <FormattedMessage defaultMessage='Refresh'/>
                            )}
                        </Button>

                        {healthCheckResult && (
                            <HealthCheckCard>
                                <HealthCheckRow>
                                    <HealthCheckLabel>
                                        <FormattedMessage defaultMessage='Status'/>
                                    </HealthCheckLabel>
                                    <Tag
                                        type={healthStatusTagType(healthCheckResult.status)}
                                        casing='all-caps'
                                        label={getStatusLabel(healthCheckResult.status)}
                                    />
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
