// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

/**
 * GENERATED FILE — DO NOT EDIT BY HAND.
 *
 * Pinned snapshot of the mattermost webapp's exported editor contract types,
 * flattened to be self-contained. CI type-checks the plugin's mirrors
 * (src/types/access_control_editors.ts, src/types/access_control.ts) against
 * this snapshot via `npm run check-editor-contract`, so contract drift fails
 * the build without needing a host checkout.
 *
 * Source of truth (in the mattermost repo):
 *   - webapp/channels/src/components/admin_console/access_control/editors/table_editor/table_editor.tsx
 *   - webapp/channels/src/components/admin_console/access_control/editors/cel_editor/editor.tsx
 *   - webapp/platform/types/src/access_control.ts
 *   - webapp/platform/types/src/properties_user.ts
 *
 * Regenerate against a mattermost webapp checkout (and re-verify) with:
 *   MM_WEBAPP_PATH=/path/to/mattermost/webapp npm run update-editor-contract-snapshot
 * (MM_WEBAPP_PATH is optional when the checkout is a sibling of this repo.)
 */

/* eslint-disable */

import type React from 'react';

export type HostTableEditorProps = {
    value: string;
    onChange: (value: string) => void;
    onValidate?: undefined | ((isValid: boolean) => void);
    disabled?: undefined | false | true;
    userAttributes: Array<({
            id: string;
            group_id: string;
            name: string;
            type: "text" | "select" | "multiselect" | "date" | "user" | "multiuser" | "rank";
            attrs?: undefined | ({
                [key: string]: unknown;
                subType?: undefined | string;
            });
            target_id: string;
            target_type: string;
            object_type: string;
            linked_field_id?: undefined | string;
            protected?: undefined | false | true;
            create_at: number;
            update_at: number;
            delete_at: number;
            created_by: string;
            updated_by: string;
        }) & ({
            group_id: "custom_profile_attributes" | "session_attributes";
            attrs: {
                sort_order: number;
                visibility: "always" | "hidden" | "when_set";
                value_type: "" | "email" | "url" | "phone";
                options?: undefined | (Array<{
                        id: string;
                        name: string;
                        color?: undefined | string;
                        rank?: undefined | number;
                    }>);
                ldap?: undefined | string;
                saml?: undefined | string;
                managed?: undefined | string;
                protected?: undefined | false | true;
                source_plugin_id?: undefined | string;
                access_mode?: undefined | "" | "source_only" | "shared_only";
                display_name?: undefined | string;
                native?: undefined | false | true;
                operators?: undefined | Array<string>;
            };
        })>;
    enableUserManagedAttributes: boolean;
    onParseError: (error: string) => void;
    channelId?: undefined | string;
    teamId?: undefined | string;
    actions: {
        getVisualAST: (expr: string) => Promise<{
                    data?: any;
                    error?: any;
                }>;
        searchUsers?: undefined | ((expression: string, term: string, after: string, limit: number) => Promise<{
                    data?: undefined | ({
                        users: Array<{
                                id: string;
                                create_at: number;
                                update_at: number;
                                delete_at: number;
                                username: string;
                                password: string;
                                auth_data?: undefined | string;
                                auth_service: string;
                                email: string;
                                nickname: string;
                                first_name: string;
                                last_name: string;
                                position: string;
                                roles: string;
                                props: {
                                    [key: string]: string;
                                };
                                notify_props: {
                                    desktop: "default" | "all" | "mention" | "none";
                                    desktop_sound: "default" | "true" | "false";
                                    calls_desktop_sound: "true" | "false";
                                    email: "true" | "false";
                                    mark_unread: "all" | "mention";
                                    push: "default" | "all" | "mention" | "none";
                                    push_status: "ooo" | "offline" | "away" | "dnd" | "online";
                                    comments: "never" | "root" | "any";
                                    first_name: "true" | "false";
                                    channel: "true" | "false";
                                    mention_keys: string;
                                    highlight_keys: string;
                                    desktop_notification_sound?: undefined | "default" | "Bing" | "Crackle" | "Down" | "Hello" | "Ripple" | "Upstairs";
                                    calls_notification_sound?: undefined | "Dynamic" | "Calm" | "Urgent" | "Cheerful";
                                    desktop_threads?: undefined | "default" | "all" | "mention" | "none";
                                    email_threads?: undefined | "default" | "all" | "mention" | "none";
                                    push_threads?: undefined | "default" | "all" | "mention" | "none";
                                    auto_responder_active?: undefined | "true" | "false";
                                    auto_responder_message?: undefined | string;
                                    calls_mobile_sound?: undefined | "" | "true" | "false";
                                    calls_mobile_notification_sound?: undefined | "" | "Dynamic" | "Calm" | "Urgent" | "Cheerful";
                                    channel_mention_auto_follow_threads?: undefined | "true" | "false";
                                };
                                last_password_update: number;
                                last_picture_update: number;
                                locale: string;
                                timezone?: undefined | ({
                                    useAutomaticTimezone: string | false | true;
                                    automaticTimezone: string;
                                    manualTimezone: string;
                                });
                                mfa_active: boolean;
                                last_activity_at: number;
                                is_bot: boolean;
                                bot_description: string;
                                terms_of_service_id: string;
                                terms_of_service_create_at: number;
                                remote_id?: undefined | string;
                                status?: undefined | string;
                                custom_profile_attributes?: undefined | ({
                                    [key: string]: string | Array<string>;
                                });
                                failed_attempts?: undefined | number;
                            }>;
                        total: number;
                    });
                    error?: any;
                }>);
    };
    isSystemAdmin?: undefined | false | true;
    validateExpressionAgainstRequester?: undefined | ((expression: string) => Promise<{
                data?: undefined | {
                    requester_matches: boolean;
                };
                error?: any;
            }>);
    onTestClick?: undefined | (() => void);
    testButtonDisabled?: undefined | false | true;
    testButtonTooltip?: undefined | string;
    testButtonLabel?: React.ReactNode;
    onMaskedStateChange?: undefined | ((hasMasked: boolean) => void);
};

