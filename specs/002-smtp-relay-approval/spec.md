# Feature Specification: SMTP Relay with Human Approval

**Feature Branch**: `002-smtp-relay-approval`
**Created**: 2026-08-06
**Status**: Draft
**Input**: User description: "though we started this project as a mock smtp server, we are pivoting. the goal is to make this as a smtp relay server, that can relay incoming emails to a real server (using google /gmail for example). But the relay should happen when the user logs into the portal, view the email and press a button 'Send Now'. The ability to relay will be behind a configuration and not enabled always. But this mode would be used for situations like, AI agents sending email, but that needs to be reviewed by real human before the actual delivery. So, in this mode, the admin login should be able to see all emails that are queued, and once approved, should be delivered via real server."

## Overview

The product gains a second operating mode. Today it is a capture-only mock mail server: it accepts mail and shows it in a portal, and nothing ever leaves the system. This feature adds an opt-in **relay-with-approval** mode in which accepted mail is held in a review queue, a human reviewer inspects it in the portal, and only an explicit "Send Now" action causes the message to be delivered to real recipients through a real upstream mail provider.

The driving use case is automated senders — AI agents in particular — that must not reach real recipients without a human in the loop. The system becomes the enforced approval gate: the agent's mail client sees an ordinary SMTP server, while every outbound message is parked until a person releases it.

Capture-only mode remains the default and is unchanged, so existing testing and development workflows keep working exactly as they do today.

## Clarifications

### Session 2026-08-06

- Q: In relay mode, what do non-reviewer portal users see, and are per-recipient user accounts still auto-created for external recipients? → A: No per-recipient owner is created for queued messages. Queued mail is visible only through the reviewer queue; non-reviewers see only mail captured in capture-only mode.
- Q: Does "Send Now" relay synchronously or hand off to a background worker? → A: Synchronous, bounded by a configurable timeout; the reviewer gets the final outcome in the same interaction. A timed-out attempt settles as Failed but is classified indeterminate, since the upstream may have accepted it.
- Q: Can the reviewer inspect attachments, or only see their filenames? → A: Inline preview for common types (images, PDF, plain text) with download as fallback for everything else. Easy attachment review is essential to the gate, so message content must be rendered in isolation from the portal's security context.
- Q: Does the reviewer see and approve the envelope recipients or the To/Cc header recipients? → A: The envelope recipients — the true delivery list. Blind-carbon recipients are surfaced to the reviewer and flagged as hidden from other recipients, so no address can receive an approved message unseen.
- Q: How long is stored mail kept, now that complete raw messages with attachments are retained? → A: Configurable retention, off by default. Once a message reaches a terminal state, its content and attachments may be purged after N days while metadata and audit records are kept permanently. Messages awaiting review or unresolved are never purged.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Reviewer releases a queued message (Priority: P1)

An AI agent composes an email and submits it to the mail server as it always has. Instead of being delivered, the message lands in a review queue. A reviewer logs into the portal, sees the queue of everything awaiting review, opens a message, reads the full content and recipient list, and presses **Send Now**. The system relays the message through the configured upstream provider to the real recipients and marks it as sent, recording who approved it and when.

**Why this priority**: This is the entire point of the feature. Without it there is no human-in-the-loop delivery, and no other story has value on its own.

**Independent Test**: Enable relay mode, submit a message from any mail client, confirm it appears in the queue and is not delivered, press Send Now in the portal, and confirm the real recipient receives it and the portal shows it as sent with the approver recorded.

**Acceptance Scenarios**:

