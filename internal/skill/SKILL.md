---
name: aigw
description: Manage LoginID AIGW (AI Gateway) projects, resources, OAuth and proxy clients, and agent access rules (agent policies, domain rules, KYA issuers) with the aigw CLI. Use when the user asks to list, create, change or delete anything in their AIGW Developer Console, or to decide which AI agents may reach their resources.
---

<!-- Written by aigw {{version}}. `aigw skill install` replaces this file; edits are lost. -->

# aigw

`aigw` is the CLI for the AIGW Developer Console. Every API command prints the API's JSON
on stdout. Warnings and prompts go to stderr.

## Before anything else

Run `aigw status -o json`.

- `"check": "ok"`: go ahead.
- `"check": "not_logged_in"`, or exit code 3: stop. Ask the user to run `aigw login` in
  their own terminal. Never ask for an API key in chat, never pass one with `--api-key`,
  and never set `AIGW_API_KEY` yourself.
- Anything else: show the user the `error` field and stop.

## Finding the right command

Commands have the shape `aigw <noun> <operation>`. Do not guess flags: run
`aigw <noun> --help` to list operations, and `aigw <noun> <operation> --help` for flags,
the scope the key needs, and an example.

| Command | What it manages |
|---|---|
| `aigw projects` | Projects: `list`, `get`, `create`, `update`, `delete` |
| `aigw projects proxy-clients` | OAuth clients for a proxy project |
| `aigw resources` | Resources in a project: `list`, `get`, `create`, `update`, `replace`, `delete` |
| `aigw resources auth-settings` | A resource's sign-in page |
| `aigw resources scopes`, `aigw resources signing-keys` | A resource's scopes and signing keys |
| `aigw projects agent-policy`, `aigw resources agent-policy` | Decisions per kind of agent proof |
| `aigw projects domain-rules`, `aigw resources domain-rules` | Decisions per agent domain |
| `aigw projects kya-issuers`, `aigw resources kya-issuers` | Trusted Know Your Agent credential issuers |

IDs come from `list` output. Pass `--project-id` for anything inside a project. Do not
make up IDs.

Body fields have their own flags. For a field with no flag, or a nested one, use
`-d "a[b][0]=value"`. Never set the same field with a flag and with `-d`: the CLI
refuses it.

## How agent access is decided

Three settings decide what happens when an AI agent calls a resource. Each can be set
on the project, and a resource can override it.

- **Agent policy**: one decision (`APPROVE`, `PENDING` for a person to decide, or
  `REJECT`) per kind of proof: `platform`, `wba` (Web Bot Auth), `rdns` (reverse DNS),
  `kya` (a trusted credential), `cimd` (metadata document), and `default-decision` when
  there is no proof. No proof's rule may be stricter than `default-decision`, so set
  them together when tightening. With nothing set, every agent is approved.
- **Domain rules**: one decision for a domain and its subdomains. An `APPROVE` rule can
  require proofs with `--methods`.
- **KYA issuers**: which credential issuers count as `kya` proof, each with a
  `minTrust` level.

Explain to the user which of the three you are about to change, and why, before you
change it.

## Rules

- **Deletes.** `delete`, `remove`, `revoke` and `clear` cannot be undone, and deleting a
  project also deletes its resources and proxy clients. Before running one, tell the
  user exactly what it removes and wait for a clear yes. Only then pass `--confirm`.
  Without `--confirm` the command fails, because there is no terminal to ask on.
- **Replace and set.** These are not deletes, so they have no prompt and no
  `--confirm`, but they overwrite what is there:
  - `resources replace` gives every field the value sent, and resets any field you
    leave out. A `clientSecret` you send replaces the stored one.
  - `kya-issuers replace` makes the entries you send the whole list. With no entries it
    clears the list.
  - `domain-rules set` and `kya-issuers set` replace any rule or entry already there for
    that domain or issuer. `kya-issuers set` without `--min-trust` sets it back to
    `KNOWN`.

  Prefer `update` where there is one: it changes only the fields you name. Before any
  `replace` or `set`, `get` or `list` the current state, tell the user what will change
  and what will be dropped, and wait for a clear yes.
- **Secrets.** `resources create` and `projects proxy-clients create` can return a
  `clientSecret` that the API never shows again. Tell the user to store it now. Do not
  repeat it, write it to a file, or put it in a commit unless the user asks.
- **One change at a time.** After each change, read the result back (`get` or `list`)
  and tell the user what changed.

## Exit codes

| Code | Meaning | What to do |
|---|---|---|
| 0 | Success | Read stdout as JSON |
| 1 | Bad input, not found, no network | Read the error; fix the command or ask the user |
| 3 | Not authenticated | Ask the user to run `aigw login` |
| 4 | The key lacks a scope | Tell the user the scope the error names; they need a key with it |
