import {StartedTestContainer, GenericContainer, StartedNetwork, Network, Wait} from "testcontainers";

/**
 * Smocker matches request paths exactly. Bifrost may POST to /v1/chat/completions or /chat/completions.
 * Use a single path regex so we do not register two mocks with the same body matchers (which would
 * cause the second request to match the duplicate rule instead of the next logical mock).
 */
export function normalizeChatCompletionMockPath(body: any): any {
	const req = body?.request;
	if (!req || typeof req.path !== 'string') {
		return body;
	}
	const p = req.path;
	const m = p.match(/^(.*)\/v1\/chat\/completions$/);
	if (!m) {
		return body;
	}
	const pathPrefix = m[1];
	const escaped = pathPrefix.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
	return {
		...body,
		request: {
			...req,
			path: {
				matcher: 'ShouldMatch',
				value: `^${escaped}(/v1)?/chat/completions$`,
			},
		},
	};
}

export const responseTest = `
data: {"id":"chatcmpl-8t1WLFfcSfmK0sfBcFbj8VEhOqNYd","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"role":"assistant","content":""},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-8t1WLFfcSfmK0sfBcFbj8VEhOqNYd","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":"Hello"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-8t1WLFfcSfmK0sfBcFbj8VEhOqNYd","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":"!"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-8t1WLFfcSfmK0sfBcFbj8VEhOqNYd","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" How"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-8t1WLFfcSfmK0sfBcFbj8VEhOqNYd","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" can"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-8t1WLFfcSfmK0sfBcFbj8VEhOqNYd","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" I"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-8t1WLFfcSfmK0sfBcFbj8VEhOqNYd","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" assist"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-8t1WLFfcSfmK0sfBcFbj8VEhOqNYd","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" you"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-8t1WLFfcSfmK0sfBcFbj8VEhOqNYd","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" today"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-8t1WLFfcSfmK0sfBcFbj8VEhOqNYd","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":"?"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-8t1WLFfcSfmK0sfBcFbj8VEhOqNYd","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{},"logprobs":null,"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';

export const responseTestText = "Hello! How can I assist you today?"

export const responseTest2 = `
data: {"id":"chatcmpl-8t1WLFfcSfmK0sfBcFbj8VEhOqNYd","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"role":"assistant","content":""},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-8t1WLFfcSfmK0sfBcFbj8VEhOqNYd","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":"Hello"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-8t1WLFfcSfmK0sfBcFbj8VEhOqNYd","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":"!"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-8t1WLFfcSfmK0sfBcFbj8VEhOqNYd","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" This"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-8t1WLFfcSfmK0sfBcFbj8VEhOqNYd","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" is"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-8t1WLFfcSfmK0sfBcFbj8VEhOqNYd","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" a"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-8t1WLFfcSfmK0sfBcFbj8VEhOqNYd","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" second"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-8t1WLFfcSfmK0sfBcFbj8VEhOqNYd","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" message"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-8t1WLFfcSfmK0sfBcFbj8VEhOqNYd","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":"."},"logprobs":null,"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';

export const responseTest2Text = "Hello! This is a second message."


export class OpenAIMockContainer {
	container: StartedTestContainer;

	start = async (network: StartedNetwork) => {
		this.container = await new GenericContainer("thiht/smocker")
			.withExposedPorts(8081)
			.withNetwork(network)
			.withNetworkAliases("openai")
			.withWaitStrategy(Wait.forHttp("/version", 8081))
			.start()

		await this.resetMocks();
	}

	stop = async () => {
		await this.container.stop()
	}

	resetMocks = async (attempt = 0): Promise<void> => {
		const maxAttempts = 5;

		try {
			await fetch(`http://localhost:${this.container.getMappedPort(8081)}/reset`, {
				method: "POST",
			});
		} catch (error) {
			if (attempt >= maxAttempts - 1) {
				throw error;
			}

			const backoffMs = Math.min(2000, 250 * Math.pow(2, attempt));
			await new Promise(resolve => setTimeout(resolve, backoffMs));

			return this.resetMocks(attempt + 1);
		}
	}

	addMock = async (body: any, attempt = 0): Promise<Response> => {
		const maxAttempts = 5;

		try {
			const response = await fetch(`http://localhost:${this.container.getMappedPort(8081)}/mocks?reset=true`, {
				method: "POST",
				headers: {
					"Content-Type": "application/json",
				},
				body: JSON.stringify([normalizeChatCompletionMockPath(body)]),
			});

			if (!response.ok) {
				throw new Error(`Failed to register mock: ${response.status} ${response.statusText}`);
			}

			return response;
		} catch (error) {
			if (attempt >= maxAttempts - 1) {
				throw error;
			}

			const backoffMs = Math.min(2000, 250 * Math.pow(2, attempt));
			await new Promise(resolve => setTimeout(resolve, backoffMs));

			return this.addMock(body, attempt + 1);
		}
	}