1. **Given** relay mode is enabled and an authenticated sender submits a message addressed to an external recipient, **When** the server accepts the message, **Then** the message is stored in "Pending Review" state and no delivery to the recipient occurs.
2. **Given** a message is in "Pending Review", **When** an authorized reviewer opens the review queue, **Then** the message is listed with its sender, recipients, subject, and time received, regardless of which recipient it was addressed to.
3. **Given** a reviewer is viewing a pending message, **When** they press "Send Now", **Then** the message is relayed to the real recipients through the configured upstream provider and its state becomes "Sent".
4. **Given** a message has been sent, **When** the reviewer views it again, **Then** the portal shows the sent timestamp and the identity of the person who approved it, and the "Send Now" action is no longer available.
5. **Given** a message with attachments and HTML content is queued, **When** it is approved and relayed, **Then** the recipient receives the message with its subject, body parts, and attachments intact.
6. **Given** a message submitted by an agent using its own sender address, **When** it is relayed, **Then** the recipient sees the configured relay identity as the sender and a reply is routed back to the agent's original address, while the portal still shows the reviewer the address the agent actually submitted.
7. **Given** an authenticated portal user whose address is not on the reviewer list, **When** they open the portal or call the API directly, **Then** they see no review queue and no send action, and no queued message is retrievable — including one addressed to their own address.
8. **Given** relay mode is enabled and a message is queued for an external recipient, **When** that recipient's address is checked against the portal's user accounts, **Then** no account has been created for it, so relaying can never provision a portal identity for an outsider.
9. **Given** a queued message carries a PDF and a file of an uncommon type, **When** the reviewer opens it, **Then** the PDF is previewable inline and the other file is downloadable, so both are inspected on their contents rather than their filenames.
10. **Given** an agent submits a message with a blind-carbon recipient that appears in no visible header, **When** the reviewer opens it, **Then** that address is listed and marked as hidden from the other recipients; and **When** the message is approved and relayed, **Then** that address receives it while the other recipients' copies still do not reveal it.

---

### User Story 2 - Operator turns relay capability on and off (Priority: P1)

An operator deploying the system decides whether this instance is a harmless capture-only mock server or a live relay. Relay is off unless deliberately switched on and pointed at an upstream provider. With relay off, the system cannot send mail anywhere, and the portal offers no send controls.

**Why this priority**: Equal to P1 because it is the safety boundary. An instance that could accidentally deliver real mail to real people is worse than no feature at all. Story 1 cannot ship without this gate.

**Independent Test**: Deploy with no relay configuration, confirm the portal shows no send controls and no message can leave the system; then enable relay with valid upstream settings and confirm the queue and send controls appear.

**Acceptance Scenarios**:

1. **Given** relay mode is not enabled, **When** mail is submitted and viewed in the portal, **Then** the system behaves exactly as it does today (capture, store, display) and exposes no approval or send action.
2. **Given** relay mode is enabled but upstream delivery settings are missing or incomplete, **When** the system starts, **Then** it refuses to start and reports which settings are missing, rather than starting in a state where approvals would silently fail.
3. **Given** relay mode is enabled but no reviewer addresses are configured, **When** the system starts, **Then** it refuses to start and says so, rather than queueing mail nobody is permitted to release.
4. **Given** relay mode is enabled with valid settings, **When** the operator inspects the running system, **Then** the active mode is discoverable in the portal and in the startup logs.
5. **Given** relay mode is enabled, **When** anyone inspects the portal, the API responses, or the logs, **Then** upstream credentials are never exposed.

---

### User Story 3 - Reviewer rejects a message (Priority: P2)

A reviewer opens a queued message and decides it must not go out — the agent got the recipient wrong, the tone is off, or it contains something it should not. They reject it. The message is never delivered, and the rejection is recorded.

**Why this priority**: The gate is only useful if it can say no. But the queue still delivers its core value with approve-only, so this follows P1.

**Independent Test**: Queue a message, reject it in the portal, confirm no delivery occurs, the state shows "Rejected", and the message can no longer be sent.

**Acceptance Scenarios**:

1. **Given** a message is in "Pending Review", **When** the reviewer rejects it, **Then** its state becomes "Rejected" and no delivery is attempted.
2. **Given** a message has been rejected, **When** any reviewer views it, **Then** the "Send Now" action is unavailable and the rejection is shown with the deciding person and timestamp.
3. **Given** a message has been rejected, **When** the audit record is inspected, **Then** the rejection is retained even after the message is removed from the inbox view.

---

### User Story 4 - Delivery to the upstream provider fails (Priority: P2)

