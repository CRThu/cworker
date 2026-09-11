---
name: cworker
description: >-
  Orchestrate and manage distributed Windows processes, jobs, and files across remote nodes
  using cworker (cw). Covers cluster inspection, async job execution, log retrieval,
  JobObject process killing, remote file operations, and known-nodes ledger management.
---

# cworker (cw) Agent Guide

## 1. Safety & Execution Rules

- **Native Host OS (Zero Sandbox) & Confirmation Gate**:
  - Workers execute with native host user privileges directly on the physical OS. **There is NO Docker container, VM, or sandbox isolation**.
  - All operations (`cw rm`, `cw cp`, `cw run`, `cw kill`) directly affect the target machine's physical filesystem and processes with irreversible consequences.
  - **Human Confirmation Gate**: Agents MUST NOT execute destructive operations (such as recursively deleting non-temporary/non-self-created directories via `cw rm -r -y`, overwriting key project directories via `cw cp -r`, or terminating unknown jobs via `cw kill`) without first explicitly notifying the user of the target node, physical path, and irreversible risks, and obtaining explicit confirmation.
- **`cw rm` MUST include `-r -y`**: Non-interactive recursive deletion requires both flags. Omitting `-y` causes the CLI to block on interactive `[y/N]` confirmation, deadlocking the agent.
  ```bash
  cw rm -r -y [<node>:]<path>
  ```
- **`cw cp` on Directories MUST include `-r`**: Copying directories requires `-r` (or `--recursive`). Concurrency can be tuned with `-j <num>` (default 8). Transfers include single-pass streaming SHA-256 integrity validation and clean non-TTY summary output.
- **`cw diff` on Directories MUST include `-r`**: Directory comparison requires `-r`. Diffs are based on SHA-256 and size. Identical files are omitted by default to keep context clean. Returns exit code 0 if identical, 1 if differences exist, 2 on error.
- **Unified Local & Remote Support**: All file commands (`cp`, `diff`, `cat`, `ls`, `md`, `rm`) natively support local paths (`[<node>:]<path>`). You can operate on local files, local directories, or cross-node seamlessly without switching tools.
- **Async Execution**: `cw run` returns immediately with a `job-<hex>` handle. Do not block; poll status with `cw ps` or inspect output with `cw logs <job_id>`.
- **Process Tree Cleanup**: `cw kill <job_id>` uses Win32 Job Objects to terminate the entire process hierarchy without leaving orphan processes.
- **`cw clean` MUST include `-y`**: Cleaning finished job records and logs requires `-y` for non-interactive execution. You must also specify either `--days <n>` or `--all`. Active `RUNNING` jobs are strictly protected from deletion.
- **401 Unauthorized Recovery**: If a command returns 401, the target node's token changed or is missing. Fetch the token via `cw show` on that node. You can either update the ledger via `cw node add <node> <target> --token <token>`, or dispatch directly with `cw run -n <node> --token <token> ...` (the client will automatically persist valid tokens to the local ledger upon successful handshake).

## 2. CLI Reference

