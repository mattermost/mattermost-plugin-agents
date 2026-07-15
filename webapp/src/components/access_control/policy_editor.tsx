// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {Suspense, useCallback, useEffect, useMemo, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {
    checkAccessControlExpression,
    deleteAgentAccessPolicy,
    deleteMCPServerAccessPolicy,
    deleteServiceAccessPolicy,
    getAccessControlFields,
    getAccessControlVisualAST,
    getAgentAccessPolicy,
    getMCPServerAccessPolicy,
    getServiceAccessPolicy,
    putAgentAccessPolicy,
    putMCPServerAccessPolicy,
    putServiceAccessPolicy,
    testAccessControlExpression,
} from '@/client/access_control';
import {AccessControlPolicy, AccessControlPropertyField, PolicyResourceType} from '@/types/access_control';
import type {ActionResult, CELEditorActions, CELEditorAttribute, TableEditorActions} from '@/types/access_control_editors';
import {getAccessControlEditors} from '@/utils/access_control';
import {PrimaryButton, TertiaryButton} from '@/components/assets/buttons';
import LoadingSpinner from '@/components/assets/loading_spinner';
import ConfirmationDialog from '@/components/confirmation_dialog';

export type PolicyEditorProps = {
    resourceType: PolicyResourceType;
    resourceId: string;
    resourceDisplayName: string;

    // Agent tab: simplified (table) editor for creators/agent admins.
    allowSimplified: boolean;

    // System admins additionally get the advanced (CEL) editor.
    allowAdvanced: boolean;

    // Forwarded as ?agent_id= on CEL calls (per-agent-admin authz lane).
    agentIdForAuthz?: string;
};

// EditorMode is the user-selectable editor. The rendered view additionally
// models the case where the policy can't render in the table editor and the
// caller may not use the advanced (CEL) editor: a read-only unsupported state
// (F6) — the CEL editor must never render for non-admin callers.
type EditorMode = 'simplified' | 'advanced';
type EditorView = EditorMode | 'unsupported';

// wrapAction adapts plugin client promises onto the ActionResult shape
// ({data} / {error}) the host webapp's editors expect.
function wrapAction<Args extends unknown[], T>(fn: (...args: Args) => Promise<T>): (...args: Args) => Promise<ActionResult<T, Error>> {
    return async (...args: Args) => {
        try {
            return {data: await fn(...args)};
        } catch (e) {
            return {error: e instanceof Error ? e : new Error(String(e))};
        }
    };
}

function policyClientFor(resourceType: PolicyResourceType) {
    switch (resourceType) {
    case 'agent':
        return {get: getAgentAccessPolicy, put: putAgentAccessPolicy, del: deleteAgentAccessPolicy};
    case 'service':
        return {get: getServiceAccessPolicy, put: putServiceAccessPolicy, del: deleteServiceAccessPolicy};
    case 'mcp':
        return {get: getMCPServerAccessPolicy, put: putMCPServerAccessPolicy, del: deleteMCPServerAccessPolicy};
    default: {
        const exhaustive: never = resourceType;
        throw new Error(`unknown resource type: ${exhaustive}`);
    }
    }
}

const DELETE_POLICY_TITLE_ID = 'delete-access-policy-title';