A reviewer approves a message but the upstream provider rejects it or is unreachable. Rather than silently losing the message or pretending it was sent, the system shows the reviewer what went wrong and lets them try again once the problem is fixed.

**Why this priority**: Upstream failures are routine — bad credentials, rate limits, rejected sender. Without this the reviewer cannot tell "sent" from "lost", which destroys trust in the gate. It is P2 only because the happy path can be demonstrated first.

**Independent Test**: Point relay at an unreachable or rejecting upstream, approve a message, confirm it shows as "Failed" with a readable reason and remains retriable; fix the upstream and retry successfully.

**Acceptance Scenarios**:

1. **Given** an approved message whose upstream delivery fails, **When** the attempt completes, **Then** the message state becomes "Failed", the failure reason is shown to the reviewer in plain language, and the message content is retained.
2. **Given** a failed message, **When** the reviewer retries it, **Then** a fresh delivery attempt is made and each attempt is recorded separately.
3. **Given** an upstream delivery succeeds only for some recipients, **When** the attempt completes, **Then** the portal shows which recipients succeeded and which did not, and a retry does not re-deliver to recipients already served.
4. **Given** two reviewers press "Send Now" on the same message at the same moment, **When** both actions are processed, **Then** the message is delivered exactly once and the second reviewer is told it was already handled.
5. **Given** an approved message whose upstream attempt exceeds the configured timeout, **When** the attempt is abandoned, **Then** the message settles as Failed–indeterminate and is presented to the reviewer as possibly-delivered rather than as a clean failure.
6. **Given** a message in Failed–indeterminate state, **When** the reviewer retries it, **Then** they must first explicitly confirm that a duplicate may reach the recipient.
7. **Given** the process is killed while a message is being relayed, **When** the system restarts, **Then** that message is no longer in "Sending" and appears as Failed–indeterminate.

---

### User Story 5 - Auditing what was released and by whom (Priority: P3)

Someone asks later: who let that email out? The system can answer. Every state change carries an actor and a timestamp, and the history survives the message being cleared from the inbox view.

**Why this priority**: Essential for the governance value of the feature, but the gate functions without a queryable history in the first release.

**Independent Test**: Approve one message and reject another, then inspect the history for both and confirm the actor, timestamp, and outcome are recorded for every transition.

**Acceptance Scenarios**:

1. **Given** any message that has changed state, **When** its history is inspected, **Then** each transition shows what changed, who caused it, and when.
2. **Given** a message that was deleted from the inbox view, **When** its history is inspected, **Then** the approval or rejection record is still present.
3. **Given** any delivery attempt, **When** logs are inspected, **Then** the attempt is logged with its outcome and no credentials or full message bodies are written to the logs.

---

### Edge Cases

