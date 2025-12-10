import {StartedTestContainer, GenericContainer, StartedNetwork, Network, Wait} from "testcontainers";

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
			.withWaitStrategy(Wait.forLogMessage("Starting mock server"))
			.start()

		await this.resetMocks();
	}

	stop = async () => {
		await this.container.stop()
	}

	resetMocks = async () => {
		await fetch(`http://localhost:${this.container.getMappedPort(8081)}/reset`, {
			method: "POST",
		})
	}

	addMock = async (body: any, attempt = 0): Promise<Response> => {
		const maxAttempts = 5;

		try {
			const response = await fetch(`http://localhost:${this.container.getMappedPort(8081)}/mocks?reset=true`, {
				method: "POST",
				headers: {
					"Content-Type": "application/json",
				},
				body: JSON.stringify([body]),
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

	addCompletionMock = async (response: string, botPrefix?: string) => {
		const prefix = botPrefix ? ("/"+botPrefix) : ""
		return this.addMock({
			request: {
				method: "POST",
				path: prefix + "/chat/completions",
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
				path: prefix + "/chat/completions",
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
				path: prefix + "/chat/completions",
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