const PolicyEditor = (props: PolicyEditorProps) => {
    const {resourceType, resourceId, resourceDisplayName, allowSimplified, allowAdvanced, agentIdForAuthz} = props;
    const intl = useIntl();
    const editors = getAccessControlEditors();
    const client = useMemo(() => policyClientFor(resourceType), [resourceType]);

    const [loading, setLoading] = useState(true);
    const [loadError, setLoadError] = useState('');
    const [policy, setPolicy] = useState<AccessControlPolicy | null>(null);
    const [expression, setExpression] = useState('');
    const [savedExpression, setSavedExpression] = useState('');
    const [expressionValid, setExpressionValid] = useState(true);
    const [mode, setMode] = useState<EditorMode>(allowSimplified ? 'simplified' : 'advanced');
    const [advancedLocked, setAdvancedLocked] = useState(false);
    const [fields, setFields] = useState<AccessControlPropertyField[]>([]);
    const [saving, setSaving] = useState(false);
    const [saveError, setSaveError] = useState('');
    const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);

    useEffect(() => {
        let cancelled = false;
        setLoading(true);
        setLoadError('');
        Promise.all([
            client.get(resourceId),
            getAccessControlFields('', 100, agentIdForAuthz).catch(() => [] as AccessControlPropertyField[]),
        ]).then(([loaded, loadedFields]) => {
            if (cancelled) {
                return;
            }
            setPolicy(loaded);
            setFields(loadedFields);
            const expr = loaded?.rules?.[0]?.expression ?? '';
            setExpression(expr);
            setSavedExpression(expr);

            // Multi-rule policies (future/external authoring) can't round-trip
            // through the simple editor; lock away from it. Admins edit rule 0
            // in advanced mode (the other rules are preserved verbatim on
            // save); everyone else gets the read-only unsupported view.
            if ((loaded?.rules?.length ?? 0) > 1) {
                setAdvancedLocked(true);
                if (allowAdvanced) {
                    setMode('advanced');
                }
            }
        }).catch(() => {
            if (!cancelled) {
                setLoadError(intl.formatMessage({defaultMessage: 'Failed to load the access policy. Please try again.'}));
            }
        }).finally(() => {
            if (!cancelled) {
                setLoading(false);
            }
        });
        return () => {
            cancelled = true;
        };
    }, [client, resourceId, agentIdForAuthz, allowAdvanced, intl]);

    // Contract §6.2: CEL editor takes {attribute, values, isNative}[].
    const celAttributes = useMemo<CELEditorAttribute[]>(() => fields.map((field) => ({
        attribute: field.name,
        values: extractFieldValues(field),
        isNative: false,
    })), [fields]);

    const tableActions = useMemo<TableEditorActions>(() => ({
        getVisualAST: wrapAction((expr: string) => getAccessControlVisualAST(resourceType, expr, agentIdForAuthz)),
        searchUsers: wrapAction((expr: string, term: string, after: string, limit: number) => testAccessControlExpression(resourceType, expr, term, after, limit, agentIdForAuthz)),
    }), [resourceType, agentIdForAuthz]);

    const celActions = useMemo<CELEditorActions>(() => ({
        checkExpression: (expr: string) => checkAccessControlExpression(resourceType, expr, agentIdForAuthz),
        searchUsers: wrapAction((expr: string, term: string, after: string, limit: number) => testAccessControlExpression(resourceType, expr, term, after, limit, agentIdForAuthz)),
    }), [resourceType, agentIdForAuthz]);

    const handleParseError = useCallback(() => {
        // The simple editor can't display this expression: lock away from it.
        setAdvancedLocked(true);
        if (allowAdvanced) {
            setMode('advanced');
        }
    }, [allowAdvanced]);

    const handleSave = useCallback(async () => {
        setSaving(true);
        setSaveError('');
        try {
            const base: AccessControlPolicy = policy ?? {
                id: resourceId,
                name: resourceDisplayName,
                type: '',
                active: true,
                create_at: 0,
                revision: 0,
                version: '',
                roles: [],
                imports: [],
                rules: [],
                props: {},
            };
            const rules = [...(base.rules ?? [])];
            if (rules.length === 0) {
                rules.push({actions: ['use'], expression});
            } else {
                rules[0] = {...rules[0], actions: ['use'], expression};
            }
            const saved = await client.put(resourceId, {...base, name: base.name || resourceDisplayName, rules});
            setPolicy(saved);
            setSavedExpression(expression);
        } catch (e) {
            const message = e instanceof Error && e.message ? e.message : '';
            setSaveError(message || intl.formatMessage({defaultMessage: 'Failed to save the access policy. Please try again.'}));
        } finally {
            setSaving(false);
        }
    }, [client, policy, resourceId, resourceDisplayName, expression, intl]);

    const handleDelete = useCallback(async () => {
        setShowDeleteConfirm(false);
        setSaving(true);
        setSaveError('');
        try {
            await client.del(resourceId);
            setPolicy(null);
            setExpression('');
            setSavedExpression('');
        } catch (e) {
            const message = e instanceof Error && e.message ? e.message : '';
            setSaveError(message || intl.formatMessage({defaultMessage: 'Failed to remove the access policy. Please try again.'}));
        } finally {
            setSaving(false);
        }
    }, [client, resourceId, intl]);

    if (!editors) {
        // Feature detection failed after the parent already checked: render
        // nothing rather than a broken editor.
        return null;
    }

    if (loading) {
        return (
            <SpinnerContainer>
                <LoadingSpinner/>
            </SpinnerContainer>
        );
    }

    if (loadError) {
        return <ErrorText>{loadError}</ErrorText>;
    }

    // Allowed-modes model: a locked policy renders in the advanced editor only
    // for callers that are allowed to use it; everyone else gets the read-only
    // unsupported view.
    let view: EditorView = mode;
    if (advancedLocked) {
        view = allowAdvanced ? 'advanced' : 'unsupported';
    }

    const showToggle = allowSimplified && allowAdvanced && !advancedLocked;
    const dirty = expression !== savedExpression;
    const canSave = view !== 'unsupported' && dirty && expressionValid && expression.trim() !== '' && !saving;

    const {TableEditor, CELEditor} = editors;

    return (
        <EditorContainer>
            {showToggle && (
                <ModeToggleRow>
                    <ModeButton
                        type='button'
                        $active={mode === 'simplified'}
                        onClick={() => setMode('simplified')}
                    >
                        <FormattedMessage defaultMessage='Simple'/>
                    </ModeButton>
                    <ModeButton
                        type='button'
                        $active={mode === 'advanced'}
                        onClick={() => setMode('advanced')}
                    >
                        <FormattedMessage defaultMessage='Advanced'/>
                    </ModeButton>
                </ModeToggleRow>
            )}
            {view === 'advanced' && advancedLocked && allowSimplified && (
                <HelperText>
                    <FormattedMessage defaultMessage="This policy uses expressions the simple editor can't display."/>
                </HelperText>
            )}

            {view === 'unsupported' ? (
                <>
                    <HelperText>
                        <FormattedMessage defaultMessage='This policy uses expressions that can only be edited by a system administrator. You can remove the policy to start over.'/>
                    </HelperText>
                    {savedExpression !== '' && <ReadOnlyExpression>{savedExpression}</ReadOnlyExpression>}
                </>
            ) : (
                <Suspense
                    fallback={
                        <SpinnerContainer>
                            <LoadingSpinner/>
                        </SpinnerContainer>
                    }
                >
                    {view === 'simplified' ? (
                        <TableEditor
                            value={expression}
                            onChange={setExpression}
                            onValidate={setExpressionValid}
                            userAttributes={fields}
                            enableUserManagedAttributes={false}
                            onParseError={handleParseError}
                            actions={tableActions}
                        />
                    ) : (
                        <CELEditor
                            value={expression}
                            onChange={setExpression}
                            onValidate={setExpressionValid}
                            userAttributes={celAttributes}
                            actions={celActions}
                        />
                    )}
                </Suspense>
            )}

            {saveError && <ErrorText>{saveError}</ErrorText>}

            <ButtonRow>
                {policy !== null && (
                    <RemoveButton
                        type='button'
                        onClick={() => setShowDeleteConfirm(true)}
                        disabled={saving}
                    >
                        <FormattedMessage defaultMessage='Remove policy'/>
                    </RemoveButton>
                )}
                {view !== 'unsupported' && (
                    <SavePolicyButton
                        type='button'
                        onClick={handleSave}
                        disabled={!canSave}
                    >
                        {saving ? (
                            <FormattedMessage defaultMessage='Saving...'/>
                        ) : (
                            <FormattedMessage defaultMessage='Save policy'/>
                        )}
                    </SavePolicyButton>
                )}
            </ButtonRow>

            <ConfirmationDialog
                show={showDeleteConfirm}
                titleId={DELETE_POLICY_TITLE_ID}
                title={<FormattedMessage defaultMessage='Remove access policy?'/>}
                message={
                    <FormattedMessage defaultMessage='Removing the policy stops attribute-based restrictions for this resource. This cannot be undone.'/>
                }
                confirmButtonText={<FormattedMessage defaultMessage='Remove'/>}
                cancelButtonText={<FormattedMessage defaultMessage='Cancel'/>}
                onConfirm={handleDelete}
                onCancel={() => setShowDeleteConfirm(false)}
                isDestructive={true}
                managedAccessibility={true}
                zIndex={2100}
            />
        </EditorContainer>
    );
};

