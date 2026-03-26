// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSelector} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';

// SKU short names align with model.LicenseShortSku* in Mattermost server public.
const professional = 'professional';
const enterprise = 'enterprise';
const enterpriseAdvanced = 'advanced';
const entry = 'entry';

// isValidSkuShortName returns whether the SKU short name is one of the known strings
// used by LicenseToLicenseTier (professional, enterprise, advanced, entry).
const isValidSkuShortName = (license: Record<string, string>) => {
    switch (license?.SkuShortName) {
    case professional:
    case enterprise:
    case enterpriseAdvanced:
    case entry:
        return true;
    default:
        return false;
    }
};

// checkEnterpriseLicensed mirrors model.MinimumEnterpriseLicense: true for enterprise-tier
// and higher (SkuShortName enterprise, advanced, entry). Unknown SKUs may match via MessageExport.
export const checkEnterpriseLicensed = (license: Record<string, string>) => {
    if (license?.SkuShortName === entry ||
        license?.SkuShortName === enterprise ||
        license?.SkuShortName === enterpriseAdvanced) {
        return true;
    }

    if (!isValidSkuShortName(license)) {
        // As a fallback for licenses whose SKU short name is unknown, make a best effort to try
        // and use the presence of a known E20/Enterprise feature as a check to determine licensing.
        if (license?.MessageExport === 'true') {
            return true;
        }
    }

    return false;
};

// checkProfessionalLicensed mirrors model.MinimumProfessionalLicense: true for professional
// and higher (includes entry, enterprise, advanced). Unknown SKUs may match via LDAP.
export const checkProfessionalLicensed = (license: Record<string, string>) => {
    if (license?.SkuShortName === professional ||
        license?.SkuShortName === entry ||
        license?.SkuShortName === enterprise ||
        license?.SkuShortName === enterpriseAdvanced) {
        return true;
    }

    if (!isValidSkuShortName(license)) {
        // As a fallback for licenses whose SKU short name is unknown, make a best effort to try
        // and use the presence of a known E10/Professional feature as a check to determine licensing.
        if (license?.LDAP === 'true') {
            return true;
        }
    }

    return false;
};

const isConfiguredForDevelopment = (state: GlobalState): boolean => {
    const config = state.entities.general.config;

    return config.EnableTesting === 'true' && config.EnableDeveloper === 'true';
};

// isEnterpriseLicensedOrDevelopment returns true when the server is licensed with minimum Mattermost
// Enterprise License, or has `EnableDeveloper` and `EnableTesting`
// configuration settings enabled, signaling a non-production, developer mode.
export const isEnterpriseLicensedOrDevelopment = (state: GlobalState): boolean => {
    const license = state.entities.general.license;

    return checkEnterpriseLicensed(license) || isConfiguredForDevelopment(state);
};

// isProfressionalLicensedOrDevelopment returns true when the server is at least licensed with a Mattermost Professional License,
// or has `EnableDeveloper` and `EnableTesting` configuration settings enabled,
// signaling a non-production, developer mode.
export const isProfessionalLicensedOrDevelopment = (state: GlobalState): boolean => {
    const license = state.entities.general.license;

    return checkProfessionalLicensed(license) || isConfiguredForDevelopment(state);
};

export function useIsMultiLLMLicensed() {
    return useSelector(isEnterpriseLicensedOrDevelopment);
}

export function useIsBasicsLicensed() {
    return useSelector(isEnterpriseLicensedOrDevelopment);
}
