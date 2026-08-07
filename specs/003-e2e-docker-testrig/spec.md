# Feature Specification: Local End-to-End Test Environment

**Feature Branch**: `003-e2e-docker-testrig`
**Created**: 2026-08-07
**Status**: Draft
**Input**: User description: "Build a Docker Compose based end-to-end test rig for the mockmt application, so the whole app (both capture-only and relay-with-approval modes) can be exercised and verified locally before shipping, without requiring a real OAuth identity provider or a real upstream mail provider. The rig needs: (1) a mock OAuth2 server that can stand in for a real identity provider so a developer can log into the portal locally, (2) a non-interactive SMTP test client that can send mail into the app to simulate an agent or user submitting mail, (3) for relay-with-approval mode specifically, a fake upstream SMTP server that speaks TLS so the app's relay feature (which requires TLS, no plaintext option) has something real to relay to and a human can verify delivery happened, and (4) all of the above wired together via Docker Compose so a developer can bring the whole stack up with one command and drive it through a browser. Once the stack is up, it should be used to actually verify the app end to end via browser automation: log in, send mail, confirm it's visible in the portal for capture-only mode; and for relay mode, log in as an authorized reviewer, see a queued message (including a blind-carbon/hidden recipient), open it, and press Send Now to confirm it relays successfully to the fake upstream."

## Overview

Today, verifying the application actually works — logging in, receiving mail, and (for the newer relay-with-approval mode) reviewing and releasing queued mail — requires a real OAuth identity provider and, for relay mode, a real upstream mail account (e.g. Gmail with an App Password). That is a real barrier to routine pre-ship verification: nobody stands up a real OAuth tenant or burns a real Gmail account just to confirm a login screen still works.

This feature gives a developer a self-contained local environment where every external dependency is substituted with a disposable stand-in they fully control, brought up with a single command, so the complete application — both operating modes — can be exercised through an ordinary browser session before shipping, with no real accounts, no real credentials, and no risk of anything actually being delivered to a real person.

## Clarifications

### Session 2026-08-07

- Q: SC-005/FR-006 claim the relay verification "demonstrably exercises real trust-checking," but no acceptance scenario actually performs a negative case (relaying to an impostor). Should the rig itself include a repeatable, deliberate-failure demonstration, or is it enough that the underlying trust-checking mechanism is correct-by-test elsewhere, with this feature only exercising the happy path? → A: The latter. The underlying mechanism's correctness (that an impostor would be rejected) is established by its own test elsewhere, outside this feature. This rig only needs to exercise the happy path through that mechanism — genuine, not bypassed, verification — not stage a live failure to prove it.
- Q: Must the single startup command block until every component is genuinely ready to use, or is it acceptable for it to merely launch everything and leave readiness to lag, with the developer expected to wait or retry? → A: It must not return (or must otherwise clearly signal) until every component is genuinely ready. A developer acting immediately after it completes must not hit failures caused by something still starting up.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Stand up a disposable local environment (Priority: P1)

A developer about to ship a change wants to verify the application actually works, end to end, without configuring a real identity provider or a real mail account. They run a single command and get a fully wired local environment: a portal they can log into, a mail-submission endpoint they can send test mail to, and — for relay mode — a stand-in destination mail server, all disposable and reset to empty every time.

**Why this priority**: Nothing else in this feature is usable without an environment to run it in. This is the foundation every other story depends on.

**Independent Test**: From a clean checkout, run the single startup command and confirm every component reports itself ready, with no manual configuration of any external account.

**Acceptance Scenarios**:

1. **Given** a clean checkout of the repository, **When** the developer runs the single startup command, **Then** it does not report completion until the portal, the mail-submission endpoint, the mock identity provider, and the stand-in destination mail server are all genuinely ready to use, with no external account of any kind configured.
2. **Given** the environment is already running, **When** the developer tears it down and brings it back up again, **Then** it starts from a clean, empty state — no mail or review-queue items left over from the previous run.
3. **Given** the environment is running, **When** the developer stops it, **Then** nothing about their real deployment configuration (the one used for actual shipping) is touched or left in a different state.

---

### User Story 2 - Verify capture-only mode end to end (Priority: P1)

A developer logs into the portal using a throwaway identity (no real account needed), sends a test message into the application as if an ordinary sender had submitted it, and confirms the message shows up correctly in their own inbox view — proving the core receive-and-display path works without touching a real identity provider.