- **Relay mode toggled off while messages are pending**: pending messages remain stored and visible but cannot be sent; the portal explains why the send action is unavailable.
- **Relay mode toggled on with capture-only messages already stored**: pre-existing messages are treated as historical capture records, not as newly sendable items.
- **Submitted `From` does not belong to the relay account**: expected and normal — an AI agent may submit as anything. The system rewrites `From` to the relay identity and routes replies back to the original sender via `Reply-To` (FR-013, FR-013a).
- **Submitted message already carries a `Reply-To`**: the sender's explicit choice wins; the system leaves it alone rather than redirecting replies to the submitting address.
- **Relay mode enabled with an empty reviewer list**: startup fails, rather than accepting mail that nobody is permitted to release.
- **An authorized reviewer is removed from the reviewer list while messages are pending**: they immediately lose queue access; the pending messages stay queued for the remaining reviewers.
- **Message with no recipients, or with an unparseable address**: rejected at submission time with a clear error rather than queued.
- **Message uses Bcc**: the blind recipients appear in the reviewer's list, marked as hidden from the other recipients, and the single approval covers them explicitly. The relayed message's visible headers are not altered to expose them.
- **Envelope recipients and header recipients disagree for any other reason**: the envelope list governs, because that is what determines delivery; the reviewer approves what will actually be sent.
- **Very large message or attachment**: the system enforces a maximum accepted message size and refuses oversized submissions at submission time rather than at approval time.
- **Attachment type with no inline preview**: the reviewer is offered a download instead, so the attachment is never a filename they must take on trust.
- **Attachment or MIME part that cannot be parsed**: the reviewer is told the part is unreadable rather than shown a blank preview, and can still download the raw part and reject the message.
- **HTML body containing a tracking pixel or remote image**: remote content is not fetched during review, so reviewing a message does not tell the sender it was read.
- **Message body or attachment containing active content**: it is rendered in isolation and cannot run in or read from the reviewer's portal session.
- **Recipient list mixes internal test addresses and real external addresses**: all listed recipients are treated the same — the human approval covers the whole recipient list shown at review time.
- **Queued message is addressed to an address that already has a portal account**: that account gains no visibility of the queued message; the queue stays reviewer-only.
- **An external recipient attempts to log into the portal**: no account exists for them as a result of relaying, so there is nothing for them to see.
- **Upstream is unreachable or slow**: the attempt is cut off at the configured timeout so the reviewer is not left waiting; the message settles as Failed–indeterminate rather than Sent.
- **Upstream connection drops after message content was transmitted**: the message settles as Failed–indeterminate, and retrying it requires the reviewer to confirm a duplicate may be delivered.
- **Process is killed mid-send**: on restart, any message still marked "Sending" is settled as Failed–indeterminate; nothing stays permanently in "Sending".
- **A message can never be delivered successfully**: the reviewer abandons it, moving it to Rejected, so it stops occupying the queue and eventually becomes purgeable.
- **Reviewer opens a message whose content has been purged**: the portal says the content was purged under the retention policy rather than showing a blank message, and offers no retry.
- **A second submission of a byte-identical message**: treated as a distinct queue item requiring its own approval; the system never assumes a repeat is already approved.
- **Sender submits while no reviewer is logged in**: the message queues indefinitely and is never auto-sent.

## Requirements *(mandatory)*

### Functional Requirements

#### Operating modes and configuration

- **FR-001**: System MUST support two operating modes selected by operator configuration: **capture-only** (the default) and **relay-with-approval**.
- **FR-002**: When relay-with-approval is not enabled, the system MUST behave exactly as it does today — accept, store, and display mail — and MUST NOT be capable of transmitting any message outside itself.
- **FR-003**: When relay-with-approval is enabled, the operator MUST be able to configure the upstream mail provider's destination, port, credentials, and connection security without code changes.
- **FR-004**: System MUST refuse to start when relay-with-approval is enabled but upstream settings are absent or incomplete, and MUST report which settings are missing.
- **FR-005**: System MUST make the active operating mode visible to reviewers in the portal, so a reviewer always knows whether pressing a button will reach real people.
- **FR-006**: System MUST NOT expose upstream credentials through the portal, any API response, or any log output.

#### Message intake and queueing

- **FR-007**: When relay-with-approval is enabled, every accepted message MUST enter the **Pending Review** state and MUST NOT be transmitted to any recipient until an authorized human explicitly approves it.
- **FR-008**: System MUST retain the complete original message — all headers, MIME structure, and attachments — so that the relayed copy is faithful to what the sender submitted, apart from header rewrites explicitly required by FR-013.
- **FR-009**: System MUST acknowledge the submitting client only after the message is durably stored, so an acknowledged submission is never lost.
- **FR-010**: System MUST continue to require authentication for message submission (per feature 001); unauthenticated clients MUST NOT be able to place anything in the review queue.
- **FR-011**: System MUST NOT automatically send a pending message under any condition — no timeout-based auto-approval, no scheduled release, no bulk auto-flush.
- **FR-012**: System MUST enforce a configurable maximum accepted message size and reject oversized submissions at submission time with a clear error.
- **FR-013**: When relaying, the system MUST replace the message's `From` address with the configured relay identity, because upstream providers reject or silently rewrite a `From` that does not belong to the authenticated relay account.
- **FR-013a**: When rewriting `From`, the system MUST ensure replies reach the original sender by setting `Reply-To` to the submitted sender address. If the submitted message already carries its own `Reply-To`, that value MUST be preserved and MUST NOT be overwritten.
- **FR-013b**: The originally submitted sender address MUST remain visible to the reviewer in the portal and MUST be retained in the stored message and in the audit record, so the rewrite never hides who actually composed the message.
- **FR-013c**: The envelope sender used when relaying MUST also be the configured relay account, since upstream providers require it to match the authenticated identity. The originally submitted envelope sender MUST be retained for display to the reviewer and in the audit record.

