// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor, within} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {ChannelAccessLevel, UserAccessLevel} from '@/components/system_console/bot';
import {ServiceInfo} from '@/types/agents';

import {AgentDraft} from '../agent_config_view';

import ConfigTab from './config_tab';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');

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
        formatNumber: (value: number) => new Intl.NumberFormat('en').format(value),
    };
    return {
        ...actual,
        useIntl: () => intl,
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

jest.mock('react-bootstrap', () => ({
    OverlayTrigger: ({children, overlay}: {children: React.ReactNode; overlay: React.ReactNode}) => <>{children}{overlay}</>,
    Tooltip: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}), {virtual: true});

jest.mock('@/client', () => ({
    fetchModelsForAgentService: jest.fn().mockResolvedValue([]),
    getBotProfilePictureUrl: jest.fn().mockResolvedValue(''),
}));

jest.mock('@/license', () => ({
    LicenseLevel: {Unlicensed: 0, Professional: 1, Enterprise: 2, EnterpriseAdvanced: 3},
    useIsLicensedFor: jest.fn(() => true),
    useServiceLimit: jest.fn(() => null),
    useLicenseLevelName: jest.fn(() => () => 'Enterprise'),
    requiredLevelFor: jest.fn(() => 2),
}));

jest.mock('src/../../assets/bot_icon.png', () => 'placeholder-icon.png', {virtual: true});

const openaiService: ServiceInfo = {
    id: 'svc_openai',
    name: 'OpenAI Mock',
    type: 'openai',
    defaultModel: 'gpt-4.1',
    outputTokenLimit: 4096,
    useResponsesAPI: true,
};

function makeDraft(overrides: Partial<AgentDraft> = {}): AgentDraft {
    return {
        displayName: 'Test Agent',
        username: 'testagent',
        serviceId: openaiService.id,
        customInstructions: '',
        channelAccessLevel: ChannelAccessLevel.All,
        channelIds: [],
        userAccessLevel: UserAccessLevel.All,
        userIds: [],
        teamIds: [],
        adminUserIds: [],
        enabledTools: [],
        autoEnableNewMCPTools: true,
        mcpDynamicToolLoading: true,
        useServiceAccountAuth: true,
        model: '',
        enableVision: true,
        disableTools: false,
        enabledNativeTools: ['web_search'],
        reasoningEnabled: true,
        reasoningEffort: 'medium',
        thinkingBudget: 0,
        maxToolTurns: 30,
        documents: [],
        ...overrides,
    };
}

function formRowForLabel(label: string): HTMLElement {
    const labelEl = screen.getByText(label);
    const row = labelEl.closest('div');
    if (!row) {
        throw new Error(`No form row for label: ${label}`);
    }

    // ItemLabel is a <label>; climb to the FormRow grid that also holds the control.
    if (labelEl.tagName === 'LABEL' && labelEl.parentElement) {
        return labelEl.parentElement;
    }
    return row;
}

describe('ConfigTab', () => {
    // AI service, tools, dynamic loading, and native tools stay manager-editable
    // even while service account auth is on (Access / MCP grants stay locked elsewhere).
    test('keeps AI service, tools, dynamic tool loading, and native tools editable', async () => {
        render(
            <IntlProvider locale='en'>
                <ConfigTab
                    draft={makeDraft()}
                    onChange={jest.fn()}
                    onAvatarChange={jest.fn()}
                    onUploadDocuments={jest.fn()}
                    onDismissDocumentUpload={jest.fn()}
                    services={[openaiService]}
                />
            </IntlProvider>,
        );

        await waitFor(() => expect(screen.getByText('AI Service')).not.toBeNull());

        const aiServiceSelect = within(formRowForLabel('AI Service')).getByRole('combobox');
        expect((aiServiceSelect as HTMLSelectElement).disabled).toBe(false);

        fireEvent.click(screen.getByRole('button', {name: /Advanced configuration/}));

        const enableToolsRadios = within(formRowForLabel('Enable Tools')).getAllByRole('radio');
        expect(enableToolsRadios.length).toBeGreaterThan(0);
        for (const radio of enableToolsRadios) {
            expect((radio as HTMLInputElement).disabled).toBe(false);
        }

        expect((screen.getByLabelText('Dynamic tool loading') as HTMLInputElement).disabled).toBe(false);
        expect(
            (within(screen.getByTestId('native-tool-web_search')).getByRole('checkbox') as HTMLInputElement).disabled,
        ).toBe(false);
    });

    test('shows vision and effort reasoning for north without native tools', async () => {
        const northService: ServiceInfo = {
            id: 'svc_north',
            name: 'North',
            type: 'north',
            defaultModel: '',
            outputTokenLimit: 4096,
            useResponsesAPI: false,
        };

        render(
            <IntlProvider locale='en'>
                <ConfigTab
                    draft={makeDraft({serviceId: northService.id})}
                    onChange={jest.fn()}
                    onAvatarChange={jest.fn()}
                    onUploadDocuments={jest.fn()}
                    onDismissDocumentUpload={jest.fn()}
                    services={[northService]}
                />
            </IntlProvider>,
        );

        await waitFor(() => expect(screen.getByText('AI Service')).not.toBeNull());
        fireEvent.click(screen.getByRole('button', {name: /Advanced configuration/}));

        expect(screen.getByText('Enable Vision')).toBeTruthy();
        expect(screen.getByText('Enable Tools')).toBeTruthy();
        expect(screen.getByText('Reasoning')).toBeTruthy();
        expect(screen.getByText('Reasoning Effort')).toBeTruthy();
        expect(screen.queryByTestId('native-tool-web_search')).toBeNull();
        expect(screen.queryByText('Native OpenAI Tools')).toBeNull();
        expect(screen.queryByText('Structured Output')).toBeNull();
    });
});

describe('ConfigTab license gating', () => {
    const {useIsLicensedFor, useServiceLimit} = jest.requireMock('@/license') as {
        useIsLicensedFor: jest.Mock;
        useServiceLimit: jest.Mock;
    };

    beforeEach(() => {
        useIsLicensedFor.mockReturnValue(true);
        useServiceLimit.mockReturnValue(null);
    });

    test('notes that only the first service is active when the service cap is in effect', async () => {
        useServiceLimit.mockReturnValue(1);
        const second: ServiceInfo = {...openaiService, id: 'svc_other', name: 'Other'};
        render(
            <IntlProvider locale='en'>
                <ConfigTab
                    draft={makeDraft()}
                    onChange={jest.fn()}
                    onAvatarChange={jest.fn()}
                    onUploadDocuments={jest.fn()}
                    onDismissDocumentUpload={jest.fn()}
                    services={[openaiService, second]}
                />
            </IntlProvider>,
        );

        await screen.findByText('AI Service');
        expect(screen.getByText(/Only the first configured service is active/)).not.toBeNull();
        expect((screen.getByRole('option', {name: openaiService.name}) as HTMLOptionElement).disabled).toBe(false);
        expect((screen.getByRole('option', {name: 'Other'}) as HTMLOptionElement).disabled).toBe(true);
    });

    test('lets an unlicensed admin turn native web search off but not on', async () => {
        useIsLicensedFor.mockImplementation((capability: string) => capability !== 'provider_web_search');

        render(
            <IntlProvider locale='en'>
                <ConfigTab
                    draft={makeDraft({enabledNativeTools: ['web_search']})}
                    onChange={jest.fn()}
                    onAvatarChange={jest.fn()}
                    onUploadDocuments={jest.fn()}
                    onDismissDocumentUpload={jest.fn()}
                    services={[openaiService]}
                />
            </IntlProvider>,
        );

        await screen.findByText('AI Service');
        fireEvent.click(screen.getByRole('button', {name: /Advanced configuration/}));
        expect(
            (within(screen.getByTestId('native-tool-web_search')).getByRole('checkbox') as HTMLInputElement).disabled,
        ).toBe(false);
    });

    test('disables turning native web search on below Professional', async () => {
        useIsLicensedFor.mockImplementation((capability: string) => capability !== 'provider_web_search');

        render(
            <IntlProvider locale='en'>
                <ConfigTab
                    draft={makeDraft({enabledNativeTools: []})}
                    onChange={jest.fn()}
                    onAvatarChange={jest.fn()}
                    onUploadDocuments={jest.fn()}
                    onDismissDocumentUpload={jest.fn()}
                    services={[openaiService]}
                />
            </IntlProvider>,
        );

        await screen.findByText('AI Service');
        fireEvent.click(screen.getByRole('button', {name: /Advanced configuration/}));
        expect(
            (within(screen.getByTestId('native-tool-web_search')).getByRole('checkbox') as HTMLInputElement).disabled,
        ).toBe(true);
        expect(screen.getByText('Enterprise')).not.toBeNull();
    });

    test.each([
        {licensed: true, expected: ['web_search']},
        {licensed: false, expected: []},
    ])('switching to a service of another type resets native tools per license (licensed=$licensed)', async ({licensed, expected}) => {
        useIsLicensedFor.mockImplementation((capability: string) => capability !== 'provider_web_search' || licensed);
        const anthropicService: ServiceInfo = {...openaiService, id: 'svc_anthropic', name: 'Anthropic Mock', type: 'anthropic'};
        const onChange = jest.fn();
        const renderTab = (serviceId: string) => (
            <IntlProvider locale='en'>
                <ConfigTab
                    draft={makeDraft({serviceId, enabledNativeTools: ['web_fetch']})}
                    onChange={onChange}
                    onAvatarChange={jest.fn()}
                    onUploadDocuments={jest.fn()}
                    onDismissDocumentUpload={jest.fn()}
                    services={[openaiService, anthropicService]}
                />
            </IntlProvider>
        );

        const {rerender} = render(renderTab(anthropicService.id));
        await screen.findByText('AI Service');
        rerender(renderTab(openaiService.id));

        expect(onChange).toHaveBeenCalledWith(expect.objectContaining({enabledNativeTools: expected}));
    });

    describe('reference documents', () => {
        const handbook = {id: 'doc_1', name: 'handbook.pdf', mimeType: 'application/pdf', size: 2048, sha256: 'a', textRunes: 100};
        const faq = {id: 'doc_2', name: 'faq.txt', mimeType: 'text/plain', size: 10, sha256: 'b', textRunes: 5};

        function renderTab(props: Partial<React.ComponentProps<typeof ConfigTab>> = {}) {
            const handlers = {
                onChange: jest.fn(),
                onUploadDocuments: jest.fn(),
                onDismissDocumentUpload: jest.fn(),
            };
            render(
                <IntlProvider locale='en'>
                    <ConfigTab
                        draft={makeDraft({documents: [handbook, faq]})}
                        onAvatarChange={jest.fn()}
                        services={[openaiService]}
                        agentId='agent_1'
                        savedDocumentIds={['doc_1']}
                        {...handlers}
                        {...props}
                    />
                </IntlProvider>,
            );
            return handlers;
        }

        test('renders the section right below Custom instructions', async () => {
            renderTab();

            await waitFor(() => expect(screen.getByText('Custom instructions')).not.toBeNull());
            const instructions = formRowForLabel('Custom instructions');
            const section = screen.getByTestId('agent-documents');
            expect(instructions.nextElementSibling).toBe(section);
            expect(within(section).getByText('handbook.pdf')).not.toBeNull();
            expect(within(section).getByText('faq.txt')).not.toBeNull();
        });

        test('removing a document updates the draft documents', async () => {
            const {onChange} = renderTab();

            fireEvent.click(await screen.findByRole('button', {name: 'Remove handbook.pdf'}));

            expect(onChange).toHaveBeenCalledWith({documents: [faq]});
        });

        test('shows Download only for saved documents of an existing agent', async () => {
            renderTab();

            expect(await screen.findByRole('button', {name: 'Download handbook.pdf'})).not.toBeNull();
            expect(screen.queryByRole('button', {name: 'Download faq.txt'})).toBeNull();
        });

        test('uploads the chosen files and shows the editor validation error', async () => {
            const {onUploadDocuments} = renderTab({errors: {documents: 'Reference documents contain too much text.'}});
            const files = [new File(['x'], 'a.txt'), new File(['y'], 'b.md')];

            fireEvent.change(await screen.findByTestId('agent-documents-input'), {target: {files}});

            expect(onUploadDocuments).toHaveBeenCalledWith(files);
            expect(screen.getByText('Reference documents contain too much text.')).not.toBeNull();
        });
    });
});
