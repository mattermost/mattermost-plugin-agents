import {expect} from '@playwright/test';

/** aimock may split streamed text/tool args across SSE chunks; match stable fragments. */
export function expectChunkedFragments(body: string, fragments: string[]): void {
	for (const fragment of fragments) {
		expect(body).toContain(fragment);
	}
}
