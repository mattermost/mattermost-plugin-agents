// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useEffect, useRef, useState} from 'react';

import {getMCPTools} from '../../client';

import {MCPToolsResponse} from './mcp_types';

export type MCPToolsList = {

    // The latest list loaded, kept while a newer one loads; null until the
    // first load succeeds.
    toolsData: MCPToolsResponse | null;

    // Why the latest load failed, or null once a load succeeds.
    error: string | null;

    // Loads the list again. Only the latest load's result is applied, so a
    // slower, older load never replaces a newer one. It never rejects: a
    // failure is reported through error.
    refresh: () => Promise<void>;
};

// useMCPToolsList is the single owner of the admin MCP tools list.
export const useMCPToolsList = (): MCPToolsList => {
    const [toolsData, setToolsData] = useState<MCPToolsResponse | null>(null);
    const [error, setError] = useState<string | null>(null);
    const requestRef = useRef(0);

    useEffect(() => () => {
        requestRef.current++;
    }, []);

    const refresh = useCallback(async () => {
        const request = ++requestRef.current;
        try {
            const response = await getMCPTools();
            if (request === requestRef.current) {
                setToolsData(response);
                setError(null);
            }
        } catch (err) {
            if (request === requestRef.current) {
                setError(err instanceof Error ? err.message : '');
            }
        }
    }, []);

    return {toolsData, error, refresh};
};