**Why this priority**: This is the application's original, always-on behavior. If this doesn't work, nothing else matters.

**Independent Test**: With the environment up, log in as any throwaway identity, submit a test message addressed to that same identity, and confirm it appears with correct subject and body when viewed in the portal.

**Acceptance Scenarios**:

1. **Given** the environment is running, **When** the developer chooses to log in, **Then** they are able to complete a full sign-in without providing any real credentials, and land on their inbox.
2. **Given** a signed-in developer, **When** a test message is submitted addressed to that developer's identity, **Then** it appears in their inbox without any manual step beyond submitting it.
3. **Given** a message has arrived, **When** the developer opens it in the portal, **Then** its subject and body render correctly.

---

### User Story 3 - Verify relay-with-approval mode end to end (Priority: P1)

A developer wants to confirm the human-approval relay gate actually works: that a submitted message is held for review rather than delivered, that a reviewer can see it (including any address hidden from other recipients), and that pressing the approval action genuinely relays the message somewhere real — not just that the portal claims success.

**Why this priority**: This is the feature that most needs a trustworthy pre-ship check, since a bug here means either mail silently never leaves (annoying) or mail leaves without real approval (a safety failure). Equal priority to the other two because the whole point of this rig is to make this checkable without a live Gmail account.

**Independent Test**: With the environment up in relay mode, submit a message with an address that is not visible to the other recipients, log in as an authorized reviewer, confirm the message is queued and the hidden address is flagged as such, approve it, and confirm both the portal and the stand-in destination server agree it was delivered.

**Acceptance Scenarios**:

1. **Given** the environment is running in relay mode, **When** a test message is submitted, **Then** it is held for review and is visible only to an authorized reviewer identity, not delivered anywhere.
2. **Given** a submitted message included an address not present in its visible recipient list, **When** a reviewer opens the queued message, **Then** that address is shown and clearly marked as hidden from the other recipients.
3. **Given** a reviewer is viewing a queued message, **When** they press the approval action, **Then** the portal reports it as delivered, and the stand-in destination server independently confirms it actually received the message — not merely that the portal displayed success.
4. **Given** the stand-in destination server, **When** the application relays a message to it, **Then** the connection is genuinely encrypted and the server's identity is genuinely verified — this run exercises the actual verification mechanism rather than one where it has been switched off for convenience. (Proving that mechanism would *reject* an impostor is the responsibility of a test elsewhere, not something this scenario stages live.)

---

### Edge Cases

- **Environment brought up with a port already in use on the developer's machine**: fails with a clear, attributable error rather than silently binding to the wrong place or hanging.
- **Developer tries to review relay-mode messages while logged in with an identity that is not an authorized reviewer**: sees no queue and cannot approve anything, matching the application's existing access rules.
- **Developer submits a test message or signs in before the startup command has reported completion**: not an expected path, since the startup command's completion is the readiness signal (FR-001) — but if it happens anyway, it either waits/retries or fails clearly rather than silently disappearing.
- **Developer runs the capture-only and relay-mode checks in the same session**: each operates against its own independent instance of the application, so actions in one do not affect the other.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The environment MUST be brought up and torn down using a single command each, requiring no manual, per-run configuration of any real external account or service. The startup command MUST NOT return control (or must otherwise clearly signal completion) until every component is genuinely ready to use — a developer acting immediately after it completes must not hit failures caused by something still starting up.
- **FR-002**: The environment MUST provide a stand-in identity provider that lets a developer complete a full sign-in to the portal using a throwaway, developer-chosen identity, with no real credentials of any kind.
- **FR-003**: The environment MUST provide a way to submit a test mail message into the application non-interactively (no manual prompts), so it can be triggered on demand as part of a verification pass.
- **FR-004**: The test message submission capability MUST support specifying sender, recipient(s), a recipient hidden from the other visible recipients, subject, and body, so it can exercise both operating modes' distinguishing behaviors.
- **FR-005**: The environment MUST provide a stand-in destination mail server capable of genuine encrypted delivery, so relay-with-approval mode's requirement for an encrypted connection can be exercised for real rather than bypassed.
- **FR-006**: The relayed connection to the stand-in destination server MUST go through the application's genuine trust-checking mechanism — not a configuration that accepts any server unconditionally. That the mechanism itself would reject an impostor is established by a correctness test of the mechanism outside this feature; this environment only needs to exercise it honestly on the path that succeeds.
- **FR-007**: The environment MUST support running the application in both capture-only mode and relay-with-approval mode at the same time, as independent instances, so both can be verified without reconfiguring and restarting between them.
- **FR-008**: Every component substituting for a real external dependency (identity provider, destination mail server) MUST be fully disposable: safe to run locally with no sensitive or real-world data, and requiring no external network account to operate.
- **FR-009**: Tearing down and re-creating the environment MUST always produce a clean starting state, with no mail, queued messages, or review history carried over from a previous run.
- **FR-010**: Bringing up or tearing down this environment MUST NOT alter, depend on, or be confused with the configuration used for an actual production-style deployment of the application.
- **FR-011**: The environment MUST make it possible to confirm, for relay-with-approval mode, that a message address not shown in the visible recipient list is nonetheless surfaced to the reviewer and flagged as hidden before approval.
- **FR-012**: The environment MUST make it possible to independently confirm that an approved relay message was actually received by the stand-in destination server, not solely that the application's own portal reports success.

