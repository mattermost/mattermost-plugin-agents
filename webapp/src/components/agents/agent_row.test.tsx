// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';

import {AgentInactiveReason, ServiceInfo, UserAgent} from '@/types/agents';

import AgentRow from './agent_row';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
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

jest.mock('react-bootstrap', () => ({
    OverlayTrigger: ({children, overlay}: {children: React.ReactNode; overlay: React.ReactNode}) => <>{children}{overlay}</>,
    Tooltip: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}), {virtual: true});

function makeAgent(): UserAgent {
    return {
        id: 'a1',
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
                onEdit={noop}
                onDelete={noop}
            />,
        );

        if (expectTooltip) {
            expect(screen.getByText('Inactive')).not.toBeNull();
            expect(screen.getByText(expectTooltip)).not.toBeNull();
        } else {
            expect(screen.queryByText('Inactive')).toBeNull();
        }
        expect(screen.getByText('Read only')).not.toBeNull();
        expect(screen.getByText('Mention @agent1 in a channel or direct message to chat with this agent.')).not.toBeNull();
    });

    test('does not show Read only badge when the user can manage the agent', () => {
        render(
            <AgentRow
                agent={makeAgent()}
                services={[availableService]}
                servicesLoaded={true}
                canManage={true}
                onEdit={noop}
                onDelete={noop}
            />,
        );

        expect(screen.queryByText('Read only')).toBeNull();
    });
});
