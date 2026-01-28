# Embedding Search E2E Test Plan

This document outlines targeted e2e tests for the embedding search frontend functionality. These tests complement the existing unit and integration tests by verifying user-facing behavior.

---

## Existing Coverage

The file `e2e/tests/semantic-search/basic-search.spec.ts` already covers:
- Single-word search via RHS
- Multi-word search via RHS
- Empty query validation (send button disabled)

---

## New Tests to Add

### File: `e2e/tests/semantic-search/search-sources.spec.ts`

Tests for the search sources display component that shows search results with relevance scores.

#### Test 1: Search sources panel displays and expands

**What it tests:** When a search returns results, the "Sources" collapsible section appears on the bot response. Clicking it expands to show the source posts with relevance scores.

**Steps:**
1. Create several posts with searchable content
2. Open RHS and send a search query
3. Wait for bot response
4. Verify "Sources" header is visible with count badge
5. Click "Sources" header to expand
6. Verify source items are visible with relevance percentages (e.g., "85%")
7. Verify each source shows a post preview

**Key selectors:**
- Sources header: `getByText('Sources')`
- Source count badge in header
- Relevance score: element containing percentage format
- Post preview content within source items

#### Test 2: Search with no results shows appropriate message

**What it tests:** When search returns no matching results, the bot response indicates this appropriately (no Sources section should appear).

**Steps:**
1. Open RHS and send a query for content that doesn't exist (e.g., "xyznonexistent12345")
2. Wait for bot response
3. Verify "Sources" section is NOT visible
4. Verify response indicates no results were found

---

### File: `e2e/tests/semantic-search/search-citations.spec.ts`

Tests for inline post citations in search responses.

#### Test 3: Post citations display with tooltip and navigation

**What it tests:** When the LLM response includes citation markers (`!!CITE#!!`), they render as clickable citation icons. Hovering shows a tooltip with the username and channel name. Clicking navigates to the source post.

**Steps:**
1. Create posts with searchable content
2. Open RHS and send a search query
3. Mock LLM response to include citation markers (e.g., "Based on the discussion !!CITE1!! the budget is approved !!CITE2!!.")
4. Wait for bot response to complete
5. Verify citation icons are rendered (small circular icons with message icon)
6. Hover over a citation icon
7. Verify tooltip appears showing `@username` and `#channelname`
8. Click the citation icon
9. Verify navigation to the source post (URL contains `/_redirect/pl/{postId}`)

**Key selectors:**
- Citation wrapper: styled component with `cursor: pointer` and circular background
- Tooltip content: contains `@` and `#` labels with username/channel values
- Post navigation: check `window.location.href` contains post permalink

**Mock response format:**
The LLM mock should return text containing `!!CITE1!!` markers. The backend will process these and emit annotations with:
```json
{
  "type": "post_citation",
  "index": 1,
  "post_id": "abc123",
  "channel_id": "ch1",
  "channel_name": "General",
  "username": "john"
}
```

---

### File: `e2e/tests/semantic-search/search-entry-points.spec.ts`

Tests for different ways users can initiate an embedding search.

#### Test 4: Slash command /ask-channel triggers search

**What it tests:** Users can trigger embedding search using the `/ask-channel` slash command from the channel message input.

**Steps:**
1. Create posts with searchable content
2. Type `/ask-channel what is the budget status` in the channel message input
3. Send the command
4. Verify RHS opens with bot response
5. Verify response contains search-derived content

**Key selectors:**
- Channel post textbox for slash command input
- RHS container: `getByTestId('mattermost-ai-rhs')`

#### Test 5: Search button visible when search is enabled

**What it tests:** The "Agents" search button appears in the search bar when embedding search is enabled in the plugin configuration.

**Steps:**
1. Navigate to a channel
2. Locate the search bar area
3. Verify "Agents" text/button is visible (indicates search is enabled)

**Note:** This test depends on the mock plugin configuration having search enabled.

---

### File: `e2e/tests/semantic-search/search-bot-selector.spec.ts`

Tests for bot selection during search operations.

#### Test 6: Can select different bot for search

**What it tests:** Users can select which bot to use when performing a search via the bot selector dropdown.

**Steps:**
1. Open RHS
2. Verify search hint text "Agents searches only content you have access to" is visible
3. Click on bot selector dropdown
4. Select a different bot
5. Send a search query
6. Verify response is from the selected bot (check bot username on response)

**Key selectors:**
- Bot selector: `getByTestId('bot-selector-rhs')`
- Bot name button in response

---

## Implementation Notes

### Mock Response Format

Search responses need to include `search_results` in post props. The mock should return a response where the bot post has props like:

```json
{
  "search_results": "[{\"postId\":\"abc123\",\"channelId\":\"ch1\",\"userId\":\"user1\",\"content\":\"Budget report for Q4\",\"score\":0.85}]"
}
```

### Test Data Setup

Each test should:
1. Create posts with unique, searchable content before running queries
2. Use distinctive keywords to avoid cross-test contamination
3. Reset mocks between tests

### Selectors Reference

| Component | Selector |
|-----------|----------|
| RHS container | `getByTestId('mattermost-ai-rhs')` |
| RHS textarea | `#rhsContainer textarea` |
| Send button | `getByTestId('SendMessageButton')` |
| Bot selector | `getByTestId('bot-selector-rhs')` |
| New chat button | `getByTestId('new-chat')` |
| App bar icon | `#app-bar-icon-mattermost-ai` |

---

## Summary

| Test | File | Description |
|------|------|-------------|
| 1 | search-sources.spec.ts | Sources panel displays and expands |
| 2 | search-sources.spec.ts | No results shows appropriate message |
| 3 | search-citations.spec.ts | Post citations display with tooltip and navigation |
| 4 | search-entry-points.spec.ts | /ask-channel slash command |
| 5 | search-entry-points.spec.ts | Search button visibility |
| 6 | search-bot-selector.spec.ts | Bot selection for search |

**Total new tests:** 6
