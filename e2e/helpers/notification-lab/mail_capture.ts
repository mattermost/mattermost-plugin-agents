import {test} from '@playwright/test';
import {GenericContainer, StartedNetwork, StartedTestContainer, Wait} from 'testcontainers';

import type {CapturedEmail} from './types';

export const MAIL_CAPTURE_IMAGE = 'axllent/mailpit:latest';
export const MAIL_CAPTURE_ALIAS = 'mailpit';
export const SMTP_PORT = 1025;
const HTTP_PORT = 8025;

type MailpitAddress = {Name: string; Address: string};
type MailpitListItem = {
    ID: string;
    Created: string;
    From: MailpitAddress;
    To: MailpitAddress[];
    Subject: string;
};
type MailpitMessage = MailpitListItem & {Text: string; HTML: string};

/** SMTP capture sidecar (Mailpit). Mattermost sends via `mailpit:1025`; the HTTP API is the capture log. */
export class MailCapture {
    private container: StartedTestContainer | null = null;
    private readonly bodies = new Map<string, CapturedEmail>();

    readonly smtpHost = MAIL_CAPTURE_ALIAS;
    readonly smtpPort = String(SMTP_PORT);

    async start(network: StartedNetwork): Promise<void> {
        test.slow(); // triples a beforeAll's 60 s timeout, which container startup can exceed
        this.container = await new GenericContainer(MAIL_CAPTURE_IMAGE)
            .withExposedPorts(HTTP_PORT)
            .withNetwork(network)
            .withNetworkAliases(MAIL_CAPTURE_ALIAS)
            .withWaitStrategy(Wait.forHttp('/livez', HTTP_PORT))
            .start();
    }

    async stop(): Promise<void> {
        await this.container?.stop();
        this.container = null;
    }

    private httpUrl(): string {
        if (!this.container) {
            throw new Error('MailCapture used before start()');
        }
        return `http://${this.container.getHost()}:${this.container.getMappedPort(HTTP_PORT)}`;
    }

    /** Every captured email, oldest first. Bodies are fetched once and cached. */
    async readAll(): Promise<CapturedEmail[]> {
        const listResponse = await fetch(`${this.httpUrl()}/api/v1/messages?limit=500`);
        if (!listResponse.ok) {
            throw new Error(`Failed to list captured emails: ${listResponse.status}`);
        }
        const list = (await listResponse.json()) as {messages: MailpitListItem[]};

        const emails: CapturedEmail[] = [];
        for (const item of list.messages) {
            let email = this.bodies.get(item.ID);
            if (!email) {
                const detailResponse = await fetch(`${this.httpUrl()}/api/v1/message/${item.ID}`);
                if (!detailResponse.ok) {
                    throw new Error(`Failed to read captured email ${item.ID}: ${detailResponse.status}`);
                }
                const detail = (await detailResponse.json()) as MailpitMessage;
                email = {
                    id: item.ID,
                    createdAt: Date.parse(item.Created),
                    to: item.To.map((to) => to.Address.toLowerCase()),
                    from: item.From.Address,
                    subject: item.Subject,
                    text: detail.Text || stripHtml(detail.HTML),
                };
                this.bodies.set(item.ID, email);
            }
            emails.push(email);
        }
        return emails.sort((a, b) => a.createdAt - b.createdAt);
    }
}

function stripHtml(html: string): string {
    return html.replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ').trim();
}
