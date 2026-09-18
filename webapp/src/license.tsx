// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useIntl} from 'react-intl';
import {useSelector} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';

// SKU short names align with model.LicenseShortSku* in Mattermost server public.
const skuE10 = 'E10';
const skuE20 = 'E20';
const skuProfessional = 'professional';
const skuEnterprise = 'enterprise';
const skuEnterpriseAdvanced = 'advanced';
const skuEntry = 'entry';

// LicenseLevel mirrors enterprise.Level on the server. Higher levels include
// every capability of the levels below them.
export enum LicenseLevel {
    Unlicensed = 0,
    Professional = 1,
    Enterprise = 2,
    EnterpriseAdvanced = 3,
}

// Capability mirrors enterprise.Capability on the server.
export type Capability =
    | 'multiplayer_channels'
    | 'thread_summarization'
    | 'channel_summarization'
    | 'provider_web_search'
    | 'agent_access_controls'
    | 'token_accounting'
    | 'multiple_llm_services'
    | 'state_changing_tools'
    | 'sovereign_web_search'
    | 'tool_approval_policies'
    | 'remote_mcp'
    | 'semantic_search'
    | 'meetings'
    | 'mcp_service_account'
    | 'shared_prompts'
    | 'channel_auto_reply'
    | 'attribute_based_access';

// capabilityMinLevel is the tier chart: the minimum level at which each capability is available.
export const capabilityMinLevel: Record<Capability, LicenseLevel> = {
    multiplayer_channels: LicenseLevel.Professional,
    thread_summarization: LicenseLevel.Professional,
    channel_summarization: LicenseLevel.Professional,
    provider_web_search: LicenseLevel.Professional,
    agent_access_controls: LicenseLevel.Professional,
    token_accounting: LicenseLevel.Professional,

    multiple_llm_services: LicenseLevel.Enterprise,
    state_changing_tools: LicenseLevel.Enterprise,
    sovereign_web_search: LicenseLevel.Enterprise,
    tool_approval_policies: LicenseLevel.Enterprise,
    remote_mcp: LicenseLevel.Enterprise,
    semantic_search: LicenseLevel.Enterprise,
    meetings: LicenseLevel.Enterprise,
    mcp_service_account: LicenseLevel.Enterprise,
    shared_prompts: LicenseLevel.Enterprise,

    channel_auto_reply: LicenseLevel.EnterpriseAdvanced,
    attribute_based_access: LicenseLevel.EnterpriseAdvanced,
};

// Agent and LLM service caps per level; null means uncapped.
export const FREE_AGENT_LIMIT = 1;
export const PROFESSIONAL_AGENT_LIMIT = 3;
export const BASE_SERVICE_LIMIT = 1;

// licenseLevelFromLicense mirrors enterprise.LevelFor. The Entry SKU maps to
// Enterprise; unknown SKUs fall back to feature flags.
export const licenseLevelFromLicense = (license: Record<string, string> | undefined | null): LicenseLevel => {
    if (!license) {
        return LicenseLevel.Unlicensed;
    }
    switch (license.SkuShortName) {
    case skuEnterpriseAdvanced:
        return LicenseLevel.EnterpriseAdvanced;
    case skuEnterprise:
    case skuEntry:
    case skuE20:
        return LicenseLevel.Enterprise;
    case skuProfessional:
    case skuE10:
        return LicenseLevel.Professional;
    default:
        break;
    }
    if (license.FutureFeatures === 'true') {
        return LicenseLevel.Enterprise;
    }
    if (license.LDAP === 'true') {
        return LicenseLevel.Professional;
    }
    return LicenseLevel.Unlicensed;
};

const isConfiguredForDevelopment = (state: GlobalState): boolean => {
    const config = state.entities.general.config;

    return config.EnableTesting === 'true' && config.EnableDeveloper === 'true';
};

// getLicenseLevel returns the current license level. A development server
// (EnableTesting and EnableDeveloper) reports EnterpriseAdvanced.
export const getLicenseLevel = (state: GlobalState): LicenseLevel => {
    if (isConfiguredForDevelopment(state)) {
        return LicenseLevel.EnterpriseAdvanced;
    }
    return licenseLevelFromLicense(state.entities.general.license);
};

export const requiredLevelFor = (capability: Capability): LicenseLevel => {
    return capabilityMinLevel[capability] ?? LicenseLevel.EnterpriseAdvanced;
};

export const licenseAllows = (state: GlobalState, capability: Capability): boolean => {
    return getLicenseLevel(state) >= requiredLevelFor(capability);
};

export const agentLimitForLevel = (level: LicenseLevel): number | null => {
    if (level >= LicenseLevel.Enterprise) {
        return null;
    }
    if (level >= LicenseLevel.Professional) {
        return PROFESSIONAL_AGENT_LIMIT;
    }
    return FREE_AGENT_LIMIT;
};

export const serviceLimitForLevel = (level: LicenseLevel): number | null => {
    return level >= LicenseLevel.Enterprise ? null : BASE_SERVICE_LIMIT;
};

export function useLicenseLevel(): LicenseLevel {
    return useSelector(getLicenseLevel);
}

export function useIsLicensedFor(capability: Capability): boolean {
    return useLicenseLevel() >= requiredLevelFor(capability);
}

export function useAgentLimit(): number | null {
    return agentLimitForLevel(useLicenseLevel());
}

export function useServiceLimit(): number | null {
    return serviceLimitForLevel(useLicenseLevel());
}

// useLicenseLevelName returns the localized name of a level for user-facing copy.
export function useLicenseLevelName(): (level: LicenseLevel) => string {
    const intl = useIntl();
    return (level: LicenseLevel) => {
        switch (level) {
        case LicenseLevel.Professional:
            return intl.formatMessage({defaultMessage: 'Professional'});
        case LicenseLevel.Enterprise:
            return intl.formatMessage({defaultMessage: 'Enterprise'});
        case LicenseLevel.EnterpriseAdvanced:
            return intl.formatMessage({defaultMessage: 'Enterprise Advanced'});
        default:
            return intl.formatMessage({defaultMessage: 'Free'});
        }
    };
}

// useIsMultiLLMLicensed reports whether multiple LLM services, per-agent
// service routing and fallback chains are available.
export function useIsMultiLLMLicensed() {
    return useIsLicensedFor('multiple_llm_services');
}

// useIsBasicsLicensed reports whether the Enterprise capability set is available.
export function useIsBasicsLicensed() {
    return useLicenseLevel() >= LicenseLevel.Enterprise;
}
