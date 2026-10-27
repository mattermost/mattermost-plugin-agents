// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';

import {AgentInactiveReason, ServiceInfo, UserAgent} from '@/types/agents';

import AgentRow from './agent_row';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, string>) => (
            defaultMessage.replace(/\{(\w+)\}/g, (_, key: string) => values?.[key] ?? `{${key}}`)
        ),
    };
    return {
        ...actual,
        useIntl: () => intl,
        FormattedMessage: ({defaultMessage, values}: {defaultMessage: string; values?: Record<string, string>}) => {
            if (!values) {
                return defaultMessage;
            }
            return defaultMessage.replace(/\{(\w+)\}/g, (_, key: string) => values[key] ?? `{${key}}`);
        },
    };
});

jest.mock('@/client', () => ({
    getProfilePictureUrl: () => 'http://example.com/avatar.png',
}));

jest.mock('@mattermost/shared/components/tooltip', () => ({
    WithTooltip: ({children, title}: {children: React.ReactNode; title?: React.ReactNode}) => (
        <>
            {children}
            <div>{title}</div>
        </>
    ),
}));

function makeAgent(): UserAgent {
    return {
        id: 'a1',
        botUserID: 'bot-1',
        name: 'agent1',
        displayName: 'Agent One',
        serviceID: 'svc-1',
        autoEnableNewMCPTools: false,
        enabledMCPTools: null,
    } as UserAgent;
}

const availableService: ServiceInfo = {
    id: 'svc-1',
    name: 'Configured Service',
    type: 'openai',
    defaultModel: 'gpt-4',
    outputTokenLimit: 0,
    useResponsesAPI: false,
};

const noop = () => { /* no-op */ };

describe('AgentRow inactive badge', () => {
    const cases: Array<{
        name: string;
        inactiveReason?: AgentInactiveReason;
        servicesLoaded: boolean;
        services: ServiceInfo[];
        expectTooltip: string | null;
    }> = [
        {
            name: 'explains a service that is not active on the plan',
            inactiveReason: 'service_not_licensed',
            servicesLoaded: true,
            services: [availableService],
            expectTooltip: 'This agent uses an LLM service that is not active on your current plan. Only the first configured service is active; multiple LLM services are available on Enterprise plans and above. Edit the agent to choose the active service.',
        },
        {
            name: 'explains an agent over the agent limit',
            inactiveReason: 'agent_limit',
            servicesLoaded: true,
            services: [availableService],
            expectTooltip: 'Your current plan has reached its limit of active AI agents, and agents created earlier take the available slots. Delete an earlier agent, or upgrade your plan for more agents.',
        },
        {
            name: 'explains an unavailable service reported by the server',
            inactiveReason: 'service_unavailable',
            servicesLoaded: false,
            services: [],
            expectTooltip: 'This agent’s LLM service was deleted or is missing required settings. Edit the agent to choose another service, or complete the service configuration.',
        },
        {
            name: 'explains an invalid configuration',
            inactiveReason: 'invalid_config',
            servicesLoaded: true,
            services: [availableService],
            expectTooltip: 'This agent’s configuration is incomplete. Edit the agent to fix it.',
        },
        {
            name: 'flags a deleted service once services have loaded',
            servicesLoaded: true,
            services: [],
            expectTooltip: 'This agent’s LLM service was deleted or is missing required settings. Edit the agent to choose another service, or complete the service configuration.',
        },
        {
            name: 'hidden when the services list was never loaded (user lacks permission)',
            servicesLoaded: false,
            services: [],
            expectTooltip: null,
        },
        {
            name: 'hidden for an active agent',
            servicesLoaded: true,
            services: [availableService],
            expectTooltip: null,
        },
    ];

    test.each(cases)('$name', ({inactiveReason, servicesLoaded, services, expectTooltip}) => {
        render(
            <AgentRow
                agent={{...makeAgent(), inactiveReason}}
                services={services}
                servicesLoaded={servicesLoaded}
                canManage={false}
                onStartChat={noop}
                onEdit={noop}
                onDelete={noop}
                onExport={noop}
            />,
        );

        if (expectTooltip) {
            expect(screen.getByText('Inactive')).not.toBeNull();
            expect(screen.getByText(expectTooltip)).not.toBeNull();
        } else {
            expect(screen.queryByText('Inactive')).toBeNull();
        }
    });
});

