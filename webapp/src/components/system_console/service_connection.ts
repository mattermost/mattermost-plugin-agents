// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {LLMService} from './service';

// connectionFingerprint captures only the fields that decide whether a service
// can reach its provider. Renaming a service, or changing a token limit, does
// not alter reachability and so must not force another provider round trip.
export function connectionFingerprint(service: LLMService): string {
    return JSON.stringify([
        service.type,
        service.apiKey,
        service.apiURL,
        service.orgId,
        service.defaultModel,
        service.region,
        service.awsAccessKeyID,
        service.awsSecretAccessKey,
        service.vertexProjectID,
        service.vertexProjectNumber,
        service.vertexAuthCredentials,
    ]);
}

// ServiceTypeLoadTestMock mirrors llm.ServiceTypeLoadTestMock. It answers from
// a local profile and contacts no provider.
export const ServiceTypeLoadTestMock = 'loadtest_mock';

// servicesNeedingConnectionTest returns the services whose provider settings
// differ from the fingerprint recorded in baseline.
//
// This is what keeps "save and test" usable: an admin correcting a typo in one
// service's name should not wait on a live call for every other service they
// have configured.
export function servicesNeedingConnectionTest(
    services: LLMService[],
    baseline: Record<string, string>,
): LLMService[] {
    return services.filter((service) => {
        // Nothing to reach, so a save-time probe would only ever report a
        // failure the admin cannot act on.
        if (service.type === ServiceTypeLoadTestMock) {
            return false;
        }

        // A service with no ID has not been through a save, so there is no
        // prior fingerprint it could match.
        if (!service.id) {
            return true;
        }
        return baseline[service.id] !== connectionFingerprint(service);
    });
}
