import fs from 'fs';
import os from 'os';
import path from 'path';

import { GenericContainer, StartedNetwork, StartedTestContainer, Wait } from 'testcontainers';

import { withDeadline } from './deadline';

export const WEB_SEARCH_MOCK_IMAGE = 'node:20-alpine';
export const WEB_SEARCH_MOCK_PORT = 8090;
export const WEB_SEARCH_MOCK_ALIAS = 'websearch';
export const WEB_SEARCH_MOCK_NETWORK_URL = `http://${WEB_SEARCH_MOCK_ALIAS}:${WEB_SEARCH_MOCK_PORT}`;

// Points the plugin's built-in SearXNG WebSearch provider at the deterministic mock.
export const WEB_SEARCH_PLUGIN_CONFIG = {
    enabled: true,
    provider: 'searxng',
    searxng: {
        baseURL: WEB_SEARCH_MOCK_NETWORK_URL,
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

function resultsForQuery(query) {
  const normalized = String(query || '').toLowerCase();
  const results = [];
  for (const [key, result] of Object.entries(RESULTS)) {
    if (normalized.includes(key)) {
      results.push({
        title: result.title,
        url: result.url,
        content: result.snippet,
      });
    }
  }
  if (results.length === 0) {
    results.push({
      title: RESULTS.typescript.title,
      url: RESULTS.typescript.url,
      content: RESULTS.typescript.snippet,
    });
  }
  return results;
}

const server = http.createServer((req, res) => {
  const parsed = url.parse(req.url, true);
  if (req.method !== 'GET' || parsed.pathname !== '/search') {
    res.writeHead(404);
    res.end();
    return;
  }

  const results = resultsForQuery(parsed.query.q);
  res.writeHead(200, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify({ results }));
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
            this.container = await withDeadline('Starting the web search mock container', 5 * 60000, () => new GenericContainer(WEB_SEARCH_MOCK_IMAGE)
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
                .withStartupTimeout(120000)
                .start());
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
