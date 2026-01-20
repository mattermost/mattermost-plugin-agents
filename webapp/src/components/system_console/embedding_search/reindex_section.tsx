// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage} from 'react-intl';
import styled from 'styled-components';

import {PrimaryButton, SecondaryButton, TertiaryButton} from '../../assets/buttons';

import {HelpText, ItemLabel} from '../item';

import {JobStatusType, StatusMessageType, HealthCheckResultType, ModelCompatibilityType, IncrementalStatsType} from './types';

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

const ErrorBadge = styled.span`
    display: inline-block;
    padding: 2px 8px;
    border-radius: 10px;
    font-size: 11px;
    font-weight: 600;
    background-color: rgba(var(--error-text-color-rgb), 0.16);
    color: var(--error-text);
    margin-left: 8px;
`;

interface ReindexSectionProps {
    jobStatus: JobStatusType | null;
    statusMessage: StatusMessageType;
    healthCheckResult: HealthCheckResultType | null;
    healthCheckLoading: boolean;
    modelCompatibility: ModelCompatibilityType | null;
    isJobStale: boolean;
    incrementalStats: IncrementalStatsType | null;
    onReindexClick: () => void;
    onCancelJob: () => void;
    onCatchUpClick: () => void;
    onHealthCheck: () => void;
    onResetStaleJob: () => void;
    onResetIncrementalStats: () => void;
}