export type HostCELEditorProps = {
    value: string;
    onChange: (value: string) => void;
    onValidate?: undefined | ((isValid: boolean) => void);
    placeholder?: undefined | string;
    className?: undefined | string;
    channelId?: undefined | string;
    teamId?: undefined | string;
    disabled?: undefined | false | true;
    userAttributes: Array<{
            attribute: string;
            values: Array<string>;
            isNative?: undefined | false | true;
        }>;
    onTestClick?: undefined | (() => void);
    testButtonLabel?: React.ReactNode;
    hasMaskedRows?: undefined | false | true;
    actions?: undefined | ({
        checkExpression?: undefined | ((expression: string) => Promise<Array<{
                        message: string;
                        line: number;
                        column: number;
                    }>>);
        searchUsers?: undefined | ((expression: string, term: string, after: string, limit: number) => Promise<{
                    data?: undefined | ({
                        users: Array<{
                                id: string;
                                create_at: number;
                                update_at: number;
                                delete_at: number;
                                username: string;
                                password: string;
                                auth_data?: undefined | string;
                                auth_service: string;
                                email: string;
                                nickname: string;
                                first_name: string;
                                last_name: string;
                                position: string;
                                roles: string;
                                props: {
                                    [key: string]: string;
                                };
                                notify_props: {
                                    desktop: "default" | "all" | "mention" | "none";
                                    desktop_sound: "default" | "true" | "false";
                                    calls_desktop_sound: "true" | "false";
                                    email: "true" | "false";
                                    mark_unread: "all" | "mention";
                                    push: "default" | "all" | "mention" | "none";
                                    push_status: "ooo" | "offline" | "away" | "dnd" | "online";
                                    comments: "never" | "root" | "any";
                                    first_name: "true" | "false";
                                    channel: "true" | "false";
                                    mention_keys: string;
                                    highlight_keys: string;
                                    desktop_notification_sound?: undefined | "default" | "Bing" | "Crackle" | "Down" | "Hello" | "Ripple" | "Upstairs";
                                    calls_notification_sound?: undefined | "Dynamic" | "Calm" | "Urgent" | "Cheerful";
                                    desktop_threads?: undefined | "default" | "all" | "mention" | "none";
                                    email_threads?: undefined | "default" | "all" | "mention" | "none";
                                    push_threads?: undefined | "default" | "all" | "mention" | "none";
                                    auto_responder_active?: undefined | "true" | "false";
                                    auto_responder_message?: undefined | string;
                                    calls_mobile_sound?: undefined | "" | "true" | "false";
                                    calls_mobile_notification_sound?: undefined | "" | "Dynamic" | "Calm" | "Urgent" | "Cheerful";
                                    channel_mention_auto_follow_threads?: undefined | "true" | "false";
                                };
                                last_password_update: number;
                                last_picture_update: number;
                                locale: string;
                                timezone?: undefined | ({
                                    useAutomaticTimezone: string | false | true;
                                    automaticTimezone: string;
                                    manualTimezone: string;
                                });
                                mfa_active: boolean;
                                last_activity_at: number;
                                is_bot: boolean;
                                bot_description: string;
                                terms_of_service_id: string;
                                terms_of_service_create_at: number;
                                remote_id?: undefined | string;
                                status?: undefined | string;
                                custom_profile_attributes?: undefined | ({
                                    [key: string]: string | Array<string>;
                                });
                                failed_attempts?: undefined | number;
                            }>;
                        total: number;
                    });
                    error?: any;
                }>);
    });
};

