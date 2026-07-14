// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {AccessControlPolicy} from '@/types/access_control';

import PolicyEditor, {PolicyEditorProps} from './policy_editor';

// Source strings use defaultMessage without ids (ids are injected at build
// time); render defaultMessage directly, matching the other component tests.
// The intl object must be referentially stable: the editor's load effect
// depends on it.
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
    getAgentAccessPolicy: jest.fn(),
    putAgentAccessPolicy: jest.fn(),
    deleteAgentAccessPolicy: jest.fn(),
    getServiceAccessPolicy: jest.fn(),
    putServiceAccessPolicy: jest.fn(),
    deleteServiceAccessPolicy: jest.fn(),
    getMCPServerAccessPolicy: jest.fn(),
    putMCPServerAccessPolicy: jest.fn(),
    deleteMCPServerAccessPolicy: jest.fn(),
    getAccessControlFields: jest.fn(),
    checkAccessControlExpression: jest.fn(),
    testAccessControlExpression: jest.fn(),
    getAccessControlVisualAST: jest.fn(),
}));

const client = jest.requireMock('@/client') as Record<string, jest.Mock>;

// Fake host editors: expose which editor rendered plus onChange/onValidate
// hooks so tests can drive expression edits.
type FakeEditorProps = {
    value: string;
    onChange: (value: string) => void;
    onValidate: (valid: boolean) => void;
};

const FakeTableEditor = ({value, onChange, onValidate}: FakeEditorProps) => (
    <div data-testid='table-editor'>
        <span data-testid='table-value'>{value}</span>
        <button onClick={() => onChange('user.attributes.team == "eng"')}>{'table-edit'}</button>
        <button onClick={() => onValidate(false)}>{'table-invalidate'}</button>
    </div>
);

const FakeCELEditor = ({value, onChange, onValidate}: FakeEditorProps) => (
    <div data-testid='cel-editor'>
        <span data-testid='cel-value'>{value}</span>
        <button onClick={() => onChange('user.attributes.level >= 3')}>{'cel-edit'}</button>
        <button onClick={() => onValidate(false)}>{'cel-invalidate'}</button>
    </div>
);

function setHostEditors() {
    (window as unknown as {Components?: Record<string, unknown>}).Components = {
        AccessControlTableEditor: FakeTableEditor,
        AccessControlCELEditor: FakeCELEditor,
    };
}

function clearHostEditors() {
    delete (window as unknown as {Components?: Record<string, unknown>}).Components;
}

const existingPolicy: AccessControlPolicy = {
    id: 'agentidaaaaaaaaaaaaaaaaaaa',
    name: 'My Agent',
    type: 'plugins/mattermost-ai.agent',
    active: true,
    create_at: 1,
    revision: 1,
    version: 'v0.2',
    imports: [],
    rules: [{actions: ['use'], expression: 'user.attributes.team == "sales"'}],
    props: {},
};

const defaultProps: PolicyEditorProps = {
    resourceType: 'agent',
    resourceId: 'agentidaaaaaaaaaaaaaaaaaaa',
    resourceDisplayName: 'My Agent',
    allowSimplified: true,
    allowAdvanced: true,
    agentIdForAuthz: 'agentidaaaaaaaaaaaaaaaaaaa',
};

function renderEditor(props: Partial<PolicyEditorProps> = {}) {
    return render(
        <IntlProvider locale='en'>
            <PolicyEditor
                {...defaultProps}
                {...props}
            />
        </IntlProvider>,
    );
}

beforeEach(() => {
    Object.values(client).forEach((mock) => mock.mockReset());
    client.getAgentAccessPolicy.mockResolvedValue(existingPolicy);
    client.getServiceAccessPolicy.mockResolvedValue(null);
    client.getMCPServerAccessPolicy.mockResolvedValue(null);
    client.getAccessControlFields.mockResolvedValue([]);
    setHostEditors();
});

afterEach(() => {
    clearHostEditors();
});

