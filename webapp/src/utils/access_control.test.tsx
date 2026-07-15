// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen, waitFor} from '@testing-library/react';

import {getAccessControlEditors, resetABACSupportCacheForTesting, useABACSupport} from './access_control';

jest.mock('@/client/access_control', () => ({
    getABACStatus: jest.fn(),
}));

const {getABACStatus} = jest.requireMock('@/client/access_control') as {
    getABACStatus: jest.Mock;
};

const FakeEditor = () => null;

function setWindowComponents(components: Record<string, unknown>) {
    (window as unknown as {Components?: Record<string, unknown>}).Components = components;
}

function clearWindowComponents() {
    delete (window as unknown as {Components?: Record<string, unknown>}).Components;
}

// Probe surfaces the hook state as text for assertions.
const SupportProbe = () => {
    const {supported, loading} = useABACSupport();
    return <div>{`supported:${supported} loading:${loading}`}</div>;
};

beforeEach(() => {
    resetABACSupportCacheForTesting();
    getABACStatus.mockReset();
    clearWindowComponents();
});

afterEach(() => {
    clearWindowComponents();
});

describe('getAccessControlEditors', () => {
    it('returns both editors when the host webapp exports them', () => {
        setWindowComponents({
            AccessControlTableEditor: FakeEditor,
            AccessControlCELEditor: FakeEditor,
        });

        const editors = getAccessControlEditors();
        expect(editors).not.toBeNull();
        expect(editors?.TableEditor).toBe(FakeEditor);
        expect(editors?.CELEditor).toBe(FakeEditor);
    });

    it('returns null when window.Components is missing', () => {
        expect(getAccessControlEditors()).toBeNull();
    });

    it.each([
        ['table editor missing', {AccessControlCELEditor: FakeEditor}],
        ['CEL editor missing', {AccessControlTableEditor: FakeEditor}],
        ['both missing', {}],
    ])('returns null when %s', (_name, components) => {
        setWindowComponents(components);
        expect(getAccessControlEditors()).toBeNull();
    });
});

describe('useABACSupport', () => {
    it('reports unsupported without probing the server when editors are missing', () => {
        render(<SupportProbe/>);

        expect(screen.getByText('supported:false loading:false')).toBeTruthy();
        expect(getABACStatus).not.toHaveBeenCalled();
    });

    it('reports supported when editors exist and the server reports available', async () => {
        setWindowComponents({
            AccessControlTableEditor: FakeEditor,
            AccessControlCELEditor: FakeEditor,
        });
        getABACStatus.mockResolvedValue({available: true});

        render(<SupportProbe/>);

        await waitFor(() => {
            expect(screen.getByText('supported:true loading:false')).toBeTruthy();
        });
    });

    it('reports unsupported when the server reports unavailable', async () => {
        setWindowComponents({
            AccessControlTableEditor: FakeEditor,
            AccessControlCELEditor: FakeEditor,
        });
        getABACStatus.mockResolvedValue({available: false});

        render(<SupportProbe/>);

        await waitFor(() => {
            expect(screen.getByText('supported:false loading:false')).toBeTruthy();
        });
    });

    it('reports unsupported when the status request fails', async () => {
        setWindowComponents({
            AccessControlTableEditor: FakeEditor,
            AccessControlCELEditor: FakeEditor,
        });
        getABACStatus.mockRejectedValue(new Error('boom'));

        render(<SupportProbe/>);

        await waitFor(() => {
            expect(screen.getByText('supported:false loading:false')).toBeTruthy();
        });
    });

    it('shares one status request across consumers', async () => {
        setWindowComponents({
            AccessControlTableEditor: FakeEditor,
            AccessControlCELEditor: FakeEditor,
        });
        getABACStatus.mockResolvedValue({available: true});

        render(
            <>
                <SupportProbe/>
                <SupportProbe/>
            </>,
        );

        await waitFor(() => {
            expect(screen.getAllByText('supported:true loading:false')).toHaveLength(2);
        });
        expect(getABACStatus).toHaveBeenCalledTimes(1);
    });
});