export type HostCELEditorActions = {
    checkExpression?: undefined | ((expression: string) => Promise<Array<{
                    message: string;
                    line: number;
                    column: number;
                }>>);
    searchUsers?: undefined | ((expression: string, term: string, after: string, limit: number) => Promise<{
                data?: undefined | ({
                    users: Array<{
                            id: string;
                            create_at: number;
                            update_at: number;
                            delete_at: number;
                            username: string;
                            password: string;
                            auth_data?: undefined | string;
                            auth_service: string;
                            email: string;
                            nickname: string;
                            first_name: string;
                            last_name: string;
                            position: string;
                            roles: string;
                            props: {
                                [key: string]: string;
                            };
                            notify_props: {
                                desktop: "default" | "all" | "mention" | "none";
                                desktop_sound: "default" | "true" | "false";
                                calls_desktop_sound: "true" | "false";
                                email: "true" | "false";
                                mark_unread: "all" | "mention";
                                push: "default" | "all" | "mention" | "none";
                                push_status: "ooo" | "offline" | "away" | "dnd" | "online";
                                comments: "never" | "root" | "any";
                                first_name: "true" | "false";
                                channel: "true" | "false";
                                mention_keys: string;
                                highlight_keys: string;
                                desktop_notification_sound?: undefined | "default" | "Bing" | "Crackle" | "Down" | "Hello" | "Ripple" | "Upstairs";
                                calls_notification_sound?: undefined | "Dynamic" | "Calm" | "Urgent" | "Cheerful";
                                desktop_threads?: undefined | "default" | "all" | "mention" | "none";
                                email_threads?: undefined | "default" | "all" | "mention" | "none";
                                push_threads?: undefined | "default" | "all" | "mention" | "none";
                                auto_responder_active?: undefined | "true" | "false";
                                auto_responder_message?: undefined | string;
                                calls_mobile_sound?: undefined | "" | "true" | "false";
                                calls_mobile_notification_sound?: undefined | "" | "Dynamic" | "Calm" | "Urgent" | "Cheerful";
                                channel_mention_auto_follow_threads?: undefined | "true" | "false";
                            };
                            last_password_update: number;
                            last_picture_update: number;
                            locale: string;
                            timezone?: undefined | ({
                                useAutomaticTimezone: string | false | true;
                                automaticTimezone: string;
                                manualTimezone: string;
                            });
                            mfa_active: boolean;
                            last_activity_at: number;
                            is_bot: boolean;
                            bot_description: string;
                            terms_of_service_id: string;
                            terms_of_service_create_at: number;
                            remote_id?: undefined | string;
                            status?: undefined | string;
                            custom_profile_attributes?: undefined | ({
                                [key: string]: string | Array<string>;
                            });
                            failed_attempts?: undefined | number;
                        }>;
                    total: number;
                });
                error?: any;
            }>);
};