| Action | Command | Output / Notes |
| :--- | :--- | :--- |
| **Cluster Health** | `cw nodes` | Status (`ONLINE`/`OFFLINE`), CPU%, Free/Total RAM, Job count |
| **Local Identity** | `cw show [--refresh]` | Displays machine name, IP addresses, service state, token |
| **Add Node** | `cw node add <name> [target] [--token <t>]` | Registers remote node to known ledger (auto-infers `:19000`) |
| **Remove Node** | `cw node rm <name>` | Unregisters node from local ledger |
| **Dispatch Job** | `cw run -n <node> [--token <t>] [--dir <dir>] [--name <n>] "<cmd>"` | Dispatches async command; returns `job-<hex>` (PID: `12345`). Valid tokens automatically saved to ledger on success |
| **List Jobs** | `cw ps [-n <node>] [--all] [--limit <n>]` | Lists jobs across cluster or node (`RUNNING` pinned to top, default 20 recent, `--all` shows all) |
| **Clean Jobs** | `cw clean <--days <n> \| --all> [-n <node>] [-y]` | Cleans completed/failed/stopped jobs and removes disk logs; protects RUNNING tasks |
| **Read Logs** | `cw logs <job_id> [-n <lines>]` | Reads trailing lines (default 100) |
| **Stream Logs** | `cw logs <job_id> -f` | Live WebSocket streaming |
| **Kill Job** | `cw kill <job_id>` | Win32 Job Object tree termination |
| **Copy File/Dir** | `cw cp [-r] [-j <n>] [<node>:]<src> [<node>:]<dest>` | Supports `local <-> remote`, `remote <-> remote`, `local <-> local` (auto mkdir, `-r` recursive, `-j` concurrency) |
| **Diff File/Dir** | `cw diff [-r] [--limit <n>] [--all] [<node>:]<src> [<node>:]<dest>` | Cross-node or local SHA-256 diff (omits matched; truncates large diffs >50; exit 0: match, 1: diff, 2: err) |
| **Read File** | `cw cat [<node>:]<path>` | Prints remote or local text file directly to stdout |
| **List Dir** | `cw ls [<node>:]<path>` | Lists directory entries, mode, size, and mod time (remote or local) |
| **Make Dir** | `cw md [<node>:]<path>` | Recursive directory creation (`mkdir -p`) on remote or local |
| **Remove** | `cw rm -r -y [<node>:]<path>` | Non-interactive recursive deletion on remote or local |
| **Service Control**| `cw service <start\|stop\|status>` | Manages background service (requires Admin) |
| **Update** | `cw update [-y] [--check] [--force] [--proxy <url>] [--mirror <url>]` | Manual self-update from GitHub (`crthu/cworker`); auto detects registry proxy; aborts if jobs running |
| **Version** | `cw -v` / `cw version` | Outputs version, build date, Go runtime |

## 3. Core Agent Workflows

### Pattern 1: Async Dispatch & Poll
```bash
# 1. Dispatch and extract job handle
cw run -n desktop-4090 --dir "D:/workspace" "python train.py"
# Output: [OK] Job job-1a2b3c4d dispatched to node 'desktop-4090' (PID: 12345)

# 2. Poll status until COMPLETED or FAILED
cw ps

# 3. If FAILED, inspect trailing logs for root cause
cw logs job-1a2b3c4d -n 100
```

### Pattern 2: Remote File Staging & Cleanup
```bash
# 1. Prepare directory and upload input (single file or recursive directory)
cw md desktop-4090:D:/tmp/work
cw cp ./input.csv desktop-4090:D:/tmp/work/input.csv
# Or upload entire local project directory recursively:
cw cp -r ./project_dir desktop-4090:D:/tmp/work

# 2. Execute task & inspect results
cw run -n desktop-4090 --dir "D:/tmp/work" "python process.py"
cw cat desktop-4090:D:/tmp/work/result.json

# 3. Mandatory cleanup (always use -r -y)
cw rm -r -y desktop-4090:D:/tmp/work
```

### Pattern 3: Runaway Process Termination
```bash
# Inspect high CPU tasks and terminate whole process tree
cw ps
cw kill job-1a2b3c4d
```

### Pattern 4: Pre-flight Diff Verification & Selective Sync
```bash
# 1. Compare directory trees before sync (exit code: 0=identical, 1=different, 2=error)
cw diff -r ./src desktop-4090:D:/workspace/src

# 2. If differences exist, sync remote directory
cw cp -r ./src desktop-4090:D:/workspace/src
```

### Pattern 5: Finished Jobs & Disk Log Retention Cleanup
```bash
# Clean jobs older than 7 days across all nodes (always include -y to prevent CLI blocking)
cw clean --days 7 -y

# Or clean all finished jobs on a specific node
cw clean --all -n desktop-4090 -y
```

## 4. Path & Syntax Rules

- **Remote Path**: `<node>:<path>` (e.g. `DESKTOP-PC:D:/projects/foo` or `DESKTOP-PC:/d/projects/foo`).
- **Path Syntax**: Automatically normalizes `D:/dir`, `D:\dir`, and `/d/dir`.
- **Command Escaping**: Wrap command strings with double quotes; escape nested quotes with `\"`.