export const ReindexSection = ({
    jobStatus,
    statusMessage,
    healthCheckResult,
    healthCheckLoading,
    modelCompatibility,
    isJobStale,
    incrementalStats,
    onReindexClick,
    onCancelJob,
    onCatchUpClick,
    onHealthCheck,
    onResetStaleJob,
    onResetIncrementalStats,
}: ReindexSectionProps) => {
    // Check if job is running
    const isReindexing = jobStatus?.status === 'running';

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
                            defaultMessage='The reindex job has not updated in over 30 minutes. The node running it ({nodeId}) may have crashed.'
                            values={{nodeId: jobStatus?.node_id || 'unknown'}}
                        />
                    </StaleText>
                    <SecondaryButton onClick={onResetStaleJob}>
                        <FormattedMessage defaultMessage='Reset Job'/>
                    </SecondaryButton>
                </StaleBanner>
            )}

            {/* Model Compatibility Warning */}
            {modelCompatibility && !modelCompatibility.compatible && (
                <WarningBanner>
                    <WarningIcon>{'⚠️'}</WarningIcon>
                    <WarningText>
                        <strong><FormattedMessage defaultMessage='Embedding Model Changed'/></strong>
                        <br/>
                        <FormattedMessage
                            defaultMessage='The embedding model configuration has changed ({reason}). Search functionality is disabled until you run a full reindex.'
                            values={{reason: modelCompatibility.reason}}
                        />
                    </WarningText>
                </WarningBanner>
            )}

            {/* Reindex Section */}
            <ActionContainer>
                <ItemLabel>
                    <FormattedMessage defaultMessage='Reindex All Posts'/>
                </ItemLabel>
                <div>
                    {/* Show different UI based on job status */}
                    {isReindexing ? (
                        <>
                            <ButtonGroup>
                                <SecondaryButton onClick={onCancelJob}>
                                    <FormattedMessage defaultMessage='Cancel Reindexing'/>
                                </SecondaryButton>
                            </ButtonGroup>

                            {jobStatus && (
                                <>
                                    <ProgressText>
                                        <FormattedMessage
                                            defaultMessage='Processing: {processed} of {total} posts ({percent}%)'
                                            values={{
                                                processed: jobStatus.processed_rows.toLocaleString(),
                                                total: jobStatus.total_rows.toLocaleString(),
                                                percent: jobStatus.total_rows ? Math.floor((jobStatus.processed_rows / jobStatus.total_rows) * 100) : 0,
                                            }}
                                        />
                                    </ProgressText>
                                    <ProgressContainer>
                                        <ProgressBar
                                            progress={jobStatus.total_rows ? Math.min((jobStatus.processed_rows / jobStatus.total_rows) * 100, 100) : 0}
                                        />
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
                    ) : (
                        <ButtonGroup>
                            <PrimaryButton onClick={onReindexClick}>
                                <FormattedMessage defaultMessage='Full Reindex'/>
                            </PrimaryButton>
                            <TertiaryButton onClick={onCatchUpClick}>
                                <FormattedMessage defaultMessage='Catch Up'/>
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
                        <FormattedMessage defaultMessage='Full Reindex clears the index and rebuilds from scratch. Catch Up indexes only posts created since the last successful index.'/>
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
                                <FormattedMessage defaultMessage='Checking...'/>
                            ) : (
                                <FormattedMessage defaultMessage='Check Health'/>
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
                                {healthCheckResult.error && (
                                    <ErrorHelpText>
                                        {healthCheckResult.error}
                                    </ErrorHelpText>
                                )}
                            </HealthCheckCard>
                        )}

                        <HelpText>
                            <FormattedMessage defaultMessage='Check if the number of indexed posts matches the database. This helps identify if posts are missing from the search index.'/>
                        </HelpText>
                    </div>
                </ActionContainer>
            </SectionDivider>

            {/* Incremental Indexing Stats Section */}
            <SectionDivider>
                <ActionContainer>
                    <ItemLabel>
                        <FormattedMessage defaultMessage='Real-time Indexing'/>
                        {incrementalStats && incrementalStats.error_count > 0 && (
                            <ErrorBadge>
                                <FormattedMessage
                                    defaultMessage='{count} errors'
                                    values={{count: incrementalStats.error_count}}
                                />
                            </ErrorBadge>
                        )}
                    </ItemLabel>
                    <div>
                        {incrementalStats && (
                            <HealthCheckCard>
                                <HealthCheckRow>
                                    <HealthCheckLabel>
                                        <FormattedMessage defaultMessage='Posts Indexed'/>
                                    </HealthCheckLabel>
                                    <HealthCheckValue>
                                        {incrementalStats.total_indexed.toLocaleString()}
                                    </HealthCheckValue>
                                </HealthCheckRow>
                                {incrementalStats.last_indexed_at && (
                                    <HealthCheckRow>
                                        <HealthCheckLabel>
                                            <FormattedMessage defaultMessage='Last Indexed'/>
                                        </HealthCheckLabel>
                                        <HealthCheckValue>
                                            {formatTimestamp(incrementalStats.last_indexed_at)}
                                        </HealthCheckValue>
                                    </HealthCheckRow>
                                )}
                                {incrementalStats.error_count > 0 && (
                                    <>
                                        <HealthCheckRow>
                                            <HealthCheckLabel>
                                                <FormattedMessage defaultMessage='Error Count'/>
                                            </HealthCheckLabel>
                                            <HealthCheckValue style={{color: 'var(--error-text)'}}>
                                                {incrementalStats.error_count}
                                            </HealthCheckValue>
                                        </HealthCheckRow>
                                        {incrementalStats.last_error && (
                                            <ErrorHelpText>
                                                <FormattedMessage
                                                    defaultMessage='Last error ({time}): {error}'
                                                    values={{
                                                        time: formatTimestamp(incrementalStats.last_error_at),
                                                        error: incrementalStats.last_error,
                                                    }}
                                                />
                                            </ErrorHelpText>
                                        )}
                                    </>
                                )}
                            </HealthCheckCard>
                        )}

                        {incrementalStats && (incrementalStats.total_indexed > 0 || incrementalStats.error_count > 0) && (
                            <TertiaryButton onClick={onResetIncrementalStats}>
                                <FormattedMessage defaultMessage='Reset Stats'/>
                            </TertiaryButton>
                        )}

                        <HelpText>
                            <FormattedMessage defaultMessage='Statistics for posts indexed in real-time as they are created. This does not include posts indexed during bulk reindexing.'/>
                        </HelpText>
                    </div>
                </ActionContainer>
            </SectionDivider>
        </ButtonContainer>
    );
};
