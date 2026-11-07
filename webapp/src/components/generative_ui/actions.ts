// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {createContext, useContext} from 'react';

export type SubmissionValue = string | number | boolean;

export interface SubmissionField {
    name: string;
    label: string;
    value: SubmissionValue;
}

/** Mirrors genui.Submission on the server. */
export interface Submission {
    block: number;
    form: string;
    label: string;
    fields: SubmissionField[];
}

/** The ai_genui_submission prop of the post a submission created. */
export interface SubmissionRecord extends Submission {
    source_post_id: string;
}

/** Whether the viewer can act on the generated UI of one agent reply. */
export type PostInteraction =
    | {kind: 'editable'}
    | {kind: 'readonly'}
    | {kind: 'onlooker'; requesterUsername?: string}
    | {kind: 'stale'}
    | {kind: 'submitted'; submission: SubmissionRecord};

export interface GenerativeUIActions {
    interaction: PostInteraction;
    submit: (submission: Submission) => Promise<void>;
}

/** Provided by the agent post; absent everywhere else, which keeps controls read-only. */
export const GenerativeUIActionsContext = createContext<GenerativeUIActions | null>(null);

export const useGenerativeUIActions = () => useContext(GenerativeUIActionsContext);

export type BlockNotice = 'submitted' | 'stale' | 'onlooker' | null;

/** The interaction state of one generated block, derived from its post's. */
export interface BlockInteraction {
    disabled: boolean;
    submittingLabel: string | null;

    /** Set when this block holds the reply's submission. */
    submitted: SubmissionRecord | null;
    notice: BlockNotice;
    requesterUsername?: string;
    submit: (form: string, label: string, fields: SubmissionField[]) => void;
}

const readOnlyBlock: BlockInteraction = {
    disabled: true,
    submittingLabel: null,
    submitted: null,
    notice: null,
    submit: () => {}, // eslint-disable-line no-empty-function
};

export const BlockInteractionContext = createContext<BlockInteraction>(readOnlyBlock);

export const useBlockInteraction = () => useContext(BlockInteractionContext);

export interface RegisteredField {
    label: string;
    required: boolean;
    getValue: () => SubmissionValue | undefined;
}

/** Collects the fields of one Form, in render order, so its buttons can submit them. */
export interface FormFields {
    register: (name: string, field: RegisteredField) => () => void;
    clearError: (name: string) => void;
    errors: ReadonlySet<string>;

    /** Returns the form's values, or null after flagging the required fields left empty. */
    collect: () => SubmissionField[] | null;
}

export const FormFieldsContext = createContext<FormFields | null>(null);

export const useFormFields = () => useContext(FormFieldsContext);