export type HostAccessControlTestResult = {
    users: Array<{
            id: string;
            create_at: number;
            update_at: number;
            delete_at: number;
            username: string;
            password: string;
            auth_data?: undefined | string;
            auth_service: string;
            email: string;
            nickname: string;
            first_name: string;
            last_name: string;
            position: string;
            roles: string;
            props: {
                [key: string]: string;
            };
            notify_props: {
                desktop: "default" | "all" | "mention" | "none";
                desktop_sound: "default" | "true" | "false";
                calls_desktop_sound: "true" | "false";
                email: "true" | "false";
                mark_unread: "all" | "mention";
                push: "default" | "all" | "mention" | "none";
                push_status: "ooo" | "offline" | "away" | "dnd" | "online";
                comments: "never" | "root" | "any";
                first_name: "true" | "false";
                channel: "true" | "false";
                mention_keys: string;
                highlight_keys: string;
                desktop_notification_sound?: undefined | "default" | "Bing" | "Crackle" | "Down" | "Hello" | "Ripple" | "Upstairs";
                calls_notification_sound?: undefined | "Dynamic" | "Calm" | "Urgent" | "Cheerful";
                desktop_threads?: undefined | "default" | "all" | "mention" | "none";
                email_threads?: undefined | "default" | "all" | "mention" | "none";
                push_threads?: undefined | "default" | "all" | "mention" | "none";
                auto_responder_active?: undefined | "true" | "false";
                auto_responder_message?: undefined | string;
                calls_mobile_sound?: undefined | "" | "true" | "false";
                calls_mobile_notification_sound?: undefined | "" | "Dynamic" | "Calm" | "Urgent" | "Cheerful";
                channel_mention_auto_follow_threads?: undefined | "true" | "false";
            };
            last_password_update: number;
            last_picture_update: number;
            locale: string;
            timezone?: undefined | ({
                useAutomaticTimezone: string | false | true;
                automaticTimezone: string;
                manualTimezone: string;
            });
            mfa_active: boolean;
            last_activity_at: number;
            is_bot: boolean;
            bot_description: string;
            terms_of_service_id: string;
            terms_of_service_create_at: number;
            remote_id?: undefined | string;
            status?: undefined | string;
            custom_profile_attributes?: undefined | ({
                [key: string]: string | Array<string>;
            });
            failed_attempts?: undefined | number;
        }>;
    total: number;
};

export type HostCELExpressionError = {
    message: string;
    line: number;
    column: number;
};

export type HostUserPropertyField = ({
    id: string;
    group_id: string;
    name: string;
    type: "text" | "select" | "multiselect" | "date" | "user" | "multiuser" | "rank";
    attrs?: undefined | ({
        [key: string]: unknown;
        subType?: undefined | string;
    });
    target_id: string;
    target_type: string;
    object_type: string;
    linked_field_id?: undefined | string;
    protected?: undefined | false | true;
    create_at: number;
    update_at: number;
    delete_at: number;
    created_by: string;
    updated_by: string;
}) & ({
    group_id: "custom_profile_attributes" | "session_attributes";
    attrs: {
        sort_order: number;
        visibility: "always" | "hidden" | "when_set";
        value_type: "" | "email" | "url" | "phone";
        options?: undefined | (Array<{
                id: string;
                name: string;
                color?: undefined | string;
                rank?: undefined | number;
            }>);
        ldap?: undefined | string;
        saml?: undefined | string;
        managed?: undefined | string;
        protected?: undefined | false | true;
        source_plugin_id?: undefined | string;
        access_mode?: undefined | "" | "source_only" | "shared_only";
        display_name?: undefined | string;
        native?: undefined | false | true;
        operators?: undefined | Array<string>;
    };
});
