---
name: cworker
description: >-
  Orchestrate and manage distributed Windows processes, jobs, and files across remote nodes
  using cworker (cw). Covers cluster inspection, async job execution, log retrieval,
  JobObject process killing, remote file operations, and cluster node management.
---

# cworker (cw) Agent Guide

- **Official Repository**: [https://github.com/crthu/cworker](https://github.com/crthu/cworker)
- **Primary Binary**: `cw` / `cw.exe` (Default deployment: `~/.cworker/bin/cw.exe`)
- **Self-Update**: Use `cw update -y` to upgrade `cworker` non-interactively to the latest GitHub release.

## 1. Safety & Execution Rules

- **Native Host OS (Zero Sandbox) & Confirmation Gate**:
  - Workers execute with native host user privileges directly on the physical OS. **There is NO Docker container, VM, or sandbox isolation**.
  - All operations (`cw rm`, `cw cp`, `cw run`, `cw kill`) directly affect the target machine's physical filesystem and processes with irreversible consequences.
  - **Human Confirmation Gate**: Agents MUST NOT execute destructive operations (such as recursively deleting non-temporary/non-self-created directories via `cw rm -r -y`, overwriting key project directories via `cw cp -r`, or terminating unknown jobs via `cw kill`) without first explicitly notifying the user of the target node, physical path, and irreversible risks, and obtaining explicit confirmation.
- **System Service Context & Mandatory `--dir`**:
  - Workers run as Windows Services (`SYSTEM` context): only System PATH is available (User PATH and user `%USERPROFILE%` are not inherited).
  - **Always specify `--dir "<path>"`** in `cw run` to avoid defaulting to `System32`. Use absolute paths for interpreters (or explicitly activate venvs) and target files.
- **`cw rm` MUST include `-r -y`**: Non-interactive recursive deletion requires both flags. Omitting `-y` causes the CLI to block on interactive `[y/N]` confirmation, deadlocking the agent.
  ```bash
  cw rm -r -y [<node>:]<path>
  ```
- **`cw cp` on Directories MUST include `-r`**: Copying directories requires `-r` (or `--recursive`). Concurrency can be tuned with `-j <num>` (default 8). Transfers include single-pass streaming SHA-256 integrity validation and clean non-TTY summary output.
- **`cw diff` on Directories MUST include `-r`**: Directory comparison requires `-r`. Diffs are based on SHA-256 and size. Identical files are omitted by default to keep context clean. Returns exit code 0 if identical, 1 if differences exist, 2 on error.
- **Unified Local & Remote Support**: All file commands (`cp`, `diff`, `cat`, `ls`, `md`, `rm`) natively support local paths (`[<node>:]<path>`). You can operate on local files, local directories, or cross-node seamlessly without switching tools.
- **Execution Modes (`cw run`)**:
  - **Async Long-running (Default)**: `cw run` returns immediately with a `job-<hex>` handle. Do not block; poll status with `cw ps` or inspect output with `cw logs <job_id>`.
  - **Foreground Sync with Auto-cleanup (Recommended for Probing: `-wc`)**: Use `cw run -wc "<cmd>"` (or `-w --clean`) for immediate tasks (e.g. `git status`, environment probing, short scripts). It streams output live, propagates the remote ExitCode directly to the CLI, and auto-destroys the job and log files upon exit without polluting `cw ps`.
- **Process Tree Cleanup**: `cw kill <job_id>` uses Win32 Job Objects to terminate the entire process hierarchy without leaving orphan processes. On `-w` synchronous runs, keyboard interrupts (Ctrl+C) automatically terminate the remote process tree and clean up.
- **`cw clean` MUST include `-y`**:
  - **Single Job Cleanup**: `cw clean [<node>:]<job_id> -y` deletes the specific finished job record and logs.
  - **Batch Cleanup**: `cw clean <--days <n> | --all> [-n <node>] -y` cleans batches of finished jobs. Active `RUNNING` jobs are strictly protected from deletion.
- **Text Slicing & 1MB Truncation (`cw cat` & `cw logs`)**:
  - Content $\le$ 1MB outputs in full; content $>$ 1MB automatically truncates to the last 100 lines with an override notice.
  - Flags: `-n/--tail <N>` (last N lines), `--head <N>` (first N lines), `-L/--lines <start:end>` (line range), `--all` (full output).
- **Global System Proxy & Direct Connection (`--proxy` / `--no-proxy`)**:
  - All `cw` commands default to following host system proxy rules (Windows registry `Internet Settings` priority, matching `ProxyOverride` bypass lists, and fallback to `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY`). Loopback addresses (`127.0.0.1`, `localhost`) are strictly protected from routing loops.
  - Use `--no-proxy` to force pure direct connection (recommended for high-bandwidth LAN transfers or dead Windows proxy registry recovery).
  - Use `--proxy <url>` (HTTP/HTTPS/SOCKS5) to explicitly tunnel commands to specific remote/bastion nodes.

## 2. CLI Reference

| Action | Command | Output / Notes |
| :--- | :--- | :--- |
| **Global Proxy Flags** | `cw [--proxy <url>] [--no-proxy] <cmd>` | All subcommands inherit proxy routing & direct override flags |
| **Cluster Health** | `cw nodes` | Status (`ONLINE`/`OFFLINE`), Version, OS, CPU%, Free/Total RAM, Job count |
| **Local Identity** | `cw show [--refresh]` | Displays machine name, IP addresses, service state, token |
| **Add Node** | `cw node add <name> [target] [--token <t>]` | Registers remote node to cluster (auto-infers `:19000`) |
| **Remove Node** | `cw node rm <name>` | Unregisters node from cluster |
| **Dispatch / Exec** | `cw run [-w] [-c] [-n <node>] "<cmd>"` | Default: async job. Use `-wc` for foreground sync execution, live streaming, exit code alignment & auto-cleanup |
| **List Jobs** | `cw ps [-n <node>] [--all] [--limit <n>]` | Lists jobs across cluster or node (`RUNNING` pinned to top, default 20 recent, `--all` shows all) |
| **Clean Jobs** | `cw clean [<node>:]<job_id> [-y]` / `cw clean <--days <n> \| --all> -y` | Cleans specific job or batches of finished jobs; deletes disk logs; protects RUNNING tasks |
| **Read Logs** | `cw logs [<node>:]<job_id> [-n <lines>] [--head <n>] [-L <range>] [--all]` | Reads logs (1MB auto-guard; supports tail, head, range & --all); supports direct node targeting |
| **Stream Logs** | `cw logs [<node>:]<job_id> [--node <node>] -f` | Live WebSocket streaming; supports direct node targeting |
| **Kill Job** | `cw kill [<node>:]<job_id> [-n <node>]` | Win32 Job Object tree termination; supports direct node targeting or cluster broadcast |
| **Copy File/Dir** | `cw cp [-r] [-j <n>] [<node>:]<src> [<node>:]<dest>` | Supports `local <-> remote`, `remote <-> remote`, `local <-> local` (auto mkdir, `-r` recursive, `-j` concurrency) |
| **Diff File/Dir** | `cw diff [-r] [--limit <n>] [--all] [<node>:]<src> [<node>:]<dest>` | Cross-node or local SHA-256 diff (omits matched; truncates large diffs >50; exit 0: match, 1: diff, 2: err) |
| **Read File** | `cw cat [<node>:]<path> [-n <t>] [--head <h>] [-L <r>] [--all]` | Prints remote or local text file (1MB auto-guard; supports tail, head, range & --all) |
| **List Dir** | `cw ls [<node>:]<path>` | Lists directory entries, mode, size, and mod time (remote or local) |
| **Make Dir** | `cw md [<node>:]<path>` | Recursive directory creation (`mkdir -p`) on remote or local |
| **Remove** | `cw rm -r -y [<node>:]<path>` | Non-interactive recursive deletion on remote or local |
| **Web Console** | `cw ui [--port <p>] [--no-open]` | Local Web console (127.0.0.1; foreground blocking server; embedded Svelte SPA) |
| **Service Control**| `cw service <start\|stop\|status>` | Manages background service (requires Admin) |
| **Update** | `cw update [-y] [--check] [--force] [--proxy <url>] [--no-proxy] [--mirror <url>]` | Self-update from GitHub (`crthu/cworker`); auto detects registry proxy; atomic rename-replace |
| **Version** | `cw -v` / `cw version` | Outputs version, build date, Go runtime |

## 3. Core Agent Workflows

### Pattern 1: Rapid Probing & Synchronous Execution (-wc)
```bash
# Execute immediate command synchronously with auto-cleanup (zero leftovers in cw ps)
cw run -n desktop-4090 -wc "nvidia-smi"
cw run -n desktop-4090 -wc --dir "D:/workspace" "git status"
```

### Pattern 2: Async Dispatch & Poll
```bash
# 1. Dispatch and extract job handle
cw run -n desktop-4090 --dir "D:/workspace" "python train.py"
# Output: [OK] Job job-1a2b3c4d dispatched to node 'desktop-4090' (PID: 12345)

# 2. Poll status until COMPLETED or FAILED (RUNNING tasks are always pinned to top)
cw ps

# 3. If FAILED, inspect trailing logs for root cause (direct node targeting recommended)
cw logs desktop-4090:job-1a2b3c4d -n 100
# Or inspect startup issues with --head:
# cw logs desktop-4090:job-1a2b3c4d --head 50
```

### Pattern 3: Remote File Staging, Pre-flight Diff & Cleanup
```bash
# 1. Prepare directory and upload input
cw md desktop-4090:D:/tmp/work
cw cp -r ./project_dir desktop-4090:D:/tmp/work

# 2. Pre-flight diff verification before sync (exit code: 0=identical, 1=different, 2=error)
cw diff -r ./project_dir desktop-4090:D:/tmp/work

# 3. Execute task & inspect results
cw run -n desktop-4090 --dir "D:/tmp/work" "python process.py"
cw cat desktop-4090:D:/tmp/work/result.json

# 4. Mandatory cleanup (always use -r -y)
cw rm -r -y desktop-4090:D:/tmp/work
```

### Pattern 4: Runaway Process Termination
```bash
# Inspect high CPU tasks and terminate whole Win32 process tree (direct node targeting recommended)
cw ps
cw kill desktop-4090:job-1a2b3c4d
```

### Pattern 5: Finished Jobs & Disk Log Retention Cleanup
```bash
# Clean a single finished job and remove its disk log
cw clean desktop-4090:job-1a2b3c4d -y

# Clean jobs older than 7 days across all nodes (always include -y to prevent CLI blocking)
cw clean --days 7 -y

# Or clean all finished jobs on a specific node
cw clean --all -n desktop-4090 -y
```

## 4. Path & Syntax Rules

- **Remote Path**: `<node>:<path>` (e.g. `DESKTOP-PC:D:/projects/foo` or `DESKTOP-PC:/d/projects/foo`).
- **Path Syntax**: Automatically normalizes `D:/dir`, `D:\dir`, and `/d/dir`.
- **Command Escaping**: Wrap command strings with double quotes; escape nested quotes with `\"`.
