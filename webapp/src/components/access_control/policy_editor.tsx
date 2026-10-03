// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {Suspense, useCallback, useEffect, useMemo, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {Button} from '@mattermost/compass-ui/components/button';
import {ErrorMessage} from '@mattermost/compass-ui/components/error-message';
import {Spinner} from '@mattermost/compass-ui/components/spinner';
import {Tabs} from '@mattermost/compass-ui/components/tabs';

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
import EditorErrorBoundary from '@/components/access_control/editor_error_boundary';
import ConfirmationDialog from '@/components/confirmation_dialog';

export type PolicyEditorProps = {
    resourceType: PolicyResourceType;
    resourceId: string;
    resourceDisplayName: string;

    // Table (Simple) editor. Used on the agent Access tab and the system
    // console service/MCP panels.
    allowSimplified: boolean;

    // CEL (Advanced) editor. System admins only; used for expressions the
    // table can't display.
    allowAdvanced: boolean;

    // Forwarded as ?agent_id= on CEL calls (per-agent-admin authz lane).
    agentIdForAuthz?: string;

    // When set, render nothing unless a policy already exists. Used so a
    // retained policy stays visible after switching to a legacy access mode.
    hideWhenEmpty?: boolean;

    // When false, the current policy stays visible and can be removed, but
    // it cannot be created or saved. Used when attribute-based access is
    // not licensed.
    allowEdit?: boolean;
};

// EditorMode is the user-selectable editor; 'unsupported' is the read-only
// view when the policy can't render in the table editor and the caller may
// not use the CEL editor.
type EditorMode = 'simplified' | 'advanced';
type EditorView = EditorMode | 'unsupported';

// deriveView computes the rendered view from the stored mode, the lock state,
// and the CURRENT permission props on every render. The stored mode is a
// preference, never an entitlement: 'advanced' is unreachable when
// allowAdvanced is false, so a mid-session privilege downgrade can never
// leave the CEL editor exposed.
function deriveView(mode: EditorMode, advancedLocked: boolean, allowSimplified: boolean, allowAdvanced: boolean): EditorView {
    if (advancedLocked) {
        // The expression can't render in the table editor: advanced for
        // those allowed to use it, read-only unsupported for everyone else.
        return allowAdvanced ? 'advanced' : 'unsupported';
    }
    if (mode === 'advanced' && allowAdvanced) {
        return 'advanced';
    }
    if (allowSimplified) {
        return 'simplified';
    }
    return allowAdvanced ? 'advanced' : 'unsupported';
}

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

