// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useState, useEffect, useCallback} from 'react';
import {useIntl} from 'react-intl';

import {doReindexPosts, getReindexStatus, cancelReindex, catchUpIndex, checkIndexHealth} from '../../../client';

import {JobStatusType, StatusMessageType, HealthCheckResultType} from './types';

export const useJobStatus = () => {
    const intl = useIntl();
    const [jobStatus, setJobStatus] = useState<JobStatusType | null>(null);
    const [statusMessage, setStatusMessage] = useState<StatusMessageType>({});
    const [polling, setPolling] = useState(false);
    const [showReindexConfirmation, setShowReindexConfirmation] = useState(false);
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
                success: false,
                message: intl.formatMessage({defaultMessage: 'Reindexing job canceled.'}),
            });
            setPolling(false);
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
            stored_dimensions: healthCheckResult.stored_dimensions,
            stored_model_name: healthCheckResult.stored_model_name,
        } : null,
        isJobStale: jobStatus?.is_stale || false,
        handleReindexClick,
        handleConfirmReindex,
        handleCancelReindex,
        handleCancelJob,
        handleCatchUpClick,
        handleHealthCheck,
        handleResumeClick,
    };
};
