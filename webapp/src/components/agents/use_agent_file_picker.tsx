// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useRef} from 'react';
import styled from 'styled-components';
import {useIntl} from 'react-intl';

import {AgentExportDocument} from '@/types/agents';
import {readAgentExportFile} from '@/utils/agent_export';

/**
 * Picks and reads an agent export file. Render `input` once; `open` shows the
 * file dialog. Only the latest pick reports a result.
 */
export function useAgentFilePicker(onDocument: (doc: AgentExportDocument) => void, onError: (message: string) => void) {
    const intl = useIntl();
    const inputRef = useRef<HTMLInputElement>(null);
    const pickRef = useRef(0);

    useEffect(() => {
        return () => {
            pickRef.current += 1;
        };
    }, []);

    const open = useCallback(() => inputRef.current?.click(), []);

    const handleChange = async (e: React.ChangeEvent<HTMLInputElement>) => {
        const file = e.target.files?.[0];
        e.target.value = '';
        if (!file) {
            return;
        }
        pickRef.current += 1;
        const pick = pickRef.current;
        try {
            const doc = await readAgentExportFile(file, intl);
            if (pick === pickRef.current) {
                onDocument(doc);
            }
        } catch (err) {
            if (pick === pickRef.current) {
                onError(err instanceof Error ? err.message : String(err));
            }
        }
    };

    const input = (
        <HiddenFileInput
            ref={inputRef}
            type='file'
            accept='.json,application/json'
            onChange={handleChange}
            data-testid='agent-import-file-input'
            tabIndex={-1}
            aria-hidden={true}
        />
    );

    return {open, input};
}

// Mattermost's global input styles outrank a single class, so raise specificity.
const HiddenFileInput = styled.input`
    &&& {
        display: none;
    }
`;
