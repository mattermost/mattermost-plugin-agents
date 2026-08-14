// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useState, useEffect, useCallback} from 'react';
import {useIntl} from 'react-intl';

import {doReindexPosts, getReindexStatus, cancelReindex, catchUpIndex, checkIndexHealth, rebuildVectorIndex} from '../../../client';

import {JobStatusType, StatusMessageType, HealthCheckResultType} from './types';

export const useJobStatus = () => {
    const intl = useIntl();
    const [jobStatus, setJobStatus] = useState<JobStatusType | null>(null);
    const [statusMessage, setStatusMessage] = useState<StatusMessageType>({});
    const [polling, setPolling] = useState(false);
    const [showReindexConfirmation, setShowReindexConfirmation] = useState(false);
    const [showRebuildConfirmation, setShowRebuildConfirmation] = useState(false);
    const [healthCheckResult, setHealthCheckResult] = useState<HealthCheckResultType | null>(null);
    const [healthCheckLoading, setHealthCheckLoading] = useState(false);

    // Function to fetch job status
    const fetchJobStatus = useCallback(async () => {
        try {
            const status = await getReindexStatus();
            setJobStatus(status);

            // Handle different status conditions
            if (status.status === 'completed') {
                setStatusMessage({
                    success: true,
                    message: intl.formatMessage({defaultMessage: 'Posts reindexing completed successfully.'}),
                });
                setPolling(false);
            } else if (status.status === 'failed') {
                setStatusMessage({
                    success: false,
                    message: intl.formatMessage(
                        {defaultMessage: 'Failed to reindex posts: {error}'},
                        {error: status.error || intl.formatMessage({defaultMessage: 'Unknown error'})},
                    ),
                });
                setPolling(false);
            } else if (status.status === 'canceled') {
                setStatusMessage({
                    success: false,
                    message: intl.formatMessage({defaultMessage: 'Reindexing was canceled.'}),
                });
                setPolling(false);
            } else if (status.status === 'running' || status.status === 'cancel_requested') {
                // Page refresh must re-attach to an in-flight job.
                setPolling(true);
            }
        } catch (error) {
            // 404 is expected when no job has run yet, don't show an error
            if (error && typeof error === 'object' && 'status_code' in error && error.status_code !== 404) {
                setStatusMessage({
                    success: false,
                    message: intl.formatMessage({defaultMessage: 'Failed to get reindexing status.'}),
                });
            }
            setPolling(false);
        }
    }, [intl]);

    // Polling effect for job status
    useEffect(() => {
        if (polling) {
            const interval = setInterval(() => {
                fetchJobStatus();
            }, 2000); // Poll every 2 seconds

            return () => clearInterval(interval);
        }

        // Return a noop function
        return function noop() { /* No cleanup needed */ };
    }, [polling, fetchJobStatus]);

    // Check status on component mount
    useEffect(() => {
        fetchJobStatus();
        handleHealthCheck();
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [fetchJobStatus]);

    // Refresh health check when job completes
    useEffect(() => {
        if (jobStatus?.status === 'completed') {
            handleHealthCheck();
        }
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [jobStatus?.status]);

    const handleReindexClick = () => {
        setShowReindexConfirmation(true);
    };

    const handleConfirmReindex = async () => {
        setShowReindexConfirmation(false);
        setStatusMessage({});

        try {
            const response = await doReindexPosts(true);
            setJobStatus(response);
            setPolling(true);
        } catch (error) {
            setStatusMessage({
                success: false,
                message: intl.formatMessage({defaultMessage: 'Failed to start reindexing. Please try again.'}),
            });
        }
    };

    const handleCancelReindex = () => {
        setShowReindexConfirmation(false);
    };

    const handleResumeClick = async () => {
        setStatusMessage({});

        try {
            const response = await doReindexPosts(false); // Resume from checkpoint
            setJobStatus(response);
            setPolling(true);
        } catch (error) {
            setStatusMessage({
                success: false,
                message: intl.formatMessage({defaultMessage: 'Failed to resume reindexing. Please try again.'}),
            });
        }
    };

    const handleCancelJob = async () => {
        try {
            const response = await cancelReindex();
            setJobStatus(response);
            setStatusMessage({
                success: true,
                message: intl.formatMessage({defaultMessage: 'Cancel requested. Waiting for the reindexing job to stop…'}),
            });

            // Keep polling so the UI surfaces the worker's transition to
            // the terminal canceled state.
            setPolling(true);
        } catch (error) {
            setStatusMessage({
                success: false,
                message: intl.formatMessage({defaultMessage: 'Failed to cancel reindexing job.'}),
            });
        }
    };

    const handleCatchUpClick = async () => {
        setStatusMessage({});

        try {
            const response = await catchUpIndex();
            setJobStatus(response);
            setPolling(true);
        } catch (error) {
            setStatusMessage({
                success: false,
                message: intl.formatMessage({defaultMessage: 'Failed to start catch-up indexing. Make sure a full reindex has been completed first.'}),
            });
        }
    };

    const handleRebuildVectorIndexClick = () => {
        setShowRebuildConfirmation(true);
    };

    const handleConfirmRebuildVectorIndex = async () => {
        setShowRebuildConfirmation(false);
        setStatusMessage({});

        try {
            const response = await rebuildVectorIndex();
            setJobStatus(response);
            setPolling(true);
        } catch (error) {
            setStatusMessage({
                success: false,
                message: intl.formatMessage({defaultMessage: 'Failed to start vector index rebuild. Please try again.'}),
            });
        }
    };

    const handleCancelRebuildVectorIndex = () => {
        setShowRebuildConfirmation(false);
    };

    const handleHealthCheck = async () => {
        setHealthCheckLoading(true);
        setHealthCheckResult(null);

        try {
            const result = await checkIndexHealth();
            if (result.status === 'not_configured') {
                // Search not configured yet - don't show as error
                setHealthCheckResult(null);
            } else if (result.status === 'init_error') {
                setStatusMessage({
                    success: false,
                    message: intl.formatMessage(
                        {defaultMessage: 'Search initialization failed: {error}'},
                        {error: result.error || intl.formatMessage({defaultMessage: 'Unknown error'})},
                    ),
                });
            } else {
                setHealthCheckResult(result);
            }
        } catch (error) {
            setStatusMessage({
                success: false,
                message: intl.formatMessage({defaultMessage: 'Failed to check index health.'}),
            });
        } finally {
            setHealthCheckLoading(false);
        }
    };

    return {
        jobStatus,
        statusMessage,
        polling,
        showReindexConfirmation,
        healthCheckResult,
        healthCheckLoading,
        modelCompatibility: healthCheckResult ? {
            compatible: healthCheckResult.model_compatible,
            needs_reindex: healthCheckResult.model_needs_reindex,
            reason: healthCheckResult.model_compat_reason,
            stored_provider_type: healthCheckResult.stored_provider_type,
            stored_dimensions: healthCheckResult.stored_dimensions,
            stored_model_name: healthCheckResult.stored_model_name,
            stored_hnsw_m: healthCheckResult.stored_hnsw_m,
            stored_vector_element_type: healthCheckResult.stored_vector_element_type,
        } : null,
        isJobStale: jobStatus?.is_stale || false,
        showRebuildConfirmation,
        handleReindexClick,
        handleConfirmReindex,
        handleCancelReindex,
        handleCancelJob,
        handleCatchUpClick,
        handleRebuildVectorIndexClick,
        handleConfirmRebuildVectorIndex,
        handleCancelRebuildVectorIndex,
        handleHealthCheck,
        handleResumeClick,
    };
};
