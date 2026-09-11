# cworker AI Agent 集成与调用指南 (AGENTS.md)

> 本文档专为 AI Agent（如 Claude Code、OpenCode、Cursor、Codex、Antigravity 等）、自动化脚本与 LLM 工具链编写，旨在提供最高确定性、无歧义的 `cw` 工具调用规范。

---

## 一、给 Agent 的核心特性与边界认知

1. **非阻塞与长程常驻性**：
   * `cw run` 派发任务为**毫秒级异步动作**，命令下发成功后立即返回唯一的 `job-<hex>` ID 并退出；
   * Agent 无需在单个终端命令中挂起长程任务，派发后应通过 `cw ps` 轮询状态，或通过 `cw logs <job_id>` 查阅阶段输出。
2. **确定性的进程树清理保证（无孤儿进程风险）**：
   * 在 Windows 环境下，`cw kill <job_id>` 由操作系统内核级 **Win32 Job Object** 驱动；
   * Agent 无需担心子脚本再开的后台子进程泄漏，调用 `cw kill` 能够 100% 连根拔起整棵进程树。
3. **原生物理宿主环境（零沙盒）与高危破坏性操作二次确认**：
   * 目标 Worker 拥有宿主原生物理用户权限，**非 Docker/VM 容器，无沙盒虚拟隔离层**；
   * 任何在远端或本地节点执行的 `cw rm`、`cw cp`、`cw run` 均**直接作用于真实物理操作系统与磁盘文件系统**，破坏性不可逆；
   * **人机确认红线（Human Confirmation Gate）**：Agent 严禁在未经用户明确授权的情况下，擅自向任何节点下发高危破坏性操作（如递归删除非当前会话自创的临时目录、全盘/大范围覆盖关键工程目录、终止非自建未知任务等）。执行前必须向用户清晰提示：目标节点、物理路径/任务句柄与不可逆后果，获得显式确认后方可执行。
4. **高危删除防护规则（必须显式传参）**：
   * **Agent 调用 `cw rm` 删除目录时，必须强制附加 `-r`（递归）和 `-y`（自动确认）**；若缺少 `-y`，CLI 会在终端阻塞等待人机输入 `[y/N]`，导致 Agent 发生超时死锁！
   * 再次强调：因无沙盒保护，执行 `cw rm -r -y` 前必须核实路径正确性，非自建临时路径务必向用户二次确认。
5. **401 鉴权失效与凭证自愈**：
   * 若命令返回 401 Unauthorized，说明本地账本中记录的 Token 缺失或与目标 Worker 不匹配；
   * Agent 应指导在目标机运行 `cw show` 获取当前 Token，有两种自愈途径：
     * **方式 A（派发自动记忆，推荐）**：在派发任务时直接附加 `--token <token>`（如 `cw run -n <node> --token <token> ...`），握手成功（HTTP 200）后，客户端会自动将该有效 Token 固化至本地账本（`nodes.json`），后续调用无需再传；
     * **方式 B（账本显式更新）**：运行 `cw node add <node> <target> --token <token>` 显式更新账本。
6. **服务连通性诊断**：
   * 若目标节点报错连接拒绝，可通过 `cw service status` 查验服务状态，并通过管理员权限 `cw service start` 拉起后台服务。
7. **文件夹递归拷贝规范（必须显式附加 `-r`）**：
   * Agent 调用 `cw cp` 传输整个文件夹时，**必须显式附加 `-r`（递归）**；若缺少 `-r`，CLI 会主动拦截并提示 `omitting directory`；
   * 命令格式为 `cw cp [-r] [-j <n>] [<node>:]<src> [<node>:]<dest>`，支持全拓扑：`本地 <-> 远端`、`远端 <-> 远端`、`本地 <-> 本地`；
   * 默认并发连接数为 8，可通过 `-j <num>` 调节；内置端到端单遍流式 SHA-256 核验与非 TTY 纯净单行摘要，对 Agent 上下文完全透明无污染。
