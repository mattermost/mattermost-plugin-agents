import {test} from '@playwright/test';
import {GenericContainer, StartedNetwork, StartedTestContainer, Wait} from 'testcontainers';

import {SMOCKER_IMAGE} from '../openai-mock';

import type {CapturedPush} from './types';

export const PUSH_PROXY_ALIAS = 'pushproxy';
const MOCK_PORT = 8080;
const ADMIN_PORT = 8081;

type SmockerHistoryEntry = {
    request?: {
        method?: string;
        path?: string;
        body?: unknown;
        date?: string;
    };
};

/**
 * Mock push proxy (a Smocker sidecar). Mattermost POSTs every push to
 * `<PushNotificationServer>/api/v1/send_push`; the proxy answers `{"status":"OK"}`
 * and Smocker's history API is the capture log.
 */
export class PushProxy {
    private container: StartedTestContainer | null = null;

    /** URL Mattermost uses as `EmailSettings.PushNotificationServer`. */
    readonly networkUrl = `http://${PUSH_PROXY_ALIAS}:${MOCK_PORT}`;

    async start(network: StartedNetwork): Promise<void> {
        test.slow(); // triples a beforeAll's 60 s timeout, which container startup can exceed
        this.container = await new GenericContainer(SMOCKER_IMAGE)
            .withExposedPorts(ADMIN_PORT)
            .withNetwork(network)
            .withNetworkAliases(PUSH_PROXY_ALIAS)
            .withWaitStrategy(Wait.forHttp('/version', ADMIN_PORT))
            .start();

        const response = await fetch(`${this.adminUrl()}/mocks?reset=true`, {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify([
                {
                    request: {
                        method: 'POST',
                        path: {matcher: 'ShouldMatch', value: '^/api/v1/(send_push|ack)$'},
                    },
                    response: {
                        status: 200,
                        headers: {'Content-Type': ['application/json']},
                        body: '{"status":"OK"}',
                    },
                },
            ]),
        });
        if (!response.ok) {
            throw new Error(`Failed to register push proxy mock: ${response.status}`);
        }
    }

    async stop(): Promise<void> {
        await this.container?.stop();
        this.container = null;
    }

    private adminUrl(): string {
        if (!this.container) {
            throw new Error('PushProxy used before start()');
        }
        return `http://${this.container.getHost()}:${this.container.getMappedPort(ADMIN_PORT)}`;
    }

    /**
     * Every `send_push` request received so far, oldest first. Push-proxy
     * "ack" calls are not part of the capture (they are device receipts).
     */
    async readAll(): Promise<CapturedPush[]> {
        const response = await fetch(`${this.adminUrl()}/history`);
        if (!response.ok) {
            throw new Error(`Failed to read push proxy history: ${response.status}`);
        }
        const history = (await response.json()) as SmockerHistoryEntry[];
        return history
            .filter((entry) => entry.request?.path === '/api/v1/send_push')
            .map((entry, index) => toCapturedPush(entry, index));
    }
}

function toCapturedPush(entry: SmockerHistoryEntry, index: number): CapturedPush {
    const raw = typeof entry.request?.body === 'string' ? JSON.parse(entry.request.body) : entry.request?.body;
    const body = (raw ?? {}) as Record<string, unknown>;
    const str = (key: string) => (typeof body[key] === 'string' ? (body[key] as string) : '');
    return {
        seq: index,
        at: entry.request?.date ? Date.parse(entry.request.date) : 0,
        type: str('type') || 'message',
        platform: str('platform'),
        deviceId: str('device_id'),
        postId: str('post_id'),
        channelId: str('channel_id'),
        rootId: str('root_id'),
        channelName: str('channel_name'),
        message: str('message'),
        senderId: str('sender_id'),
        senderName: str('sender_name'),
        badge: typeof body.badge === 'number' ? body.badge : 0,
        sound: str('sound'),
        category: str('category'),
        isIdLoaded: body.is_id_loaded === true,
        raw: body,
    };
}