	/**
	 * Register multiple Smocker mocks in one request (replaces all mocks, same as addMock).
	 * Use this when sequential completions need different responses (e.g. tool call then text).
	 */
	addMocks = async (bodies: any[], attempt = 0): Promise<Response> => {
		const maxAttempts = 5;

		try {
			const response = await fetch(`http://localhost:${this.container.getMappedPort(8081)}/mocks?reset=true`, {
				method: "POST",
				headers: {
					"Content-Type": "application/json",
				},
				body: JSON.stringify(bodies.map(normalizeChatCompletionMockPath)),
			});

			if (!response.ok) {
				throw new Error(`Failed to register mocks: ${response.status} ${response.statusText}`);
			}

			return response;
		} catch (error) {
			if (attempt >= maxAttempts - 1) {
				throw error;
			}

			const backoffMs = Math.min(2000, 250 * Math.pow(2, attempt));
			await new Promise(resolve => setTimeout(resolve, backoffMs));

			return this.addMocks(bodies, attempt + 1);
		}
	}

	addCompletionMock = async (response: string, botPrefix?: string) => {
		const prefix = botPrefix ? ("/"+botPrefix) : ""
		return this.addMock({
			request: {
				method: "POST",
				path: prefix + "/v1/chat/completions",
			},
			context: {
				times: 100,
			},
			response: {
				status: 200,
				headers: {
					"Content-Type": "text/event-stream",
				},
				body: response,
			},
		})
	}

	// Added for more complex mocking scenarios
	addCompletionMockWithRequestBody = async (response: string, requestBodyContains: string, botPrefix?: string) => {
		const prefix = botPrefix ? ("/"+botPrefix) : ""
		return this.addMock({
			request: {
				method: "POST",
				path: prefix + "/v1/chat/completions",
				body: {
					matcher: "ShouldContainSubstring",
					value: requestBodyContains
				}
			},
			context: {
				times: 100,
			},
			response: {
				status: 200,
				headers: {
					"Content-Type": "text/event-stream",
				},
				body: response,
			},
		})
	}

	// Add error mock for testing error handling
	addErrorMock = async (statusCode: number, errorMessage: string, botPrefix?: string) => {
		const prefix = botPrefix ? ("/"+botPrefix) : ""
		return this.addMock({
			request: {
				method: "POST",
				path: prefix + "/v1/chat/completions",
			},
			context: {
				times: 100,
			},
			response: {
				status: statusCode,
				headers: {
					"Content-Type": "application/json",
				},
				body: JSON.stringify({
					error: {
						message: errorMessage,
						type: "api_error",
					}
				}),
			},
		})
	}
}

export const RunOpenAIMocks = async (network: StartedNetwork): Promise<OpenAIMockContainer> => {
	const container = new OpenAIMockContainer()
	await container.start(network)

	return container
}

/**
 * Create a streaming SSE response that includes a tool call.
 * Follows OpenAI's chat.completions streaming format.
 */
export function buildToolCallResponse(toolCallId: string, toolName: string, args: string): string {
	const escapedArgs = args.replace(/"/g, '\\"');
	const chunks = [
		`data: {"id":"chatcmpl-tc1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-mock","choices":[{"index":0,"delta":{"role":"assistant","content":null,"tool_calls":[{"index":0,"id":"${toolCallId}","type":"function","function":{"name":"${toolName}","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-tc1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-mock","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"${escapedArgs}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-tc1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-mock","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		'data: [DONE]',
	];
	return chunks.join('\n\n') + '\n\n';
}

/**
 * Create a streaming SSE text response (for after tool execution).
 */
export function buildTextResponse(text: string): string {
	const words = text.split(' ');
	const chunks = [
		`data: {"id":"chatcmpl-tr1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-mock","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
	];
	for (const word of words) {
		chunks.push(
			`data: {"id":"chatcmpl-tr1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-mock","choices":[{"index":0,"delta":{"content":"${word} "},"finish_reason":null}]}`,
		);
	}
	chunks.push(
		`data: {"id":"chatcmpl-tr1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-mock","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	);
	chunks.push('data: [DONE]');
	return chunks.join('\n\n') + '\n\n';
}