8. **跨机与本地文件强一致性比对（`cw diff`）与确定性退出码**：
   * 命令格式为 `cw diff [-r] [--limit <n>] [--all] [<node>:]<src> [<node>:]<dest>`；
   * 专为多机同步核验与 Pre-flight 检查设计，支持全拓扑：`本地 <-> 远端`、`远端 <-> 远端`、`本地 <-> 本地`；
   * 递归比对目录时**必须显式附加 `-r`**（若缺失直接报错并退出码 2）；
   * **默认排除全部 `[MATCH]` 匹配项**，终端仅输出 `[MODIFIED]`、`[ADDED]`、`[DELETED]` 的差异行，并在末尾单行汇总匹配数，零冗余噪音，避免污染 Agent 认知上下文；
   * **大差异自动截断与省略保护**：若差异条目过多，默认仅展示前 50 条并输出 `... and N more differing entries omitted (use --all to show all)`，可用 `--limit <n>` 调节或 `--all` 展开全部，末尾 Summary 始终保持全量真实统计；
   * **确定性退出码契约**：
     * `0`：两端完全一致（无变动）；
     * `1`：存在变动（有修改/新增/删除）；
     * `2`：异常错误（文件不存在、遗漏 `-r`、网络异常等）。
     Agent 可直接在自动化脚本中以单行退出码判定是否触发全量同步。

---

## 二、标准 Agent 任务调度动线（Best Practices）

### 1. 集群就绪探测
在派发重要任务前，Agent 应先执行 `cw nodes` 确认目标机器是否在线以及硬件负载（CPU% 与可用内存）：
```bash
cw nodes
```

### 2. 任务派发与凭证提取
```bash
cw run -n <node_name> [--token <token>] --name <readable_name> --dir "<working_dir>" "<command>"
```
**输出特征**：
```text
[OK] Job job-1a2b3c4d dispatched to node 'DESKTOP-4090' (PID: 12345)
Use 'cw logs job-1a2b3c4d -f' to stream live logs.
```
Agent 应通过正则提取出 `job-[a-f0-9]+` 作为后续任务生命周期的句柄（Handle）。

### 3. 任务状态轮询与监控
Agent 应周期性调用 `cw ps` 跟踪任务执行：
```bash
cw ps
```
**状态机约定**：
* `RUNNING`：任务正在执行，伴随实时的 `CPU%` 与 `MEM(MB)` 开销；
* `COMPLETED`：执行完毕，退出码为 0；
* `FAILED`：执行失败，退出码非 0；
* `STOPPED`：被主动通过 `cw kill` 终止。

### 4. 日志审计与故障诊断
若任务出现 `FAILED` 或 Agent 需要提取任务输出：
```bash
# 获取末尾 100 行日志
cw logs job-1a2b3c4d -n 100
```

### 5. 文件传输与远程清理
```bash
# 传单文件到远端指定绝对路径 (目标路径父目录不存在时会自动递归创建)
cw cp ./input_data.csv DESKTOP-4090:D:/workspace/data.csv

# 递归上传整个目录 (带 -r 递归，-j 指定并发连接数)
cw cp -r ./workspace DESKTOP-4090:D:/workspace

# 跨机器直接中继拷贝目录 (零中转磁盘开销，内存管道直灌)
cw cp -r DESKTOP-4090:D:/workspace/output TEST-BOX:D:/workspace/output

# 读取远端或本地配置文件 (文本直接打印，免去临时下载)
cw cat DESKTOP-4090:D:/workspace/metrics.json

# 跨机或本地差异核验 (单文件对比，返回 Size 与完整 SHA-256)
cw diff ./train.py DESKTOP-4090:D:/workspace/train.py

# 递归比对两端目录树 (Pre-flight 同步前核验，默认排除相同文件，退出码 0 表示一致，1 表示存在差异)
cw diff -r ./workspace DESKTOP-4090:D:/workspace

# 任务结束后清理远程工作临时目录 (强制附加 -r -y 避免交互死锁)
cw rm -r -y DESKTOP-4090:D:/workspace/temp/
```

---

## 三、路径与参数容错规范

* **Windows 盘符与 Git Bash 语法**：
  `cw` 在底层实现了强类型智能归一化，Agent 可以安全输入以下任意一种格式，系统会自动纠正为 Windows 物理路径：
  * `D:/projects/foo`
  * `D:\projects\foo`
  * `/d/projects/foo`
* **冒号隔离机制**：
  Agent 在构造 `node:path` 格式时，第一段冒号前必须是节点名（如 `DESKTOP-PC:D:/path`），系统能自动防范 Windows 盘符冒号误切。
