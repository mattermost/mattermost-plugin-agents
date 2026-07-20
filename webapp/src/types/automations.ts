// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Types mirror mattermost-plugin-channel-automation's Automation model
// (server/model/automation.go and webapp/src/types.ts). Prefer the Go
// shapes when the two repos drift (e.g. channel_created.team_id).

export type TriggerType =
    | 'message_posted'
    | 'schedule'
    | 'membership_changed'
    | 'channel_created'
    | 'user_joined_team';

export const AIProviderTypeAgent = 'agent';
export const AIProviderTypeService = 'service';

export type AIPromptRequestAs = 'triggerer' | 'creator';

export type MembershipChangedAction = 'joined' | 'left' | '';

export type UserJoinedTeamUserType = 'user' | 'guest' | '';

export type MessagePostedTriggerParams = {
    channel_id: string;
    include_thread_replies?: boolean;
};

export type ScheduleTriggerParams = {
    channel_id: string;
    interval: string; // Go duration, e.g. "1h", "24h", "168h"
    start_at?: number; // UTC Unix ms
};

export type MembershipChangedTriggerParams = {
    channel_id: string;
    action?: MembershipChangedAction; // omitted = both
};

export type ChannelCreatedTriggerParams = {
    team_id: string;
};

export type UserJoinedTeamTriggerParams = {
    team_id: string;
    user_type?: UserJoinedTeamUserType; // omitted = both
};

// Exactly one config should be set.
export type Trigger = {
    message_posted?: MessagePostedTriggerParams;
    schedule?: ScheduleTriggerParams;
    membership_changed?: MembershipChangedTriggerParams;
    channel_created?: ChannelCreatedTriggerParams;
    user_joined_team?: UserJoinedTeamTriggerParams;
};

export type SendMessageActionParams = {
    channel_id: string;
    reply_to_post_id?: string;
    as_bot_id?: string;
    body: string;
};

export type Guardrails = {
    channel_ids?: string[];
};

export type AIPromptActionParams = {
    system_prompt?: string;
    prompt: string;
    provider_type: string; // "agent" | "service"
    provider_id: string;
    allowed_tools?: string[];
    guardrails?: Guardrails;
    request_as?: AIPromptRequestAs;
};

// Exactly one of send_message / ai_prompt should be set.
export type Action = {
    id: string;
    send_message?: SendMessageActionParams;
    ai_prompt?: AIPromptActionParams;
};

export type Automation = {
    id: string;
    name: string;
    enabled: boolean;
    trigger: Trigger;
    actions: Action[];
    created_at: number;
    updated_at: number;
    created_by: string;
};

// Mutable fields for create/update. Enabled is optional so an omitted value
// can preserve the existing flag (matches AutomationUpdate on the server).
export type AutomationUpdate = {
    name: string;
    enabled?: boolean;
    trigger: Trigger;
    actions: Action[];
};

export function getTriggerType(trigger: Trigger): TriggerType | '' {
    if (trigger.message_posted) {
        return 'message_posted';
    }
    if (trigger.schedule) {
        return 'schedule';
    }
    if (trigger.membership_changed) {
        return 'membership_changed';
    }
    if (trigger.channel_created) {
        return 'channel_created';
    }
    if (trigger.user_joined_team) {
        return 'user_joined_team';
    }
    return '';
}

export function getTriggerChannelID(trigger: Trigger): string {
    if (trigger.message_posted) {
        return trigger.message_posted.channel_id;
    }
    if (trigger.schedule) {
        return trigger.schedule.channel_id;
    }
    if (trigger.membership_changed) {
        return trigger.membership_changed.channel_id;
    }
    return '';
}

export function getAIPromptAction(automation: Automation): AIPromptActionParams | undefined {
    return automation.actions.find((a) => a.ai_prompt)?.ai_prompt;
}