describe('AgentRow actions menu', () => {
    test.each([
        {action: 'Edit', expectEdit: 1, expectDelete: 0, expectExport: 0},
        {action: 'Export', expectEdit: 0, expectDelete: 0, expectExport: 1},
        {action: 'Delete', expectEdit: 0, expectDelete: 1, expectExport: 0},
    ])('$action runs only its own callback', ({action, expectEdit, expectDelete, expectExport}) => {
        const onEdit = jest.fn();
        const onDelete = jest.fn();
        const onExport = jest.fn();
        const onStartChat = jest.fn();
        render(
            <AgentRow
                agent={makeAgent()}
                services={[availableService]}
                servicesLoaded={true}
                canManage={true}
                onStartChat={onStartChat}
                onEdit={onEdit}
                onDelete={onDelete}
                onExport={onExport}
            />,
        );

        fireEvent.click(screen.getByRole('button', {name: 'Agent actions'}));
        fireEvent.click(screen.getByRole('button', {name: action}));

        expect(onEdit).toHaveBeenCalledTimes(expectEdit);
        expect(onDelete).toHaveBeenCalledTimes(expectDelete);
        expect(onExport).toHaveBeenCalledTimes(expectExport);
        expect(onStartChat).not.toHaveBeenCalled();
    });

    test('Enter on the actions button opens the menu without editing the agent or starting a chat', () => {
        const onEdit = jest.fn();
        const onStartChat = jest.fn();
        render(
            <AgentRow
                agent={makeAgent()}
                services={[availableService]}
                servicesLoaded={true}
                canManage={true}
                onStartChat={onStartChat}
                onEdit={onEdit}
                onDelete={noop}
                onExport={noop}
            />,
        );

        fireEvent.keyDown(screen.getByRole('button', {name: 'Agent actions'}), {key: 'Enter'});

        expect(onEdit).not.toHaveBeenCalled();
        expect(onStartChat).not.toHaveBeenCalled();
    });

    test('has no actions menu (the editor has no read-only mode) for non-managers', () => {
        render(
            <AgentRow
                agent={makeAgent()}
                services={[availableService]}
                servicesLoaded={true}
                canManage={false}
                onStartChat={noop}
                onEdit={noop}
                onDelete={noop}
                onExport={noop}
            />,
        );

        expect(screen.queryByRole('button', {name: 'Agent actions'})).toBeNull();
        expect(screen.queryByRole('button', {name: 'Export'})).toBeNull();
    });
});

describe('AgentRow primary action', () => {
    const rowName = 'Start a new chat with Agent One';

    const renderRow = (props: Partial<React.ComponentProps<typeof AgentRow>> = {}) => render(
        <AgentRow
            agent={makeAgent()}
            services={[availableService]}
            servicesLoaded={true}
            canManage={false}
            onStartChat={noop}
            onEdit={noop}
            onDelete={noop}
            onExport={noop}
            {...props}
        />,
    );

    test.each([
        {who: 'manager', canManage: true, how: 'click'},
        {who: 'non-manager', canManage: false, how: 'click'},
        {who: 'manager', canManage: true, how: 'Enter'},
        {who: 'non-manager', canManage: false, how: 'Enter'},
        {who: 'manager', canManage: true, how: ' '},
        {who: 'non-manager', canManage: false, how: ' '},
    ])('$who: "$how" starts a new chat with the bot user id', ({canManage, how}) => {
        const onStartChat = jest.fn();
        const onEdit = jest.fn();
        renderRow({canManage, onStartChat, onEdit});

        const row = screen.getByRole('button', {name: rowName});
        if (how === 'click') {
            fireEvent.click(row);
        } else {
            fireEvent.keyDown(row, {key: how});
        }

        expect(onStartChat).toHaveBeenCalledTimes(1);
        expect(onStartChat).toHaveBeenCalledWith('bot-1');
        expect(onEdit).not.toHaveBeenCalled();
    });

    test('is keyboard focusable', () => {
        renderRow();
        expect(screen.getByRole('button', {name: rowName}).getAttribute('tabindex')).toBe('0');
    });

    test('clicking the actions button does not start a chat', () => {
        const onStartChat = jest.fn();
        renderRow({canManage: true, onStartChat});

        fireEvent.click(screen.getByRole('button', {name: 'Agent actions'}));

        expect(onStartChat).not.toHaveBeenCalled();
    });

    test.each([
        {name: 'empty', agent: {...makeAgent(), botUserID: ''}},
        {name: 'missing',
            agent: (() => {
                const a = makeAgent();
                delete a.botUserID;
                return a;
            })()},
    ])('is not actionable when the bot user id is $name', ({agent}) => {
        const onStartChat = jest.fn();
        renderRow({agent, onStartChat});

        const row = screen.getByRole('button', {name: rowName});
        fireEvent.click(row);
        fireEvent.keyDown(row, {key: 'Enter'});

        expect(onStartChat).not.toHaveBeenCalled();
        expect(row.getAttribute('aria-disabled')).toBe('true');
        const describedBy = row.getAttribute('aria-describedby') ?? '';
        expect(document.getElementById(describedBy)?.textContent).toBe('This agent has no bot account, so a chat cannot be started.');
    });
});
