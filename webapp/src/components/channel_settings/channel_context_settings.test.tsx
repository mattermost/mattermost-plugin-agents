// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';

import type {Channel} from '@mattermost/types/channels';

import {getChannelContext, saveChannelContext, uploadChannelKnowledgeFiles} from '@/client';
import type {ChannelSettingsTabHandlers} from '@/types/channel_settings';

import ChannelContextSettings from './channel_context_settings';

jest.mock('react-intl', () => {
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, string | number>) => {
            if (!values) {
                return defaultMessage;
            }
            return Object.entries(values).reduce(
                (message, [key, value]) => message.replace(`{${key}}`, String(value)),
                defaultMessage,
            );
        },
    };
    return {
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
        useIntl: () => intl,
    };
});

jest.mock('@/client', () => ({
    getChannelContext: jest.fn(),
    saveChannelContext: jest.fn(),
    uploadChannelKnowledgeFiles: jest.fn(),
}));

const mockGetChannelContext = getChannelContext as jest.MockedFunction<typeof getChannelContext>;
const mockSaveChannelContext = saveChannelContext as jest.MockedFunction<typeof saveChannelContext>;
const mockUploadChannelKnowledgeFiles = uploadChannelKnowledgeFiles as jest.MockedFunction<typeof uploadChannelKnowledgeFiles>;

const channel = {
    id: 'channel-1',
    team_id: 'team-1',
    type: 'O',
} as Channel;

function renderSettings() {
    const setUnsaved = jest.fn();
    let handlers: ChannelSettingsTabHandlers | null = null;
    const registerHandlers = jest.fn((next: ChannelSettingsTabHandlers | null) => {
        handlers = next;
    });

    const view = render(
        <ChannelContextSettings
            channel={channel}
            setUnsaved={setUnsaved}
            registerHandlers={registerHandlers}
        />,
    );

    return {
        setUnsaved,
        registerHandlers,
        getHandlers: () => handlers,
        unmount: view.unmount,
    };
}

describe('ChannelContextSettings', () => {
    beforeEach(() => {
        jest.clearAllMocks();
    });

    test('loads existing instructions and files without becoming dirty', async () => {
        mockGetChannelContext.mockResolvedValue({
            customInstructions: 'Use the release glossary.',
            files: [{id: 'file-1', name: 'Release_Guide.pdf', mimeType: 'application/pdf', size: 2048}],
        });
        const {setUnsaved, getHandlers} = renderSettings();

        expect(screen.getByText('Loading channel AI context…')).not.toBeNull();
        expect(await screen.findByDisplayValue('Use the release glossary.')).not.toBeNull();
        expect(screen.getByText('Release_Guide.pdf')).not.toBeNull();
        expect(screen.getByText('PDF 2 KB')).not.toBeNull();
        expect(getHandlers()).not.toBeNull();
        await waitFor(() => expect(setUnsaved).toHaveBeenLastCalledWith(false));
    });

    test('saves edited instructions through the registered host handler', async () => {
        mockGetChannelContext.mockResolvedValue({customInstructions: '', files: []});
        mockSaveChannelContext.mockResolvedValue({
            customInstructions: 'Prefer concise answers.',
            files: [],
        });
        const {setUnsaved, getHandlers} = renderSettings();

        const textarea = await screen.findByLabelText('Custom instructions');
        fireEvent.change(textarea, {target: {value: 'Prefer concise answers.'}});
        await waitFor(() => expect(setUnsaved).toHaveBeenLastCalledWith(true));

        await act(async () => {
            await getHandlers()?.save();
        });

        expect(mockSaveChannelContext).toHaveBeenCalledWith('channel-1', {
            customInstructions: 'Prefer concise answers.',
            fileIDs: [],
        });
        await waitFor(() => expect(setUnsaved).toHaveBeenLastCalledWith(false));
    });

    test('uploads, removes, and resets knowledge files', async () => {
        mockGetChannelContext.mockResolvedValue({
            customInstructions: '',
            files: [{id: 'file-1', name: 'existing.txt', mimeType: 'text/plain', size: 10}],
        });
        mockUploadChannelKnowledgeFiles.mockResolvedValue([
            {id: 'file-2', name: 'budget.xlsx', mimeType: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', size: 30720},
        ]);
        const {getHandlers, setUnsaved} = renderSettings();

        await screen.findByText('existing.txt');
        const input = screen.getByLabelText('Upload knowledge base files');
        const uploadedFile = new File(['budget'], 'budget.xlsx', {
            type: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
        });
        fireEvent.change(input, {target: {files: [uploadedFile]}});

        expect(await screen.findByText('budget.xlsx')).not.toBeNull();
        expect(mockUploadChannelKnowledgeFiles).toHaveBeenCalledWith('channel-1', [uploadedFile]);
        await waitFor(() => expect(setUnsaved).toHaveBeenLastCalledWith(true));

        fireEvent.click(screen.getByLabelText('Remove existing.txt'));
        expect(screen.queryByText('existing.txt')).toBeNull();

        act(() => {
            getHandlers()?.reset();
        });
        expect(await screen.findByText('existing.txt')).not.toBeNull();
        expect(screen.queryByText('budget.xlsx')).toBeNull();
        await waitFor(() => expect(setUnsaved).toHaveBeenLastCalledWith(false));
    });

    test('surfaces load and save failures without discarding edits', async () => {
        mockGetChannelContext.mockRejectedValueOnce(new Error('load failed'));
        const firstRender = renderSettings();
        expect((await screen.findByRole('alert')).textContent).toContain('load failed');
        expect(firstRender.getHandlers()).not.toBeNull();
        firstRender.unmount();

        mockGetChannelContext.mockResolvedValueOnce({customInstructions: '', files: []});
        mockSaveChannelContext.mockRejectedValueOnce(new Error('save failed'));
        const secondRender = renderSettings();
        const textarea = await screen.findByLabelText('Custom instructions');
        fireEvent.change(textarea, {target: {value: 'Keep this edit'}});

        await act(async () => {
            await expect(secondRender.getHandlers()?.save()).rejects.toThrow('save failed');
        });

        expect(screen.getByDisplayValue('Keep this edit')).not.toBeNull();
        expect(screen.getByRole('alert').textContent).toContain('save failed');
    });
});
