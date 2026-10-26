// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import {fireEvent, render, screen, waitFor, within} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {getCustomPrompts, updateCustomPrompt} from '@/client';
import {CustomPrompt} from '@/types';
import {SkillsConfig, normalizeSkillsConfig} from '@/types/agents';

import SkillsTab from './skills_tab';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const real = actual.createIntl({locale: 'en', defaultLocale: 'en', onError: () => null});
    const intl = {
        formatMessage: (descriptor: {id?: string; defaultMessage: string}, values?: Record<string, string | number>) =>
            real.formatMessage({id: descriptor.id ?? descriptor.defaultMessage, ...descriptor}, values),
    };
    return {
        ...actual,
        useIntl: () => intl,
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

jest.mock('@/client', () => ({
    getCustomPrompts: jest.fn(),
    updateCustomPrompt: jest.fn(),
}));

// WithTooltip renders its title alongside children so tests can assert the tooltip text.
jest.mock('@mattermost/shared/components/tooltip', () => ({
    WithTooltip: ({children, title}: {children: React.ReactNode; title?: React.ReactNode}) => (
        <>
            {children}
            <div>{title}</div>
        </>
    ),
}));

const mockGetCustomPrompts = getCustomPrompts as jest.MockedFunction<typeof getCustomPrompts>;
const mockUpdateCustomPrompt = updateCustomPrompt as jest.MockedFunction<typeof updateCustomPrompt>;

const ME = 'user_me';
const sharedID = 's'.repeat(26);
const personalID = 'p'.repeat(26);
const hiddenID = 'h'.repeat(26);

function prompt(overrides: Partial<CustomPrompt>): CustomPrompt {
    return {
        id: 'x'.repeat(26),
        creator_id: ME,
        name: 'Skill',
        description: '',
        template: 'Do it',
        is_shared: false,
        created_at: 0,
        updated_at: 0,
        deleted_at: 0,
        ...overrides,
    };
}

const sharedSkill = prompt({id: sharedID, creator_id: 'someone_else', name: 'Release notes', description: 'Write release notes', is_shared: true});
const personalSkill = prompt({id: personalID, name: 'My drafts', description: '', template: 'Draft it'});
const othersPrivateSkill = prompt({id: 'o'.repeat(26), creator_id: 'someone_else', name: 'Not mine'});

function renderTab({initial = normalizeSkillsConfig(), sharingLicensed = true}: {initial?: SkillsConfig; sharingLicensed?: boolean} = {}) {
    const changes: Array<Partial<SkillsConfig>> = [];
    let latest = initial;
    const Harness = () => {
        const [skills, setSkills] = useState(initial);
        return (
            <SkillsTab
                skills={skills}
                onChange={(updates) => {
                    changes.push(updates);
                    setSkills((prev) => {
                        latest = {...prev, ...updates};
                        return latest;
                    });
                }}
                currentUserId={ME}
                sharingLicensed={sharingLicensed}
            />
        );
    };
    render(
        <IntlProvider locale='en'>
            <Harness/>
        </IntlProvider>,
    );
    return {changes, latest: () => latest};
}

function row(name: string): HTMLElement {
    return screen.getByText(name).closest('li') as HTMLElement;
}

describe('SkillsTab', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockGetCustomPrompts.mockResolvedValue([sharedSkill, personalSkill, othersPrivateSkill]);
        mockUpdateCustomPrompt.mockResolvedValue();
    });

    test('lists shared skills and the current user\'s personal skills', async () => {
        renderTab();

        const shared = within(await screen.findByRole('region', {name: 'Shared skills'}));
        const personal = within(screen.getByRole('region', {name: 'Your personal skills'}));
        expect(shared.getByText('Release notes')).not.toBeNull();
        expect(shared.getByText('Write release notes')).not.toBeNull();
        expect(personal.getByText('My drafts')).not.toBeNull();
        expect(personal.getByText('Add a description so the agent can choose this skill')).not.toBeNull();
        expect(personal.getByText('Only you can use this skill. Share it to make it available to other users.')).not.toBeNull();
        expect(screen.queryByText('Not mine')).toBeNull();
    });

    test('toggling a shared skill keeps IDs of skills this editor cannot see', async () => {
        const {latest} = renderTab({initial: {...normalizeSkillsConfig(), enabledSharedIDs: [hiddenID]}});

        fireEvent.click(await screen.findByRole('switch', {name: 'Enable skill Release notes'}));
        expect(latest().enabledSharedIDs).toEqual([hiddenID, sharedID]);

        fireEvent.click(screen.getByRole('switch', {name: 'Disable skill Release notes'}));
        expect(latest().enabledSharedIDs).toEqual([hiddenID]);
    });

    test('description overrides are trimmed on blur and removed when cleared', async () => {
        const {latest} = renderTab({initial: {...normalizeSkillsConfig(), descriptionOverrides: {[hiddenID]: 'kept'}}});

        const input = within(await screen.findByRole('region', {name: 'Shared skills'})).getByLabelText('Description override') as HTMLInputElement;
        expect(input.maxLength).toBe(1024);

        fireEvent.change(input, {target: {value: '  Use for releases  '}});
        fireEvent.blur(input);
        expect(latest().descriptionOverrides).toEqual({[hiddenID]: 'kept', [sharedID]: 'Use for releases'});

        fireEvent.change(input, {target: {value: '   '}});
        expect(latest().descriptionOverrides).toEqual({[hiddenID]: 'kept'});
    });

    test('an override hides the missing description hint', async () => {
        renderTab({initial: {...normalizeSkillsConfig(), descriptionOverrides: {[personalID]: 'Use for drafts'}}});

        await screen.findByText('My drafts');
        expect(screen.queryByText('Add a description so the agent can choose this skill')).toBeNull();
    });

    test('sharing a personal skill saves it as shared and enables it for this agent', async () => {
        const {latest} = renderTab({initial: {...normalizeSkillsConfig(), enabledSharedIDs: [hiddenID]}});

        fireEvent.click(await screen.findByRole('button', {name: 'Share skill My drafts'}));

        await waitFor(() => expect(within(screen.getByRole('region', {name: 'Shared skills'})).queryByText('My drafts')).not.toBeNull());
        expect(mockUpdateCustomPrompt).toHaveBeenCalledWith(personalID, {name: 'My drafts', description: '', template: 'Draft it', is_shared: true});
        expect(latest().enabledSharedIDs).toEqual([hiddenID, personalID]);
        expect(within(row('My drafts')).getByRole('switch', {name: 'Disable skill My drafts'})).not.toBeNull();
    });

    test('sharing sends the stored skill, not the copy loaded with the tab', async () => {
        renderTab();
        const button = await screen.findByRole('button', {name: 'Share skill My drafts'});
        mockGetCustomPrompts.mockResolvedValue([sharedSkill, {...personalSkill, template: 'Edited elsewhere'}, othersPrivateSkill]);

        fireEvent.click(button);

        await waitFor(() => expect(mockUpdateCustomPrompt).toHaveBeenCalledWith(personalID, {name: 'My drafts', description: '', template: 'Edited elsewhere', is_shared: true}));
    });

    test('a failed share leaves the skill personal and reports the error', async () => {
        mockUpdateCustomPrompt.mockRejectedValue(new Error('nope'));
        const {changes} = renderTab();

        fireEvent.click(await screen.findByRole('button', {name: 'Share skill My drafts'}));

        expect(await screen.findByText('Failed to share the skill. Please try again.')).not.toBeNull();
        expect(within(screen.getByRole('region', {name: 'Your personal skills'})).getByText('My drafts')).not.toBeNull();
        expect(changes).toEqual([]);
    });

    test('hides Share when sharing is not licensed', async () => {
        renderTab({sharingLicensed: false});

        await screen.findByText('My drafts');
        expect(screen.queryByRole('button', {name: /^Share skill/})).toBeNull();
    });

    test('turning skills off hides the rest of the settings', async () => {
        const {latest} = renderTab();
        await screen.findByText('Release notes');

        fireEvent.click(screen.getByRole('switch', {name: 'Enable skills'}));

        expect(latest().disabled).toBe(true);
        expect(screen.queryByRole('switch', {name: 'Allow personal skills'})).toBeNull();
        expect(screen.queryByText('Release notes')).toBeNull();
    });

    test('allow personal skills maps to allowPersonal', async () => {
        const {latest} = renderTab();
        await screen.findByText('Release notes');

        fireEvent.click(screen.getByRole('switch', {name: 'Allow personal skills'}));
        expect(latest().allowPersonal).toBe(true);
    });

    test('shows an empty state when there are no skills', async () => {
        mockGetCustomPrompts.mockResolvedValue([]);
        renderTab();

        expect(await screen.findByText('No skills yet')).not.toBeNull();
        expect(screen.getByText('Create skills from Skills in the AI actions menu of the message box.')).not.toBeNull();
    });

    test('reports a failed load', async () => {
        mockGetCustomPrompts.mockRejectedValue(new Error('boom'));
        renderTab();

        expect(await screen.findByText('Failed to load skills.')).not.toBeNull();
    });
});