#### Review queue and access control

- **FR-014**: Authorized reviewers MUST be able to see every message in the review queue, regardless of which recipient the message was addressed to. The queue is instance-wide, not scoped to the reviewer's own address.
- **FR-015**: The queue listing MUST show, for each message, the sender, the complete envelope recipient list, the subject, the time received, and the current state.
- **FR-015a**: The recipient list shown to the reviewer MUST be the **envelope** recipient list — every address the message would actually be delivered to — and not the addresses named in the visible `To`/`Cc` headers. Any envelope recipient absent from the visible headers (a blind-carbon recipient) MUST be shown to the reviewer and clearly marked as hidden from the other recipients. No address may receive an approved message without a human having seen that address.
- **FR-015b**: Surfacing a blind-carbon recipient to the reviewer MUST NOT add that address to the message's visible headers when relayed; the recipient's blindness to other recipients MUST be preserved.
- **FR-016**: A reviewer MUST be able to view a message's full content before deciding: the plain-text body, the HTML body substantially as the recipient will see it, and every attachment with its filename, declared type, and size.
- **FR-016a**: A reviewer MUST be able to preview attachments of common types — at minimum images, PDF, and plain text — inline in the portal without leaving the review screen. Judging an attachment by its filename alone MUST NOT be the only option.
- **FR-016b**: Every attachment MUST be downloadable, including types with no inline preview, so that no attachment is un-inspectable.
- **FR-016c**: Rendering or previewing message content MUST NOT let that content execute in the portal's security context. Scripts, embedded objects, and active content in HTML bodies and previewed attachments MUST be prevented from running as — or reading data from — the reviewer's portal session. Downloads MUST be served so the browser cannot interpret them as portal-origin content.
- **FR-016d**: When a message body or attachment references remote content, the portal MUST NOT fetch it during review by default, so that opening a message for review does not signal the sender or a third party that it was read.
- **FR-016e**: Attachment previews and downloads MUST be available only to authorized reviewers, under the same access restriction as the queue itself (FR-018).
- **FR-017**: Access to the review queue and to approval actions MUST be restricted to authorized reviewers. A portal user is an authorized reviewer if, and only if, their authenticated email address appears in an operator-configured list of reviewer addresses. All other authenticated users are ordinary users with today's per-recipient inbox scope.
- **FR-017a**: The reviewer list MUST be configurable by the operator without code changes, and MUST be compared case-insensitively against the authenticated address.
- **FR-017b**: When relay-with-approval is enabled and the reviewer list is empty, the system MUST refuse to start, because no queued message could ever be released.
- **FR-018**: Users who are not authorized reviewers MUST NOT be able to view any queued message, nor invoke any approval or send action, through the portal or directly through the API. This restriction applies even to a message addressed to the requesting user's own authenticated address.
- **FR-018a**: When relay-with-approval is enabled, the system MUST NOT create, look up, or associate a portal user account for a message recipient. A queued message MUST have no owning portal user. Relaying to an external address MUST NEVER provision a portal identity for that address.
- **FR-018b**: Messages captured in capture-only mode MUST retain their existing per-recipient ownership and remain visible to their owning user. Enabling relay mode MUST NOT retroactively change the ownership or visibility of previously captured messages.
- **FR-019**: A reviewer MUST be able to filter or distinguish the queue by state, so pending work is separable from already-handled messages.

#### Approval, delivery, and outcomes

