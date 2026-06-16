/**
 * Small builders for aimock JSON fixtures used by ported E2E specs.
 * Prefer checked-in JSON under e2e/fixtures/aimock/ for review; use these when
 * generating fixtures programmatically in spikes.
 */

export type AimockFixtureFile = {
	fixtures: AimockFixtureEntry[];
};

export type AimockFixtureEntry = {
	match: Record<string, string | boolean>;
	response: {
		content?: string;
		toolCalls?: Array<{
			id?: string;
			name: string;
			arguments: Record<string, unknown>;
		}>;
	};
};

export function buildStreamingTextFixture(
	userMessage: string,
	content: string,
): AimockFixtureFile {
	return {
		fixtures: [
			{
				match: {userMessage},
				response: {content},
			},
		],
	};
}

export function buildAskPolicyToolRoundFixture(options: {
	userMessage: string;
	toolCallId: string;
	toolName: string;
	toolArguments: Record<string, unknown>;
	followUpContent: string;
	titlePromptPrefix?: string;
	titleContent?: string;
}): AimockFixtureFile {
	const titlePrefix =
		options.titlePromptPrefix ??
		'Write a short title for the following request. Include only the title and nothing else, no quotations. Request:';
	const titleContent = options.titleContent ?? 'Aimock ask policy title';

	return {
		fixtures: [
			{
				match: {toolCallId: options.toolCallId},
				response: {content: options.followUpContent},
			},
			{
				match: {userMessage: titlePrefix},
				response: {content: titleContent},
			},
			{
				match: {userMessage: options.userMessage, hasToolResult: false},
				response: {
					toolCalls: [
						{
							id: options.toolCallId,
							name: options.toolName,
							arguments: options.toolArguments,
						},
					],
				},
			},
		],
	};
}
