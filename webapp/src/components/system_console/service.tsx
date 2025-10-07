// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export type ServiceData = {
    id: string
    name: string
    serviceName: string
    url: string
    apiKey: string
    orgId: string
    defaultModel: string
    username: string
    password: string
    tokenLimit: number
    streamingTimeoutSeconds: number
    sendUserId: boolean
    outputTokenLimit: number
    useResponsesAPI: boolean
    enabledNativeTools?: string[]
}