- **FR-020**: An authorized reviewer MUST be able to approve and release a pending message with a single explicit action ("Send Now").
- **FR-020a**: The "Send Now" action MUST perform the relay synchronously and return the final outcome in the same interaction, so the reviewer learns whether the message went out without having to re-check later.
- **FR-020b**: Every relay attempt MUST be bounded by a configurable timeout whose default keeps the reviewer's wait within the SC-007 budget. An attempt exceeding the timeout MUST be abandoned and settled, never left open.
- **FR-021**: Approval granularity MUST be per message, not per recipient. One "Send Now" action releases the message to every envelope recipient shown to the reviewer at review time. The system MUST NOT offer partial release to a subset of recipients; a reviewer who objects to any recipient MUST reject the whole message.
- **FR-021a**: A submitted message MUST appear in the review queue as exactly one item regardless of how many recipients it carries, so the reviewer sees one decision per message the sender composed.
- **FR-022**: A message MUST be relayed at most once. Concurrent or repeated approvals of the same message MUST NOT produce duplicate deliveries, and the losing caller MUST be told the message was already handled.
- **FR-023**: On successful relay, the message state MUST become **Sent** and MUST record the delivery timestamp, the approving reviewer's identity, and the upstream provider's acceptance response.
- **FR-024**: On failed relay, the message state MUST become **Failed**, MUST record a human-readable failure reason, MUST retain the full message content, and MUST remain eligible for retry.
- **FR-024a**: Every Failed message MUST be classified as either **confirmed-failed** — the upstream explicitly rejected it, or the connection failed before any message content was transmitted — or **indeterminate** — the attempt timed out or the connection dropped after transmission began, so the upstream may or may not have accepted it. The classification MUST be shown to the reviewer alongside the failure reason.
- **FR-025**: A reviewer MUST be able to retry a failed message; each attempt MUST be recorded separately, and a retry MUST NOT re-deliver to recipients already successfully served.
- **FR-025a**: Retrying a **confirmed-failed** message MUST be a single action. Retrying an **indeterminate** message MUST require a separate explicit confirmation in which the reviewer acknowledges that a duplicate may reach the recipient, because at-most-once delivery (FR-022) cannot be guaranteed for an indeterminate outcome.
- **FR-026**: An authorized reviewer MUST be able to reject a pending message. A rejected message MUST never be delivered and MUST be retained for audit.
- **FR-026a**: A reviewer MUST be able to abandon a failed message, moving it to Rejected without further delivery attempts, so that a message which can never be sent does not remain unresolved forever. Abandoning a message whose last outcome was indeterminate MUST record in the audit trail that its delivery status was unknown at the time it was abandoned.
- **FR-027**: Message states MUST be exactly: Pending Review, Sending, Sent, Failed, Rejected. Only these transitions are permitted: Pending Review → Sending, Pending Review → Rejected, Sending → Sent, Sending → Failed, Failed → Sending, Failed → Rejected. Sent and Rejected are terminal; every message therefore has a path to a terminal state and becomes eligible for retention purging.
- **FR-028**: A message MUST NOT remain in the **Sending** state once the attempt that put it there has ended. If the process is interrupted mid-attempt, the system MUST on restart settle any message still marked Sending as Failed–indeterminate, so a reviewer is never blocked by a stuck message and is never told a possibly-delivered message simply failed.
- **FR-029**: All connections to the upstream provider MUST be encrypted; the system MUST refuse to transmit a message or upstream credentials over an unencrypted connection.

#### Auditing

- **FR-030**: System MUST record, for every message state change, the actor who caused it, the timestamp, and the resulting outcome.
- **FR-031**: Audit records MUST be retained even after the message is removed from the reviewer's inbox view.
- **FR-032**: Every delivery attempt MUST be logged with its outcome, without writing credentials or full message bodies to the logs.

#### Retention

- **FR-033**: The operator MUST be able to configure a retention period after which the stored content of a message in a terminal state is purged. Retention MUST be disabled by default, so an operator who configures nothing keeps everything.
- **FR-034**: Purging MUST remove the raw message content and attachments while retaining the message's metadata — sender, envelope recipients, subject, timestamps, final state, and deciding reviewer — and all audit records, so that FR-031 and SC-004 continue to hold after a purge.
- **FR-035**: Messages not in a terminal state MUST NEVER be purged. Pending Review, Sending, and Failed messages are retained regardless of age, because purging them would destroy either work awaiting a human or evidence of an unresolved delivery.
- **FR-036**: A purged message MUST be identifiable as purged rather than appearing to be an empty message, and MUST NOT be retriable, since its content no longer exists.

