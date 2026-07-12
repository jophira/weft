package cmd

// Built-in work-plane templates. `weft init` seeds these into ~/weft/templates
// (only when absent — never overwriting an edited template), and `weft ticket
// new` falls back to them when a template file is missing. The {{ticket}}
// placeholder is substituted with the ticket id at scaffold time.

const defaultTicketTemplate = `# {{ticket}}

**Status:** todo
**Owner:**
**Links:**

## Summary

<what this ticket delivers, in one or two sentences>

## Context

<why — the problem, background, constraints>

## Acceptance criteria

- [ ] ...
- [ ] ...

## Notes

`

const defaultEstimateTemplate = `# {{ticket}} — estimate

**Confidence:** low | medium | high

| Work | Size | Notes |
|---|---|---|
|  |  |  |

**Total:**
**Risks / unknowns:**
`

const defaultAnalysisTemplate = `# {{ticket}} — analysis

## Findings

<what the code / system currently does; where the change lands>

## Options considered

1. **Option A** — ...
2. **Option B** — ...

## Decision

<chosen approach and why>
`

const defaultPlanTemplate = `# {{ticket}} — plan

## Approach

<the shape of the implementation>

## Steps

1. [ ] ...
2. [ ] ...

## Verification

<how we confirm it works — tests, manual check>
`

// seedTemplates are the templates written into ~/weft/templates by weft init.
var seedTemplates = map[string]string{
	"ticket.md":   defaultTicketTemplate,
	"estimate.md": defaultEstimateTemplate,
	"analysis.md": defaultAnalysisTemplate,
	"plan.md":     defaultPlanTemplate,
}
