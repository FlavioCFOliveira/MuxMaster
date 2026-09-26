---
name: task-rmp300-oauth2-default-transport
description: 2026-09-26: middleware-stdlib.md §20 HTTPClient table row rewritten to match oauth2TransportFrom (field copy, no Clone, 100/100/100 caps, TLSNextProto rule, startup-construction constraint)
metadata:
  type: project
---
rmp #300 (commits 3c4b31a, e9a0fa5): the OAuth2Introspect nil-HTTPClient default is specified only in the §20 options table row (line ~263), not as a numbered rule, to avoid renumbering 95-111.

**Why:** numbered rules must stay monotonic per file; a new rule inside §20 would force renumbering cited ranges.
**How to apply:** future changes to the default transport edit that table row. The "HTTP/2 may open additional connections" wording mirrors the code GoDoc (OAuth2Options.HTTPClient); Go 1.27.1 MaxConnsPerHost doc itself carries no HTTP/2 caveat. Related: [[task_rmp305_v130_spec_fixes]].