### Key Entities

- **Queued Message**: A message accepted from a submitting client and awaiting or having completed review. Carries the original raw message content, the originally submitted sender address and envelope sender, the envelope recipient list (marking which addresses are absent from the visible headers), subject, received time, current state, and — once decided — the approving or rejecting reviewer and decision time. One queued message is one review decision, however many recipients it carries. A queued message has **no owning portal user** — recipients are external parties, not identities in this system. Once purged under the retention policy, it retains its metadata and is marked as purged.
- **Recipient Delivery**: The per-recipient outcome of relaying a queued message: the recipient address, whether it has been successfully delivered, and the upstream response for that address. Distinguishes partial success from total failure. This tracks delivery outcomes only — approval remains a single per-message decision (FR-021).
- **Delivery Attempt**: One try at relaying a message upstream. Records when it started, its outcome, whether a failure was confirmed or indeterminate, the upstream response or error, and which reviewer initiated it. A message may have several attempts across retries.
- **Audit Event**: An immutable record of a state change — which message, which transition, which actor, when.
- **Reviewer**: Referred to as the "admin login" in the original request; **reviewer** is the canonical term used throughout this spec. A portal user whose authenticated email address appears in the operator-configured reviewer list, and who may therefore view the instance-wide queue and act on it. Distinct from an ordinary portal user, who sees only mail addressed to them.
- **Relay Configuration**: The operator-supplied settings that enable relay mode: the upstream provider's destination, port, credentials, and connection security; the relay identity used to rewrite `From` and the envelope sender; the list of authorized reviewer addresses; the per-attempt delivery timeout; the maximum accepted message size; and the retention period for purging terminal messages.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: With relay mode disabled, every existing capture-and-view workflow behaves identically to the current release, and no message can leave the system by any path.
- **SC-002**: A message submitted by an automated sender is visible in the reviewer's queue within 5 seconds of the sender's client receiving acceptance.
- **SC-003**: A reviewer can go from opening the portal to a message being delivered in under 30 seconds and no more than three interactions.
- **SC-004**: Across any audit period, 100% of messages that reached a real recipient have a recorded human approval naming the approver — zero messages leave the system unattributed.
- **SC-004a**: Zero addresses receive a relayed message without having been displayed to the approving reviewer, blind-carbon recipients included.
- **SC-005**: Duplicate or concurrent approvals of the same message result in exactly one delivery, in 100% of attempts.
- **SC-006**: A relayed message arrives at the real recipient with its subject, body content, and attachments intact and equivalent to what the sender submitted; the only permitted differences are the `From` rewrite and the added `Reply-To` (FR-013).
- **SC-006a**: A reply sent by a recipient to a relayed message reaches the original submitting sender, not the shared relay account.
- **SC-007**: When upstream delivery fails, the reviewer sees the reason in the portal within 10 seconds of pressing Send Now — including when the upstream is unreachable, because the attempt is bounded by a timeout inside that budget — and the message remains retriable.
- **SC-007a**: No relayed message is delivered twice. An indeterminate outcome is never retried without the reviewer explicitly confirming that risk.
- **SC-008**: A reviewer can locate a specific pending message in a queue of 500 messages in under 15 seconds.
- **SC-008a**: A reviewer can inspect the actual contents of 100% of a message's attachments before deciding — inline for common types, by download for the rest — with no attachment reviewable by filename alone.
- **SC-008b**: Active content embedded in a message body or attachment cannot execute in the portal session or read the reviewer's credentials, verified by test with a hostile sample message.
- **SC-008c**: Opening a message for review issues zero network requests to sender-controlled or third-party hosts.
- **SC-009**: No credential value appears in any log line, API response, or portal view, verified by inspection.
- **SC-009a**: With retention configured, stored message content does not grow without bound: content older than the configured period in a terminal state is absent, while 100% of the corresponding audit records remain queryable.
- **SC-009b**: No message awaiting review or in an unresolved failed state is ever removed by retention, verified across a full retention cycle.
- **SC-010**: Relaying to external recipients creates zero new portal accounts, and a non-reviewer calling the API directly can retrieve zero queued messages — including any addressed to their own address.

