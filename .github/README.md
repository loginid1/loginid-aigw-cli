# aigw

`aigw` is the command-line interface for the AIGW Developer Console. Use it to set up
projects, resources and agent policies from a terminal or a script, instead of
clicking through the console.

```console
$ aigw login
$ aigw projects list
$ aigw resources create --project-id <project-id> --name checkout --kind OAUTH_CLIENT \
    --oauth-client.redirect-uris https://example.com/callback
```

## Install

`aigw` runs on macOS, Linux and Windows, on arm64 (including Apple silicon) and
x86-64.

### Homebrew (macOS and Linux)

```sh
brew install loginid1/tap/aigw-cli
aigw version
```

`brew upgrade aigw-cli` updates it.

### Download (macOS, Linux and Windows)

Each [release](https://github.com/loginid1/loginid-aigw-cli/releases) has a zip for
each OS and CPU, named `aigw_<version>_<os>_<arch>.zip`:

| OS | CPU | File |
|---|---|---|
| macOS | Apple silicon | `aigw_<version>_darwin_arm64.zip` |
| macOS | Intel | `aigw_<version>_darwin_amd64.zip` |
| Linux | arm64 | `aigw_<version>_linux_arm64.zip` |
| Linux | x86-64 | `aigw_<version>_linux_amd64.zip` |
| Windows | x86-64 | `aigw_<version>_windows_amd64.zip` |
| Windows | arm64 | `aigw_<version>_windows_arm64.zip` |

Download the zip and `checksums.txt` from the same release, then check, unzip and
install it. For example, for Linux on x86-64:

```sh
sha256sum --ignore-missing -c checksums.txt
unzip aigw_<version>_linux_amd64.zip
sudo install aigw /usr/local/bin/
aigw version
```

On macOS, check with `shasum -a 256 --ignore-missing -c checksums.txt`. The binary is
not notarized yet. If you downloaded it in a browser, macOS blocks the first run
because it "cannot verify" `aigw`. Clear that with:

```sh
xattr -d com.apple.quarantine aigw
```

Homebrew does this for you.

### Windows

In PowerShell, in the folder with the zip and `checksums.txt` (use `arm64` in place of
`amd64` on an arm64 PC):

```powershell
$zip = "aigw_<version>_windows_amd64.zip"
(Get-FileHash $zip -Algorithm SHA256).Hash.ToLower()   # must match the line for $zip:
Select-String -SimpleMatch $zip checksums.txt
Expand-Archive $zip -DestinationPath "$env:LOCALAPPDATA\Programs\aigw"
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
[Environment]::SetEnvironmentVariable("Path", "$userPath;$env:LOCALAPPDATA\Programs\aigw", "User")
```

Open a new terminal, then run `aigw version`. The binary is not signed yet, so if
Windows warns that the file came from the internet, run
`Unblock-File "$env:LOCALAPPDATA\Programs\aigw\aigw.exe"`.

In Git Bash, prompts such as `aigw login` need a real console: run them as
`winpty aigw login`, or use Windows Terminal.

## Log in

`aigw` authenticates with a devcon API key. Keys start with `aigw_sk_`. Create one in
the Developer Console, then:

```sh
aigw login
```

`login` asks for the key without showing it, lists your projects with it to check it
works, and then saves it in the OS credential store (macOS Keychain, Windows
Credential Manager, or the Secret Service keyring on Linux). A key the API rejects is
not saved.

To check what `aigw` will use:

```console
$ aigw status
API:    https://aigw.loginid.io
Key:    aigw_sk_…X7kQ (saved login)
Check:  ok (118 ms)
```

Other ways to give a key:

| Where | Use |
|---|---|
| `--api-key <key>` | One command. The key ends up in your shell history. |
| `AIGW_API_KEY` | CI and scripts. Wins over the saved login. |
| `aigw login --with-key-stdin < key.txt` | Save a key without a prompt. Add `--confirm` to replace a saved key. |
| `aigw login --insecure-storage` | Machines with no credential store. The key goes in a plain file instead: mode `0600` on macOS and Linux; on Windows, protected only by your user folder's permissions. |

`aigw logout` removes the saved key.

## Commands

Commands have the shape `aigw <noun> <operation>`. Run `aigw <noun> --help` to see
the operations, and `aigw <noun> <operation> --help` for flags and an example.

| Command | What it manages |
|---|---|
| `projects` | Projects: `list`, `get`, `create`, `update`, `delete` |
| `projects proxy-clients` | OAuth clients for a proxy project |
| `projects agent-policy` | The project's default agent policy |
| `projects domain-rules` | Approve, hold or reject agents by domain, for the whole project |
| `projects kya-issuers` | Which Know Your Agent (KYA) issuers the project trusts |
| `resources` | Resources: `list`, `get`, `create`, `update`, `replace`, `delete` |
| `resources auth-settings` | Sign-in page appearance and text |
| `resources scopes`, `resources signing-keys` | A resource's scopes and signing keys |
| `resources agent-policy`, `resources domain-rules`, `resources kya-issuers` | The same policies, for one resource |
| `status` | The API and key in use, and a check of the key |
| `login`, `logout` | Save or remove the API key |
| `version` | The `aigw` version |

### How agent access is decided

Three settings decide what happens when an AI agent calls a resource. Each can be set
on the project and overridden on a resource.

- **Agent policy** gives each kind of proof an agent can show a decision: `APPROVE`,
  `PENDING` (a person decides) or `REJECT`. With no policy set, every agent is approved.
- **Domain rules** set a decision for one domain and its subdomains. An `APPROVE` rule
  can ask for particular proofs with `--methods`.
- **KYA issuers** list the credential issuers whose credentials count as proof.

```sh
aigw projects agent-policy update --project-id <project-id> --default-decision PENDING
aigw projects domain-rules set agents.example.com --project-id <project-id> \
    --decision APPROVE --methods WBA --methods RDNS
```

### Request bodies

Each body field has a flag, as `--help` lists. For a field with no flag, or a nested
one, use `-d` with brackets:

```sh
aigw projects kya-issuers replace --project-id <project-id> \
    -d "entries[0][issuerName]=<issuer>" -d "entries[0][minTrust]=KNOWN"
```

`-d` refuses fields the API does not have. A flag and `-d` cannot set the same field.

### Output

Commands print the API's JSON response on stdout, ready for `jq`. Warnings and
prompts go to stderr, so stdout always stays JSON. `status` and `version` print text,
or JSON with `-o json`.

A `delete`, `remove`, `revoke` or `clear` prints its own verb and the IDs it acted on,
e.g. `{"deleted": true, "projectId": "…"}` or `{"revoked": true, ...}`.

Some responses carry a secret the API never returns again: the `clientSecret` from
`resources create` and `projects proxy-clients create`. `aigw` warns on stderr when
one does. Store it at once. To keep it off the screen and out of logs, pass
`--secret-file <path>`: the secret goes to that new file (mode `0600` on macOS and
Linux), and the output says where it went instead.

```sh
aigw projects proxy-clients create --project-id <project-id> --secret-file ./proxy-secret
```

### Deleting

A delete asks first. The question names what it deletes and what goes with it:
deleting a project also deletes its resources and proxy clients. Pass `--confirm` to
skip the question. With no terminal, as in scripts, CI or an AI agent, `--confirm` is
required: a delete never runs because nobody was there to answer.

## Scripts, CI and AI agents

- Give the key with `AIGW_API_KEY`.
- Read stdout as JSON. Check the exit code before you read it.
- Pass `--confirm` to deletes on purpose. Without it, they fail.
- Use `aigw status -o json` to check the setup before a run.

| Exit code | Meaning |
|---|---|
| 0 | Success |
| 1 | Any other error: bad input, not found, no network |
| 3 | Not authenticated: no key, or the API rejected it |
| 4 | The key lacks a scope. The error names the scope, e.g. `project:write` |

Each command's `--help` names the scope it needs.

## Use with Claude Code

`aigw` carries a [Claude Code skill](https://code.claude.com/docs/en/skills)
that teaches Claude how to use it: check the login first, find commands with `--help`,
ask you before every delete, and keep secrets out of the chat.

```sh
aigw skill install
```

This writes `~/.claude/skills/aigw/SKILL.md` (or `$CLAUDE_CONFIG_DIR/skills/aigw/` when
that is set). Start a new Claude Code session to use it. The skill ships inside `aigw`,
so it always matches your version: run `aigw skill install` again after you upgrade.

`aigw skill uninstall` removes it. To give the same instructions to another AI tool,
add them to its instructions file, for example `aigw skill show --body >> AGENTS.md`.

The skill does not pre-approve any command. Claude Code still asks you before it runs
`aigw`, as it does for any shell command.

## Configuration

`aigw` keeps its settings in `~/.config/aigw/config.toml`, or in
`$XDG_CONFIG_HOME/aigw/` when that is set. The file never holds the key itself, only
where it is stored and a masked hint.

## License notices

`aigw` includes third-party open-source software. Their licenses are in
[THIRD_PARTY_NOTICES.txt](../THIRD_PARTY_NOTICES.txt).
