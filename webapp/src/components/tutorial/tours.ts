// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export const FINISHED = 999;
export const SKIPPED = -999;

// Unique category key - used as preference key
// Format: descriptive_name_v{version} (version helps reset tours for users)
export const TutorialTourCategories: Record<string, string> = {
    AGENTS_TOUR: 'agents_tour_v1',
};

// Define steps for the Agents tour (0-indexed)
// Single tip tour only needs step 0 + FINISHED
export const AgentsTutorialSteps = {
    AgentsIcon: 0,
    FINISHED,
};

// Map categories to their steps (used by manager hook)
export const TTCategoriesMapToSteps: Record<string, Record<string, number>> = {
    [TutorialTourCategories.AGENTS_TOUR]: AgentsTutorialSteps,
};
