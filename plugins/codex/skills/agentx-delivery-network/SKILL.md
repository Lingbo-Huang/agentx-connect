---
name: agentx-delivery-network
description: Use a connected AgentX MCP only when work needs a private capability, another responsibility owner, durable cross-session delivery, or independently verifiable external results. Apply when searching AgentX services, invoking a precise capability, handing work to a provider or team, resuming a HandoffRef, reviewing Artifacts, or accepting/requesting revision of delivery. Do not trigger for ordinary work the current Host can reliably complete itself.
---

# AgentX Delivery Network

Keep ordinary work in the current Host. Do not call AgentX merely to decompose a task, switch models, add agents, or repeat work already possible with current tools and granted context.

## Choose the path

1. Use `PASS_THROUGH` when the Host can reliably finish the work itself.
2. Call `search_capabilities` only when one of these boundaries exists:
   - a connected private account, data source, API, or tool is missing;
   - another person, team, organization, or Provider owns required authority or responsibility;
   - work must survive this chat, Host, device, or process;
   - delivery needs independent evidence, verification, acceptance, or recovery.
3. Describe the required result, constraints, Artifact type, sensitivity, deadline, budget, and quality mode. Do not put secrets or full private documents in the search query.
4. Follow the Search disposition:
   - `MATCHES_FOUND`: use the exact returned `bindingId`, `bindingVersion`, and `executionMode`;
   - `CAPABILITY_GAP`: ask the user to adjust constraints or record the gap. Do not silently use a Runtime.
5. Treat `automaticFallbackAllowed=false` as a hard rule. A user-approved general capability must appear as a real Search match before use.

## Invoke short-lived capability

Use `invoke_capability`, or `use_capability` with `operation=INVOKE`, only for a short, deterministic capability without independent delivery responsibility.

- Reuse one idempotency key only for the identical request.
- Treat `InvocationReceipt` as the execution record.
- If the call is unavailable or denied, report the bounded error or ask the user; do not silently substitute another model, Provider, or Runtime.

## Handoff durable work

Use `handoff_work`, or `use_capability` with `operation=HANDOFF`, when work crosses a Principal/account/permission boundary, must outlive the Host, or needs a separately accountable delivery.

- State the complete goal and named expected Artifacts.
- Disclose only required named ContextReferences after user authorization.
- Preserve the returned `HandoffRef`; it is the short resume key, not the full task state.
- Do not claim completion when a Provider, Agent, or Runtime stops. AgentX Server owns Mission, Artifact, Verification, Recovery, and Acceptance facts.

## Resume and decide

1. Prefer the stored HandoffRef. If the user asks to “查看刚才的任务” or the Host no longer has that reference, call `list_handoffs`, or `use_capability` with `operation=LIST_HANDOFFS`, and show only the bounded safe summaries returned by AgentX. Do not guess an ID or search another Principal/Space.
2. Call `get_handoff_status`, or `use_capability` with `operation=GET_STATUS`, for the selected HandoffRef. After the first successful read, pass the last `updateCursor` as `knownUpdateCursor`; when `changed=false`, do not present the same Need You or Outcome as a new update.
3. Present bounded Need You items and the Server's exact next action.
4. Fetch only authorized ArtifactReferences. Open large results through the AgentX deep link instead of copying them into chat.
5. Refresh status immediately before a decision and use the exact `missionVersion`.
6. Accept only after the user has reviewed the Artifact and verification evidence.
7. On rejection, give a concrete revision reason. Preserve prior Artifacts; do not restart from zero.

A Handoff belongs to the authenticated Tenant/Space/Principal, not permanently to one Host installation. Another authorized installation for the same Principal and Space may resume it; the immutable origin installation remains in the audit trail.

Never expose AgentX credentials, Host tokens, cookies, payment secrets, or private keys in prompts, tool parameters, logs, or Artifacts.

`updateCursor` is only a caller-bound read acknowledgement. It is not authorization, an Acceptance version, or proof that an IM notification was delivered.
