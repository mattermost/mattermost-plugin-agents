# Figma Remote MCP Server - Tool Classification

## Overview

The **Figma MCP Server** is Figma's official cloud-hosted remote MCP server, providing AI coding agents with structured design context from Figma Design, FigJam, and Figma Make files.

**Remote Endpoint:** `https://mcp.figma.com/mcp` (Streamable HTTP)
**Guide Repository:** [figma/mcp-server-guide](https://github.com/figma/mcp-server-guide)

---

## Tool Classification Summary

| Classification | Count | Percentage |
|---------------|-------|------------|
| **READ**      | 9     | 69.2%      |
| **WRITE**     | 4     | 30.8%      |
| **DELETE**     | 0     | 0%         |
| **MIXED**     | 0     | 0%         |
| **Total**     | **13** | 100%      |

---

## Complete Tool Catalog

### READ Tools

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `get_design_context` | Retrieves structured design context for a specific Figma node (layout, typography, colors, tokens, component structure). Returns React + Tailwind representation by default. Formerly `get_code`. | **READ** | Only retrieves and returns structured design data. Does not modify anything in Figma. |
| `get_metadata` | Returns a sparse XML representation of a node with basic properties (layer IDs, names, types, positions, sizes). Useful for large designs. | **READ** | Only reads and returns structural metadata. |
| `get_screenshot` | Captures a screenshot/image of a specified Figma node. Returns an image file. Formerly `get_image`. | **READ** | Only captures and returns a visual representation. Does not alter the file. |
| `get_variable_defs` | Extracts variables and styles (design tokens) used in a Figma selection - colors, spacing, typography. | **READ** | Only reads and returns variable/style definitions. |
| `get_figjam` | Returns metadata for FigJam diagrams in XML format, including screenshots of nodes. FigJam files only. | **READ** | Only reads and returns metadata from FigJam files. |
| `create_design_system_rules` | Generates a custom design system rules file tailored to the user's codebase and tech stack. Output is text returned to the agent. | **READ** | Despite its name, this tool only generates text output returned to the AI agent. It does not write anything to Figma. |
| `get_code_connect_map` | Retrieves existing Code Connect mappings between Figma node IDs and code components. Requires Org/Enterprise plan. | **READ** | Only reads and returns existing Code Connect mapping data. |
| `get_code_connect_suggestions` | Detects and suggests mappings of Figma components to code components. Starting step of the Code Connect workflow. | **READ** | Only reads the Figma scenegraph to generate suggestions. Does not create mappings. |
| `whoami` | Returns the identity of the currently authenticated Figma user (email, plan, seat types). Remote-only. | **READ** | Only reads and returns user information. |

### WRITE Tools

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `generate_diagram` | Generates a FigJam diagram from Mermaid syntax. Creates new interactive, editable FigJam diagram content. | **WRITE** | Creates new FigJam diagram content (shapes, connectors, text). |
| `generate_figma_design` | Captures live running UI and converts it into editable Figma design layers. Can create new files or add frames to existing files. **Claude Code only.** | **WRITE** | Creates new Figma design files or adds new frames/layers. |
| `add_code_connect_map` | Adds a single Code Connect mapping between a Figma node ID and a code component. Requires Org/Enterprise plan. | **WRITE** | Creates a new Code Connect mapping entry. |
| `send_code_connect_mappings` | Batch-creates Code Connect mappings after user reviews suggestions. Final step of the Code Connect workflow. | **WRITE** | Writes new Code Connect mapping entries in batch. |

---

## Auto-Approvable READ Tools

The following 9 tools are classified as READ-only and are candidates for auto-approval:

```json
{
  "server_name": "Figma",
  "server_url_pattern": "mcp.figma.com",
  "read_tools": [
    "get_design_context",
    "get_metadata",
    "get_screenshot",
    "get_variable_defs",
    "get_figjam",
    "create_design_system_rules",
    "get_code_connect_map",
    "get_code_connect_suggestions",
    "whoami"
  ]
}
```

---

## Key Observations

1. **No DELETE tools**: The Figma MCP server does not expose any tools that remove or delete data.
2. **`create_design_system_rules` is READ**: Despite the name, it generates text output returned to the agent; nothing is written to Figma.
3. **`generate_figma_design` is client-gated**: Only available to Claude Code clients, not other MCP clients.
4. **Code Connect tools require Org/Enterprise plans**: `get_code_connect_map`, `add_code_connect_map`, `get_code_connect_suggestions`, and `send_code_connect_mappings` only work with published components on these plan tiers.
5. **Historical renames**: `get_code` was renamed to `get_design_context`; `get_image` was renamed to `get_screenshot`.

---

## Sources

- [Figma MCP Server - Tools and Prompts (Developer Docs)](https://developers.figma.com/docs/figma-mcp-server/tools-and-prompts/)
- [Guide to the Figma MCP Server (Help Center)](https://help.figma.com/hc/en-us/articles/32132100833559-Guide-to-the-Figma-MCP-server)
- [Figma MCP Server Guide (GitHub)](https://github.com/figma/mcp-server-guide)