// extractFieldValues pulls the selectable option names out of a property
// field's attrs (select/multiselect fields) for CEL autocomplete.
function extractFieldValues(field: AccessControlPropertyField): string[] {
    const options = field.attrs?.options;
    if (!Array.isArray(options)) {
        return [];
    }
    return options.
        map((option) => (option && typeof option === 'object' && 'name' in option ? String((option as {name: unknown}).name) : '')).
        filter((name) => name !== '');
}

// --- Styled Components ---

const EditorContainer = styled.div`
    display: flex;
    flex-direction: column;
    gap: 12px;
`;

const SpinnerContainer = styled.div`
    display: flex;
    justify-content: center;
    padding: 24px 0;
`;

const ErrorText = styled.div`
    color: var(--dnd-indicator, #D24B4E);
    font-size: 13px;
`;

const HelperText = styled.div`
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
`;

const ReadOnlyExpression = styled.code`
    display: block;
    padding: 8px 10px;
    border-radius: 4px;
    background: rgba(var(--center-channel-color-rgb), 0.04);
    font-size: 12px;
    white-space: pre-wrap;
    word-break: break-word;
`;

const ModeToggleRow = styled.div`
    display: flex;
    gap: 4px;
`;

const ModeButton = styled.button<{$active: boolean}>`
    padding: 6px 12px;
    border: 1px solid ${(p) => (p.$active ? 'var(--button-bg)' : 'rgba(var(--center-channel-color-rgb), 0.16)')};
    border-radius: 4px;
    background: ${(p) => (p.$active ? 'rgba(var(--button-bg-rgb), 0.08)' : 'transparent')};
    color: ${(p) => (p.$active ? 'var(--button-bg)' : 'rgba(var(--center-channel-color-rgb), 0.72)')};
    font-size: 12px;
    font-weight: 600;
    cursor: pointer;
`;

const ButtonRow = styled.div`
    display: flex;
    justify-content: flex-end;
    gap: 8px;
`;

const RemoveButton = styled(TertiaryButton)`
    height: 36px;
    color: var(--dnd-indicator, #D24B4E);
`;

const SavePolicyButton = styled(PrimaryButton)`
    height: 36px;
`;

export default PolicyEditor;
