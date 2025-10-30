// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';

import {CitationComponent} from './citation_component';
import {Annotation} from './types';

const openAICitationRegex = /\([^\s:]+\s*:\s*https?:\/\/[\S^)]*\)/g;

// Insert special markers in the text that will survive markdown processing
export function insertAnnotationMarkers(message: string, annotations: Annotation[]): string {
    const sortedAnnotations = [...annotations].sort((a, b) => b.start_index - a.start_index);
    let result = message;

    // Insert markers from end to start to preserve indices
    // Use a simple marker that won't conflict with markdown
    for (const annotation of sortedAnnotations) {
        const marker = `!!CITE${annotation.index}!!`;
        result = result.slice(0, annotation.end_index) + marker + result.slice(annotation.end_index);
    }

    return result;
}

// Replace citation markers in the processed JSX with actual citation components
export function replaceCitationMarkers(element: any, annotations: Annotation[]): any {
    if (typeof element === 'string') {
        const cleanedElement = element.replace(openAICitationRegex, '').replace(/\s+\./g, '.');

        // Use regex to find all citation markers at once
        const markerRegex = /!!CITE(\d+)!!/g;
        const matches = [...cleanedElement.matchAll(markerRegex)];

        if (matches.length > 0) {
            // Split the string by all markers and rebuild with components
            const parts = cleanedElement.split(markerRegex);
            const result = [];

            for (let i = 0; i < parts.length; i++) {
                // Add text part (if not empty)
                if (parts[i] !== '') {
                    result.push(parts[i]);
                }

                // Check if this is followed by a citation index (odd indices after split)
                if (i + 1 < parts.length && parts[i + 1] && (/^\d+$/).test(parts[i + 1])) {
                    const citationIndex = parseInt(parts[i + 1], 10);
                    const annotation = annotations.find((ann) => ann.index === citationIndex);

                    if (annotation) {
                        result.push(
                            <CitationComponent
                                key={`citation-${citationIndex}-${Math.random()}`}
                                annotation={annotation}
                            />,
                        );
                    }

                    // Skip the index part we just processed
                    i++;
                }
            }

            return result.length === 1 ? result[0] : result;
        }

        return cleanedElement;
    }

    if (React.isValidElement(element)) {
        // Recursively process children
        const props = element.props as {children?: React.ReactNode};
        if (props.children) {
            const processedChildren = React.Children.map(props.children, (child) =>
                replaceCitationMarkers(child, annotations),
            );

            return React.cloneElement(element, {}, processedChildren);
        }

        return element;
    }

    if (Array.isArray(element)) {
        return element.map((item) => replaceCitationMarkers(item, annotations));
    }

    return element;
}