### Key Entities

- **Local Test Environment**: The complete, disposable set of running components a developer brings up and tears down together; the unit of "one command to start, one command to stop."
- **Stand-In Identity Provider**: Substitutes for a real OAuth identity provider; lets a developer complete a sign-in as any throwaway identity they choose, with no real account.
- **Test Mail Sender**: The non-interactive means of submitting a test message into the application, with control over sender, recipients (including a hidden one), subject, and body.
- **Stand-In Destination Server**: Substitutes for a real upstream mail provider; the place relay-with-approval mode's approved messages are actually delivered to during verification, reachable only over a genuinely encrypted, identity-verified connection.
- **Throwaway Identity**: Any developer-chosen email address used to sign in during verification; carries no real-world meaning and is not tied to a real account.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A developer can go from a clean checkout to a fully running, genuinely ready-to-use local environment with a single command that does not return until that readiness is real, in under 5 minutes on a typical development machine.
- **SC-002**: A developer can complete a full capture-only verification pass (sign in, submit mail, see it rendered) using only throwaway identities, with zero real external accounts involved.
- **SC-003**: A developer can complete a full relay-with-approval verification pass (submit mail with a hidden recipient, sign in as a reviewer, see the hidden recipient flagged, approve, confirm delivery) using only throwaway identities and the stand-in destination server, with zero real external accounts involved.
- **SC-004**: Every teardown-and-restart of the environment results in an empty starting state, verified across repeated cycles.
- **SC-005**: The relay verification runs through the application's genuine trust-checking mechanism rather than a bypassed one; that the mechanism correctly rejects an impostor is guaranteed by a test of the mechanism itself, outside this feature, not by this environment staging that failure live.
- **SC-006**: A developer relying on this environment can state, with independent evidence from the stand-in destination server (not solely the application's own UI), whether an approved message was actually delivered.

## Assumptions

- **Both operating modes need independent coverage**: because a single running instance of the application is in only one mode at a time, verifying both modes requires two independently running instances within the same environment, not a single instance switched back and forth.
- **This produces a reusable local environment, not a permanent automated test suite**: the deliverable is an environment a developer brings up and drives themselves (including via browser automation as part of confirming this feature works); building a checked-in, continuously-run automated browser test suite is a separate, later concern.
- **State does not need to persist across restarts**: a clean slate on every startup is preferred over preserving history between runs, since this environment exists purely for point-in-time verification.
- **Throwaway identities and any credentials used within this environment are inherently non-sensitive**: nothing here represents a real account, so these are safe to keep as part of the environment's own configuration.
- **This environment is additional to, not a replacement for, verifying against a real identity provider and a real mail provider before an actual production release.**

## Out of Scope

- A permanent, continuously-run automated test suite or CI integration; this feature delivers the environment and a one-time verification pass through it.
- Verifying against a real OAuth identity provider or a real upstream mail provider (Gmail or otherwise) — that remains a separate, existing pre-release step.
- Load, performance, or concurrency testing of any kind.
- Verifying time-dependent behavior such as message retention/purging, which depends on the passage of real time.
- A live, in-rig demonstration of relay failure against an impostor or untrusted server; that guarantee is established by a correctness test of the underlying trust-checking mechanism, outside this feature.
- Any change to how the application behaves in a real, non-test deployment.