## Assumptions

- **Dual-mode, not replacement**: the existing capture-only mock server behaviour is retained as the default. The pivot adds a mode, it does not remove the current product.
- **Relay is off by default**: a deployment that does not explicitly configure relay cannot deliver mail. This is the primary safety property.
- **One shared upstream account per instance**: the operator configures a single upstream provider account that all approved mail is relayed through. Per-user or per-sender upstream accounts are out of scope.
- **Recipients see the relay account, not the agent**: because `From` is rewritten (FR-013), external recipients see one consistent sending identity for the whole instance. Attribution to the original agent survives in `Reply-To`, in the portal, and in the audit record — but not in the recipient's From line.
- **Reviewers are a deploy-time list**: adding or removing a reviewer is a configuration change, not a portal action. There is no in-app reviewer management screen in this feature.
- **The envelope is the source of truth for delivery**: what the reviewer approves is the address list the server will actually deliver to, not the prettier list a mail client would display. Headers are for humans; the envelope is what happens.
- **Approval is all-or-nothing per message**: the reviewer's only two verbs are Send Now and Reject. Releasing to some recipients but not others is deliberately not offered, so that what the reviewer read is exactly what goes out.
- **No auto-approval of any kind**: pending messages wait indefinitely for a human. There is no timeout that releases mail, because the entire value of the feature is that a human decided.
- **No editing before sending**: the reviewer approves or rejects what the sender produced. Amending subject, body, or recipients before release is out of scope for this feature.
- **No automatic retries**: a failed delivery waits for a human to retry it. The system does not retry on its own, so it cannot surprise anyone with a delayed send.
- **Sending is synchronous and human-paced**: the reviewer's click drives one relay at a time and waits for the result. Throughput is bounded by how fast a person can review, so no send queue, worker pool, or background scheduler is needed.
- **The reviewer is shown untrusted content by design**: everything in the queue was composed by an automated sender and must be treated as hostile input by the portal, not merely as data to display. Easy inspection and safe isolation are both required; neither is traded for the other.
- **A timeout is not a failure, it is an unknown**: the system never claims a timed-out message failed to send. It records the outcome as indeterminate and makes the reviewer own the duplicate risk before retrying.
- **Existing submission authentication is a prerequisite**: this feature builds on feature 001 and assumes message submission already requires authentication.
- **Existing portal authentication is reused**: reviewers sign in through the identity provider already wired into the portal; no new login mechanism is introduced.
- **Retention only touches finished work**: purging applies to messages a human has closed out. Anything awaiting review, or failed and unresolved, is kept regardless of age. There is still no expiry that releases or discards pending mail.
- **Retention is off by default**: an operator who configures nothing keeps everything, exactly as today. Turning on purging is a deliberate act.
- **Metadata and audit outlive content**: after a purge the record of what was sent, to whom, and by whose approval survives; only the message body and attachments go.
- **The submitting client is told the message was accepted, not that it was delivered.** An automated sender receives a normal acceptance response; it is not expected to understand that a human gate exists.
- **Single instance**: coordination across multiple concurrently running instances of the service is not required, though the exactly-once guarantee (FR-022) must hold across concurrent reviewers within one instance.

## Out of Scope

- Editing or composing messages in the portal before release.
- Bulk approval of multiple messages in one action.
- Notifying reviewers (email, chat, webhook) that new mail is awaiting review.
- Per-sender or per-recipient auto-approval rules and allowlists.
- Partial release of a message to a subset of its recipients.
- In-app management of the reviewer list.
- Multiple upstream providers, or routing rules that pick a provider per message.
- Message signing (DKIM) or authentication record (SPF/DMARC) management.
- Receiving replies, bounce processing, or any inbound mail from the upstream provider.
- Scheduled or delayed sending.
