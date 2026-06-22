import fs from 'fs';
import os from 'os';
import path from 'path';

import { GenericContainer, StartedNetwork, StartedTestContainer, Wait } from 'testcontainers';

export const WEB_SEARCH_MOCK_IMAGE = 'node:20-alpine';
export const WEB_SEARCH_MOCK_PORT = 8090;
export const WEB_SEARCH_MOCK_ALIAS = 'websearch';
export const WEB_SEARCH_MOCK_API_PATH = '/customsearch/v1';
export const WEB_SEARCH_MOCK_NETWORK_URL = `http://${WEB_SEARCH_MOCK_ALIAS}:${WEB_SEARCH_MOCK_PORT}${WEB_SEARCH_MOCK_API_PATH}`;

// Points the plugin's built-in Google WebSearch provider at the deterministic mock.
export const WEB_SEARCH_PLUGIN_CONFIG = {
    enabled: true,
    provider: 'google',
    google: {
        apiKey: 'mock-key',
        searchEngineId: 'mock-cx',
        apiURL: WEB_SEARCH_MOCK_NETWORK_URL,
        resultLimit: 5,
    },
};

export type WebSearchMockResult = {
    title: string;
    url: string;
    snippet: string;
};

export const CITATION_SEARCH_RESULTS: Record<string, WebSearchMockResult> = {
    typescript: {
        title: 'TypeScript Documentation',
        url: 'https://www.typescriptlang.org/docs/',
        snippet: 'TypeScript extends JavaScript with static types.',
    },
    javascript: {
        title: 'JavaScript | MDN',
        url: 'https://developer.mozilla.org/en-US/docs/Web/JavaScript',
        snippet: 'JavaScript is a dynamic programming language.',
    },
    react: {
        title: 'React Docs',
        url: 'https://react.dev/',
        snippet: 'React is a library for building user interfaces.',
    },
};

function buildServerScript(): string {
    const resultsJson = JSON.stringify(CITATION_SEARCH_RESULTS);
    return `
const http = require('http');
const url = require('url');

const RESULTS = ${resultsJson};

function itemsForQuery(query) {
  const normalized = String(query || '').toLowerCase();
  const items = [];
  for (const [key, result] of Object.entries(RESULTS)) {
    if (normalized.includes(key)) {
      items.push({
        title: result.title,
        link: result.url,
        snippet: result.snippet,
      });
    }
  }
  if (items.length === 0) {
    items.push({
      title: RESULTS.typescript.title,
      link: RESULTS.typescript.url,
      snippet: RESULTS.typescript.snippet,
    });
  }
  return items;
}

const server = http.createServer((req, res) => {
  const parsed = url.parse(req.url, true);
  if (req.method !== 'GET') {
    res.writeHead(405);
    res.end();
    return;
  }

  const items = itemsForQuery(parsed.query.q);
  res.writeHead(200, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify({ items }));
});

server.listen(${WEB_SEARCH_MOCK_PORT}, '0.0.0.0');
`.trim();
}

export class WebSearchMockContainer {
    private container: StartedTestContainer | null = null;
    private serverDir: string | null = null;

    async start(network: StartedNetwork): Promise<void> {
        this.serverDir = fs.mkdtempSync(path.join(os.tmpdir(), 'web-search-mock-'));
        fs.writeFileSync(path.join(this.serverDir, 'server.js'), buildServerScript());

        try {
            this.container = await new GenericContainer(WEB_SEARCH_MOCK_IMAGE)
                .withExposedPorts(WEB_SEARCH_MOCK_PORT)
                .withNetwork(network)
                .withNetworkAliases(WEB_SEARCH_MOCK_ALIAS)
                .withBindMounts([
                    {
                        source: this.serverDir,
                        target: '/app',
                        mode: 'ro',
                    },
                ])
                .withCommand(['node', '/app/server.js'])
                .withWaitStrategy(Wait.forListeningPorts())
                .start();
        } catch (error) {
            this.removeServerDir();
            throw error;
        }
    }

    async stop(): Promise<void> {
        try {
            if (this.container) {
                await this.container.stop();
            }
        } finally {
            this.container = null;
            this.removeServerDir();
        }
    }

    private removeServerDir(): void {
        if (this.serverDir && fs.existsSync(this.serverDir)) {
            fs.rmSync(this.serverDir, { recursive: true, force: true });
        }

        this.serverDir = null;
    }
}

export async function RunWebSearchMockSidecar(network: StartedNetwork): Promise<WebSearchMockContainer> {
    const mock = new WebSearchMockContainer();
    await mock.start(network);
    return mock;
}