const PolicyEditorContent = (props: PolicyEditorProps) => {
    const {resourceType, resourceId, resourceDisplayName, allowSimplified, allowAdvanced, agentIdForAuthz, hideWhenEmpty, allowEdit = true} = props;
    const intl = useIntl();
    const editors = getAccessControlEditors();
    const client = useMemo(() => policyClientFor(resourceType), [resourceType]);

    const [loading, setLoading] = useState(true);
    const [loadFailed, setLoadFailed] = useState(false);
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

    // Load once per mount; the exported wrapper keys this component by
    // resource identity, so a resource switch remounts with fresh state.
    useEffect(() => {
        let cancelled = false;
        setLoading(true);
        setLoadFailed(false);

        // A failed attribute-catalogue fetch fails the whole load: silently
        // rendering an empty catalogue would present an outage as "no
        // attributes" with no error or retry path.
        Promise.all([
            client.get(resourceId),
            getAccessControlFields('', 100, agentIdForAuthz),
        ]).then(([loaded, loadedFields]) => {
            if (cancelled) {
                return;
            }
            setPolicy(loaded);
            setFields(loadedFields);
            const expr = loaded?.rules?.[0]?.expression ?? '';
            setExpression(expr);
            setSavedExpression(expr);

            // Multi-rule policies can't round-trip through the simple editor;
            // deriveView shows advanced or unsupported from allowAdvanced on
            // each render, so a later privilege change must not reload.
            if ((loaded?.rules?.length ?? 0) > 1) {
                setAdvancedLocked(true);
            }
        }).catch(() => {
            if (!cancelled) {
                setLoadFailed(true);
            }
        }).finally(() => {
            if (!cancelled) {
                setLoading(false);
            }
        });
        return () => {
            cancelled = true;
        };
    }, [client, resourceId, agentIdForAuthz]);

    // The CEL editor takes {attribute, values, isNative, objectType}[].
    // Native/session flags must match the host's toCELEditorAttributes mapping
    // so autocomplete lands under user.* / user.session.* instead of
    // user.attributes.*.
    const celAttributes = useMemo<CELEditorAttribute[]>(() => fields.
        filter((field) => typeof field.name === 'string' && field.name.trim() !== '').
        map((field) => ({
            attribute: field.name,
            values: extractFieldValues(field),
            isNative: Boolean(field.attrs?.native),
            objectType: field.object_type,
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
        if (!allowEdit) {
            return;
        }

        // The editor is a single-expression UI. Multi-rule policies keep
        // rules[1..n] invisible; refusing to save avoids persisting hidden
        // restrictions the author never reviewed.
        if ((policy?.rules?.length ?? 0) > 1) {
            return;
        }
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
            const rules = [{actions: ['use'], expression}];
            const saved = await client.put(resourceId, {...base, name: base.name || resourceDisplayName, rules});
            setPolicy(saved);
            setSavedExpression(expression);
        } catch (e) {
            const message = e instanceof Error && e.message ? e.message : '';
            if (message) {
                setSaveError(message);
            } else if (typeof e === 'object' && e !== null && 'status_code' in e && e.status_code === 403) {
                // Cloud/WAF eat the 403 JSON body, leaving ClientError.message empty.
                setSaveError(intl.formatMessage({defaultMessage: 'You do not satisfy one or more conditions in this policy. Adjust the rules to include your attributes, or ask a system admin.'}));
            } else {
                setSaveError(intl.formatMessage({defaultMessage: 'Failed to save the access policy. Please try again.'}));
            }
        } finally {
            setSaving(false);
        }
    }, [allowEdit, client, policy, resourceId, resourceDisplayName, expression, intl]);

    const handleDelete = useCallback(async () => {
        setShowDeleteConfirm(false);
        setSaving(true);
        setSaveError('');
        try {
            await client.del(resourceId);
            setPolicy(null);
            setExpression('');
            setSavedExpression('');

            // The expression that forced the lock is gone; return to a
            // usable authoring state instead of staying read-only.
            setAdvancedLocked(false);
            setMode(allowSimplified ? 'simplified' : 'advanced');
            setExpressionValid(true);
        } catch (e) {
            const message = e instanceof Error && e.message ? e.message : '';
            setSaveError(message || intl.formatMessage({defaultMessage: 'Failed to remove the access policy. Please try again.'}));
        } finally {
            setSaving(false);
        }
    }, [client, resourceId, allowSimplified, intl]);

    if (!editors) {
        // Feature detection failed after the parent already checked: render
        // nothing rather than a broken editor.
        return null;
    }

    if (loading) {
        if (hideWhenEmpty) {
            return null;
        }
        return (
            <SpinnerContainer>
                <Spinner size='24'/>
            </SpinnerContainer>
        );
    }

    if (loadFailed) {
        return (
            <ErrorMessage message={<FormattedMessage defaultMessage='Failed to load the access policy. Please try again.'/>}/>
        );
    }

    if (hideWhenEmpty && !policy) {
        return null;
    }

    const view = deriveView(mode, advancedLocked, allowSimplified, allowAdvanced);

    const showToggle = allowSimplified && allowAdvanced && !advancedLocked;
    const dirty = expression !== savedExpression;
    const multiRule = (policy?.rules?.length ?? 0) > 1;
    const canSave = view !== 'unsupported' && !multiRule && dirty && expressionValid && expression.trim() !== '' && !saving;

    const {TableEditor, CELEditor} = editors;

    return (
        <EditorContainer>
            {hideWhenEmpty && (
                <HelperText>
                    <FormattedMessage defaultMessage='This access policy still restricts who can use this agent, in addition to the setting above. Remove it here if it is no longer needed.'/>
                </HelperText>
            )}
            {showToggle && (
                <Tabs
                    tabs={[
                        {key: 'simplified', label: <FormattedMessage defaultMessage='Simple'/>},
                        {key: 'advanced', label: <FormattedMessage defaultMessage='Advanced'/>},
                    ]}
                    activeKey={mode}
                    onChange={(key) => setMode(key === 'advanced' ? 'advanced' : 'simplified')}
                />
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
                <EditorErrorBoundary>
                    <Suspense
                        fallback={
                            <SpinnerContainer>
                                <Spinner size='24'/>
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
                </EditorErrorBoundary>
            )}

            {saveError && <ErrorMessage message={saveError}/>}

            <ButtonRow>
                {policy !== null && (
                    <Button
                        emphasis='tertiary'
                        destructive={true}
                        size='small'
                        onClick={() => setShowDeleteConfirm(true)}
                        disabled={saving}
                    >
                        <FormattedMessage defaultMessage='Remove policy'/>
                    </Button>
                )}
                {view !== 'unsupported' && !multiRule && (
                    <Button
                        emphasis='primary'
                        size='small'
                        onClick={handleSave}
                        loading={saving}
                        disabled={!canSave || !allowEdit}
                    >
                        {saving ? (
                            <FormattedMessage defaultMessage='Saving...'/>
                        ) : (
                            <FormattedMessage defaultMessage='Save policy'/>
                        )}
                    </Button>
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

const ButtonRow = styled.div`
    display: flex;
    justify-content: flex-end;
    gap: 8px;
`;

// PolicyEditor keys the content by resource identity so no editor state can
// survive a resource switch; centralized here so no call site can forget it.
const PolicyEditor = (props: PolicyEditorProps) => (
    <PolicyEditorContent
        key={`${props.resourceType}-${props.resourceId}`}
        {...props}
    />
);

export default PolicyEditor;
