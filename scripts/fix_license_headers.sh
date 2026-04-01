#!/bin/bash

# Script to fix copyright/license headers in Go files
# Usage: ./fix_license_headers.sh [year]

# Default year is 2023 if not provided
YEAR=${1:-2023}

# The expected license header
LICENSE_HEADER="// Copyright (c) ${YEAR}-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information."

# Find all Go files in the repository
find . -name "*.go" | while read -r file; do
  # Skip files in vendor, node_modules, enterprise, and other generated directories
  # Also skip mock files that end with _mock.go
  if [[ "$file" == *"/node_modules/"* || "$file" == *"/vendor/"* || "$file" == *"/dist/"* || "$file" == *"/enterprise/"* || "$file" == *"_mock.go" ]]; then
    echo "Skipping file in excluded directory or mock file: $file"
    continue
  fi

  # Get the first line of the file
  FIRST_LINE=$(head -n 1 "$file")

  # Files with a leading Go build tag must keep the tag as the first line; skip automatic
  # header rewriting so we do not strip //go:build (see mcp/*_test.go integration tests).
  if [[ "$FIRST_LINE" == "//go:build"* ]] || [[ "$FIRST_LINE" == "// +build"* ]]; then
    continue
  fi

  # Check if the file already has the correct license header
  if [[ "$FIRST_LINE" == "// Copyright (c) "* && "$FIRST_LINE" == *"Mattermost, Inc. All Rights Reserved." ]]; then
    # Already has a copyright line, check the second line
    SECOND_LINE=$(head -n 2 "$file" | tail -n 1)
    if [[ "$SECOND_LINE" == "// See LICENSE.txt for license information." ]]; then
      # License header is correct, skip
      continue
    fi
  fi

  # Get the package line and the rest of the file
  PACKAGE_LINE=$(grep -n "^package " "$file" | head -n 1)
  if [[ -z "$PACKAGE_LINE" ]]; then
    echo "Warning: No package declaration found in $file"
    continue
  fi

  PACKAGE_LINE_NUM=$(echo "$PACKAGE_LINE" | cut -d: -f1)
  
  # Extract the file content from the package line to the end
  FILE_CONTENT=$(tail -n +"$PACKAGE_LINE_NUM" "$file")
  
  # Create a temporary file with the correct license header
  TMP_FILE=$(mktemp)
  echo "$LICENSE_HEADER" > "$TMP_FILE"
  echo "" >> "$TMP_FILE"  # Add a blank line
  echo "$FILE_CONTENT" >> "$TMP_FILE"
  
  # Replace the original file with the fixed one
  mv "$TMP_FILE" "$file"
  echo "Fixed license header in $file"
done

echo "License header fix completed."