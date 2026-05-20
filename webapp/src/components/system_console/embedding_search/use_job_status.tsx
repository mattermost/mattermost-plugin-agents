// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useState, useEffect, useCallback} from 'react';
import {IntlShape, useIntl} from 'react-intl';

import {doReindexPosts, getReindexStatus, cancelReindex, catchUpIndex, checkIndexHealth} from '../../../client';

import {JobStatusType, StatusMessageType, HealthCheckResultType} from './types';

// Sentinel for native fetch failures, which carry no HTTP status. Using -1
// instead of undefined lets callers branch on a single value.
export const NO_STATUS = -1;

export const extractStatusCode = (err: unknown): number => {
    if (err && typeof err === 'object' && 'status_code' in err) {
        const code = (err as {status_code?: unknown}).status_code;
        if (typeof code === 'number') {
            return code;
        }
    }
    return NO_STATUS;
};

export const extractServerMessage = (err: unknown): string => {
    if (err && typeof err === 'object' && 'message' in err) {
        const msg = (err as {message?: unknown}).message;
        if (typeof msg === 'string') {
            return msg;
        }
    }
    return '';
};

// Maps a thrown ClientError-like value to a single admin-actionable sentence.
export const formatReindexError = (err: unknown, intl: IntlShape, actionLabel: string): string => {
    const status = extractStatusCode(err);
    const serverMsg = extractServerMessage(err);

    switch (status) {
    case 401:
        return intl.formatMessage({defaultMessage: 'Your session has expired. Reload the page and sign in again.'});
    case 403:
        return intl.formatMessage({defaultMessage: 'System administrator privileges are required to reindex.'});
    case 409:
        return serverMsg || intl.formatMessage({defaultMessage: 'A reindex job is already running. Wait for it to finish, or cancel it before starting a new one.'});
    case NO_STATUS:
        return intl.formatMessage(
            {defaultMessage: '{action} could not reach the server. Check your connection and try again.'},
            {action: actionLabel},
        );
    default:
        if (serverMsg) {
            return intl.formatMessage(
                {defaultMessage: '{action} failed: {error}'},
                {action: actionLabel, error: serverMsg},
            );
        }
        return intl.formatMessage(
            {defaultMessage: '{action} failed. Check the server logs and try again.'},
            {action: actionLabel},
        );
    }
};

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
            if (extractStatusCode(error) !== 404) {
                setStatusMessage({
                    success: false,
                    message: formatReindexError(
                        error,
                        intl,
                        intl.formatMessage({defaultMessage: 'Fetching reindex status'}),
                    ),
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
                message: formatReindexError(
                    error,
                    intl,
                    intl.formatMessage({defaultMessage: 'Reindexing'}),
                ),
            });

            // 409 means a job is already running; surface its progress.
            if (extractStatusCode(error) === 409) {
                fetchJobStatus();
            }
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
                message: formatReindexError(
                    error,
                    intl,
                    intl.formatMessage({defaultMessage: 'Resuming reindexing'}),
                ),
            });
            if (extractStatusCode(error) === 409) {
                fetchJobStatus();
            }
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
                message: formatReindexError(
                    error,
                    intl,
                    intl.formatMessage({defaultMessage: 'Canceling reindexing'}),
                ),
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
                message: formatReindexError(
                    error,
                    intl,
                    intl.formatMessage({defaultMessage: 'Catch-up indexing'}),
                ),
            });
            if (extractStatusCode(error) === 409) {
                fetchJobStatus();
            }
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
                message: formatReindexError(
                    error,
                    intl,
                    intl.formatMessage({defaultMessage: 'Index health check'}),
                ),
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