describe('PolicyEditor', () => {
    it('renders nothing when the host editors are missing', async () => {
        clearHostEditors();
        const {container} = renderEditor();
        await waitFor(() => {
            expect(container.innerHTML).toBe('');
        });
    });

    it('loads the policy and shows the simplified editor with the saved expression', async () => {
        renderEditor();

        expect(await screen.findByTestId('table-editor')).toBeTruthy();
        expect(screen.getByTestId('table-value').textContent).toBe('user.attributes.team == "sales"');
        expect(screen.queryByTestId('cel-editor')).toBeNull();
        expect(screen.getByText('Remove policy')).toBeTruthy();
    });

    it('shows the mode toggle only when both editors are allowed', async () => {
        renderEditor({allowAdvanced: false});
        expect(await screen.findByTestId('table-editor')).toBeTruthy();
        expect(screen.queryByText('Advanced')).toBeNull();
    });

    it('starts in advanced mode when the simplified editor is not allowed', async () => {
        renderEditor({allowSimplified: false});
        expect(await screen.findByTestId('cel-editor')).toBeTruthy();
        expect(screen.queryByText('Simple')).toBeNull();
    });

    it('switches editors via the toggle', async () => {
        renderEditor();
        expect(await screen.findByTestId('table-editor')).toBeTruthy();

        fireEvent.click(screen.getByText('Advanced'));
        expect(await screen.findByTestId('cel-editor')).toBeTruthy();

        fireEvent.click(screen.getByText('Simple'));
        expect(await screen.findByTestId('table-editor')).toBeTruthy();
    });

    it('saves the edited expression with the use action and route-resolved identity', async () => {
        client.putAgentAccessPolicy.mockResolvedValue({
            ...existingPolicy,
            rules: [{actions: ['use'], expression: 'user.attributes.team == "eng"'}],
        });
        renderEditor();
        expect(await screen.findByTestId('table-editor')).toBeTruthy();

        const saveButton = screen.getByText('Save policy').closest('button') as HTMLButtonElement;
        expect(saveButton.disabled).toBe(true); // not dirty yet

        fireEvent.click(screen.getByText('table-edit'));
        expect(saveButton.disabled).toBe(false);

        fireEvent.click(saveButton);
        await waitFor(() => {
            expect(client.putAgentAccessPolicy).toHaveBeenCalledTimes(1);
        });

        const [savedID, savedPolicy] = client.putAgentAccessPolicy.mock.calls[0];
        expect(savedID).toBe(defaultProps.resourceId);
        expect(savedPolicy.rules).toEqual([{actions: ['use'], expression: 'user.attributes.team == "eng"'}]);
    });

    it('keeps save disabled while the expression is invalid', async () => {
        renderEditor();
        expect(await screen.findByTestId('table-editor')).toBeTruthy();

        fireEvent.click(screen.getByText('table-edit'));
        fireEvent.click(screen.getByText('table-invalidate'));

        expect((screen.getByText('Save policy').closest('button') as HTMLButtonElement).disabled).toBe(true);
        fireEvent.click(screen.getByText('Save policy'));
        expect(client.putAgentAccessPolicy).not.toHaveBeenCalled();
    });

    it('hides the remove button when no policy exists and reports non-existence', async () => {
        client.getAgentAccessPolicy.mockResolvedValue(null);
        const onPolicyExistenceChange = jest.fn();
        renderEditor({onPolicyExistenceChange});

        expect(await screen.findByTestId('table-editor')).toBeTruthy();
        expect(screen.queryByText('Remove policy')).toBeNull();
        expect(onPolicyExistenceChange).toHaveBeenCalledWith(false);
    });

    it('deletes the policy after confirmation and reports non-existence', async () => {
        client.deleteAgentAccessPolicy.mockResolvedValue(null);
        const onPolicyExistenceChange = jest.fn();
        renderEditor({onPolicyExistenceChange});

        expect(await screen.findByTestId('table-editor')).toBeTruthy();
        expect(onPolicyExistenceChange).toHaveBeenLastCalledWith(true);

        fireEvent.click(screen.getByText('Remove policy'));
        fireEvent.click(screen.getByText('Remove'));

        await waitFor(() => {
            expect(client.deleteAgentAccessPolicy).toHaveBeenCalledWith(defaultProps.resourceId);
        });
        expect(onPolicyExistenceChange).toHaveBeenLastCalledWith(false);
        expect(screen.queryByText('Remove policy')).toBeNull();
    });

    it('locks to the advanced editor for multi-rule policies', async () => {
        client.getAgentAccessPolicy.mockResolvedValue({
            ...existingPolicy,
            rules: [
                {actions: ['use'], expression: 'user.attributes.team == "sales"'},
                {actions: ['use'], expression: 'user.attributes.level >= 2'},
            ],
        });
        renderEditor();

        expect(await screen.findByTestId('cel-editor')).toBeTruthy();
        expect(screen.queryByText('Simple')).toBeNull();
        expect(screen.getByText("This policy uses expressions the simple editor can't display.")).toBeTruthy();
    });

    it('shows a load error when the policy fetch fails', async () => {
        client.getAgentAccessPolicy.mockRejectedValue(new Error('load failed'));
        renderEditor();

        expect(await screen.findByText('Failed to load the access policy. Please try again.')).toBeTruthy();
        expect(screen.queryByTestId('table-editor')).toBeNull();
    });

    it('surfaces a save error and stays editable', async () => {
        client.putAgentAccessPolicy.mockRejectedValue(new Error('save exploded'));
        renderEditor();
        expect(await screen.findByTestId('table-editor')).toBeTruthy();

        fireEvent.click(screen.getByText('table-edit'));
        fireEvent.click(screen.getByText('Save policy'));

        expect(await screen.findByText('save exploded')).toBeTruthy();
        expect(screen.getByTestId('table-editor')).toBeTruthy();
    });

    it('routes service resources through the service policy client', async () => {
        client.getServiceAccessPolicy.mockResolvedValue(null);
        render(
            <IntlProvider locale='en'>
                <PolicyEditor
                    resourceType='service'
                    resourceId='serviceidaaaaaaaaaaaaaaaaa'
                    resourceDisplayName='OpenAI'
                    allowSimplified={false}
                    allowAdvanced={true}
                />
            </IntlProvider>,
        );

        expect(await screen.findByTestId('cel-editor')).toBeTruthy();
        expect(client.getServiceAccessPolicy).toHaveBeenCalledWith('serviceidaaaaaaaaaaaaaaaaa');
        expect(client.getAgentAccessPolicy).not.toHaveBeenCalled();
    });
});
