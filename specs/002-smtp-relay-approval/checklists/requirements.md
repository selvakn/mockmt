# Specification Quality Checklist: SMTP Relay with Human Approval

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-08-06
**Feature**: [spec.md](../spec.md)

## Content Quality

- [x] No implementation details (languages, frameworks, APIs)
- [x] Focused on user value and business needs
- [x] Written for non-technical stakeholders
- [x] All mandatory sections completed

## Requirement Completeness

- [x] No [NEEDS CLARIFICATION] markers remain
- [x] Requirements are testable and unambiguous
- [x] Success criteria are measurable
- [x] Success criteria are technology-agnostic (no implementation details)
- [x] All acceptance scenarios are defined
- [x] Edge cases are identified
- [x] Scope is clearly bounded
- [x] Dependencies and assumptions identified

## Feature Readiness

- [x] All functional requirements have clear acceptance criteria
- [x] User scenarios cover primary flows
- [x] Feature meets measurable outcomes defined in Success Criteria
- [x] No implementation details leak into specification

## Notes

**Iteration 1 (2026-08-06)**: Three [NEEDS CLARIFICATION] markers open — reviewer authorization (FR-017), sender identity on relay (FR-013), approval granularity (FR-021). All other items passed.

**Iteration 2 (2026-08-06)**: All three resolved by the user and folded into the spec:

- **FR-013 / FR-013a / FR-013b** — rewrite `From` to the configured relay identity, route replies to the original sender via `Reply-To` (preserving any `Reply-To` the sender set), and keep the original sender visible to the reviewer and in the audit record.
- **FR-017 / FR-017a / FR-017b** — reviewer authorization is an operator-configured list of email addresses, matched case-insensitively; startup fails if relay is enabled with an empty list.
- **FR-021 / FR-021a** — approval is per message, not per recipient; one submission is one queue item and one decision.

Dependent sections were updated for consistency: User Story 1 scenarios 6–7, User Story 2 scenario 3, four new edge cases, the Queued Message / Recipient Delivery / Reviewer / Relay Configuration entities, SC-006 and new SC-006a, three new assumptions, and two new out-of-scope items.

**Validation performed**: zero clarification markers remain; FR-001 through FR-032 (plus lettered sub-requirements) and SC-001 through SC-009 are sequential with no gaps; all inline `(FR-nnn)` cross-references resolve to defined requirements; keyword scan for implementation leakage (language, datastore, framework, transport, config mechanism) returned no hits.

**Iteration 3 — `/speckit-clarify` session, 2026-08-06**: Five gaps found by taxonomy scan and resolved. See the `## Clarifications` section of the spec for the recorded Q&A.

1. **Non-reviewer visibility in relay mode** (security) → queued messages have no per-recipient owner; relaying never provisions a portal account for an external recipient. Added FR-018a/018b, SC-010.
2. **Synchronous vs backgrounded send** (reliability) → synchronous with a bounded timeout. Added FR-020a/020b, and the indeterminate-outcome classification FR-024a/025a that stops a timed-out send being retried into a duplicate. Rewrote FR-028 to cover crash recovery.
3. **Attachment inspection** (UX/security) → inline preview for common types plus download fallback. Added FR-016a–016e including isolation of untrusted content and no remote-content fetching during review. Added SC-008a/008b/008c.
4. **Envelope vs header recipients** (domain model) → the envelope list governs; blind-carbon recipients are surfaced to the reviewer and flagged, without exposing them to other recipients. Added FR-015a/015b, FR-013c, SC-004a. This closed a silent bypass of the approval gate.
5. **Retention** (data volume) → configurable, off by default; terminal-state content purged after N days while metadata and audit records persist. Added FR-033–036, SC-009a/009b.

Follow-on consistency fix found during integration: **Failed** had no exit but retry, so failed messages could never reach a terminal state and never become purgeable. Added FR-026a (reviewer may abandon a failed message) and extended FR-027 with the `Failed → Rejected` transition.

Terminology normalized: the original request's "admin login" is recorded as the canonical term **reviewer** in the Reviewer entity.

**Post-clarification validation**: 5 clarification bullets for 5 accepted answers, no duplicates; only permitted new headings added (`## Clarifications`, `### Session 2026-08-06`); FR-001–FR-036 with lettered sub-requirements and SC-001–SC-010 all unique, no duplicate IDs; no `NEEDS CLARIFICATION`/`TODO`/`TBD` markers; implementation-leakage keyword scan clean; the two surviving uses of "indefinitely" refer to pending messages, which FR-035 exempts from purging, so no contradiction with the new retention policy.

**Status**: All items pass. Ready for `/speckit-plan`.
