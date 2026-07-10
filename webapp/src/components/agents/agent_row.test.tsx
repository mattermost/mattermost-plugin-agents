// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';

import {ServiceInfo, UserAgent} from '@/types/agents';

import AgentRow from './agent_row';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
    return {
        ...actual,
        useIntl: () => intl,
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

jest.mock('@/client', () => ({
    getProfilePictureUrl: () => 'http://example.com/avatar.png',
}));

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

describe('AgentRow "Service unavailable" badge', () => {
    const cases: Array<{
        name: string;
        servicesLoaded: boolean;
        services: ServiceInfo[];
        expectBadge: boolean;
    }> = [
        {
            name: 'shows when services loaded and the agent\'s service is missing',
            servicesLoaded: true,
            services: [],
            expectBadge: true,
        },
        {
            name: 'hidden when the services list was never loaded (user lacks permission)',
            servicesLoaded: false,
            services: [],
            expectBadge: false,
        },
        {
            name: 'hidden when services loaded and the agent\'s service exists',
            servicesLoaded: true,
            services: [availableService],
            expectBadge: false,
        },
    ];

    test.each(cases)('$name', ({servicesLoaded, services, expectBadge}) => {
        render(
            <AgentRow
                agent={makeAgent()}
                services={services}
                servicesLoaded={servicesLoaded}
                canManage={false}
                onEdit={noop}
                onDelete={noop}
            />,
        );

        const badge = screen.queryByText('Service unavailable');
        if (expectBadge) {
            expect(badge).not.toBeNull();
        } else {
            expect(badge).toBeNull();
        }
    });
});
