# cworker AI Agent 集成与调用指南 (AGENTS.md)

> 本文档专为 AI Agent（如 Claude Code、OpenCode、Cursor、Codex、Antigravity 等）、自动化脚本与 LLM 工具链编写，旨在提供最高确定性、无歧义的 `cw` 工具调用规范。

- **官方仓库**：[https://github.com/crthu/cworker](https://github.com/crthu/cworker)
- **权威部署**：`~/.cworker/bin/cw.exe`
- **自升级指令**：`cw update -y`（从 GitHub Releases 自动拉包原子替换升级）

---

## 一、给 Agent 的核心特性与边界认知

1. **非阻塞长程常驻 与 同步即时执行（-w / -wc）**：
   * **长程异步任务（默认）**：`cw run` 默认为**毫秒级异步动作**，命令下发成功后立即返回唯一的 `job-<hex>` ID 并退出；Agent 无需在单个终端命令中挂起长程任务，派发后通过 `cw ps` 轮询，通过 `cw logs <job_id>` 查阅阶段输出；
   * **即时探测与短命令执行（推荐 `-wc`）**：针对环境探测、`git status`、短脚本等即时调用，**必须优先使用 `cw run -wc "<command>"`**（即 `--wait --clean`）：
     * `-w, --wait`：前台同步阻塞执行，实时流式直出 stdout/stderr，进程退出码严格对齐远端 ExitCode；
     * `-c, --clean`：任务执行完毕后自动物理销毁远端日志目录与内存记录（合写即 `-wc`），零垃圾残留，不污染 `cw ps`；
     * **门禁规则**：`-c` 严格依赖 `-w`，严禁在纯异步模式下使用。
2. **确定性的进程树清理保证（无孤儿进程风险）**：
   * 在 Windows 环境下，`cw kill <job_id>` 由操作系统内核级 **Win32 Job Object** 驱动；
   * Agent 无需担心子脚本再开的后台子进程泄漏，调用 `cw kill` 能够 100% 连根拔起整棵进程树；在 `-w` 同步模式下若捕获中断信号同样联动强杀并清场。
3. **原生物理宿主环境（零沙盒）与高危破坏性操作二次确认**：
   * 目标 Worker 拥有宿主原生物理用户权限，**非 Docker/VM 容器，无沙盒虚拟隔离层**；
   * 任何在远端或本地节点执行的 `cw rm`、`cw cp`、`cw run` 均**直接作用于真实物理操作系统与磁盘文件系统**，破坏性不可逆；
   * **人机确认红线（Human Confirmation Gate）**：Agent 严禁在未经用户明确授权的情况下，擅自向任何节点下发高危破坏性操作（如递归删除非当前会话自创的临时目录、全盘/大范围覆盖关键工程目录、终止非自建未知任务等）。执行前必须向用户清晰提示：目标节点、物理路径/任务句柄与不可逆后果，获得显式确认后方可执行。
4. **系统服务环境隔离与绝对路径规范**：
   * **环境隔离**：Worker 默认以 Windows 系统服务（`SYSTEM` 权限）常驻，**仅加载系统环境变量（System PATH），不继承特定登录用户的 User PATH 与 `%USERPROFILE%`**；
   * **必传 `--dir`**：调用 `cw run` **必须显式传入 `--dir "<working_dir>"`**（避免默认回退至 `System32` 导致相对路径找不到文件）；
   * **路径全绝对化**：Python/Node 等解释器优先使用绝对路径（或在命令中 `call activate.bat` 显式激活）；文件传输与清理必须使用物理绝对路径，严禁使用相对路径或假定家目录。
5. **高危删除防护规则（必须显式传参）与长任务保活心跳**：
   * **Agent 调用 `cw rm` 删除目录时，必须强制附加 `-r`（递归）和 `-y`（自动确认）**；若缺少 `-y`，CLI 会在终端阻塞等待人机输入 `[y/N]`，导致 Agent 发生超时死锁！
   * **长耗时删除流式进度与保活心跳（>5s）**：面对数十万小文件的深层庞大目录（耗时 >5s），TTY 模式下单行原位实时刷新已删项数与速率（`\rDeleting '<target>'... X items removed (Y items/s, elapsed Xs)`）；非 TTY 管道下自动按 5 秒低频输出存活心跳（`[cworker] Deleting '<target>'... X items removed (elapsed Xs)...`），持续重置 Agent 空闲超时检测，杜绝误杀；短任务（$\le$3s）零中间输出直接完成；
   * 再次强调：因无沙盒保护，执行 `cw rm -r -y` 前必须核实路径正确性，非自建临时路径务必向用户二次确认。
6. **401 鉴权失效与凭证自愈**：
   * 若命令返回 401 Unauthorized，说明本地记录的 Token 缺失或与目标 Worker 不匹配；
   * Agent 应指导在目标机运行 `cw show` 获取当前 Token，有两种自愈途径：
     * **方式 A（派发自动记忆，推荐）**：在派发任务时直接附加 `--token <token>`（如 `cw run -n <node> --token <token> ...`），握手成功（HTTP 200）后，客户端会自动记住该有效 Token，后续调用无需再传；
     * **方式 B（显式更新节点）**：运行 `cw node add <node> <target> --token <token>` 显式更新节点配置。
7. **服务连通性诊断**：
   * 若目标节点报错连接拒绝，可通过 `cw service status` 查验服务状态，并通过管理员权限 `cw service start` 拉起后台服务。
8. **文件夹递归拷贝规范（必须显式附加 `-r`）**：
   * Agent 调用 `cw cp` 传输整个文件夹时，**必须显式附加 `-r`（递归）**；若缺少 `-r`，CLI 会主动拦截并提示 `omitting directory`；
   * 命令格式为 `cw cp [-r] [-j <n>] [<node>:]<src> [<node>:]<dest>`，支持全拓扑：`本地 <-> 远端`、`远端 <-> 远端`、`本地 <-> 本地`；
   * 默认并发连接数为 8，可通过 `-j <num>` 调节；内置端到端单遍流式 SHA-256 核验与非 TTY 纯净单行摘要，长任务（>5s）自动输出低频（每 5 秒）单行心跳进度（`[cworker] Transferred: xx% ...`），防止 Agent 空闲超时，对 Agent 上下文完全透明无污染。
9. **跨机与本地文件强一致性比对（`cw diff`）与确定性退出码**：
   * 命令格式为 `cw diff [-r] [--limit <n>] [--all] [<node>:]<src> [<node>:]<dest>`；
   * 专为多机同步核验与 Pre-flight 检查设计，支持全拓扑：`本地 <-> 远端`、`远端 <-> 远端`、`本地 <-> 本地`；
   * **两端异步并行哈希与 Agent 存活心跳**：比对时源端与目标端异步并发计算，耗时加速一倍；在非 TTY / Agent 自动化环境下，长任务（>5s）自动按 5 秒低频输出单行心跳（`[cworker] Hashed: xx% ...`），并在结束时输出单行哈希耗时摘要，既杜绝 Agent 工具调用超时，又避免 Token 膨胀；
   * 递归比对目录时**必须显式附加 `-r`**（若缺失直接报错并退出码 2）；
   * **默认排除全部 `[MATCH]` 匹配项**，终端仅输出 `[MODIFIED]`、`[ADDED]`、`[DELETED]` 的差异行，并在末尾单行汇总匹配数，零冗余噪音，避免污染 Agent 认知上下文；
   * **大差异自动截断与省略保护**：若差异条目过多，默认仅展示前 50 条并输出 `... and N more differing entries omitted (use --all to show all)`，可用 `--limit <n>` 调节或 `--all` 展开全部，末尾 Summary 始终保持全量真实统计；
   * **确定性退出码契约**：
     * `0`：两端完全一致（无变动）；
     * `1`：存在变动（有修改/新增/删除）；
     * `2`：异常错误（文件不存在、遗漏 `-r`、网络异常等）。
     Agent 可直接在自动化脚本中以单行退出码判定是否触发全量同步。
10. **任务历史与单任务/批量清理规范（`cw clean` 必须附加 `-y`）**：
   * **单任务定向清理**：`cw clean [<node>:]<job_id> -y`，精准清除指定的已终态任务及其磁盘日志目录（释放磁盘且移出任务列表）；
   * **批量清理**：未指定 job_id 时，**必须显式指定 `--days <n>` 或 `--all` 之一，且必须附加 `-y`（自动确认）**；若缺少 `-y`，CLI 会在终端阻塞等待输入 `[y/N]` 导致超时死锁；
   * 底层保证：正在处于 `RUNNING` 状态的任务绝对受保护，严禁被清理。
11. **本地 Web 控制台启动认知（`cw ui` 前台常驻阻塞警示）**：
    * `cw ui [--port <port>] [--no-open]` 为本地前台常驻 HTTP 服务（严格绑定 127.0.0.1，Go embed 内嵌 Svelte 5 SPA 前端产物）；
    * **Agent 严禁在非守护同步会话中直接阻塞执行 `cw ui`**，否则会导致终端挂起超时；当用户需要可视化界面时，Agent 应建议用户在独立终端直接运行 `cw ui`，或以 Daemon 后台模式启动。
12. **文本切片与 1MB 自动截断规则（`cw cat` 与 `cw logs`）**：
    * **默认截断**：未指定切片参数时，文件/日志 $\le$ 1MB 默认全量输出；> 1MB 自动截取末尾 100 行并提示 `--all`；
    * **切片参数**：
      * `-n, --tail <N>`：读取末尾 N 行（无文件大小限制）；
      * `--head <N>`：读取开头 N 行（早停断流）；
      * `-L, --lines <start:end>`：读取区间切片（如 `-L 100:200`）；
      * `--all`：输出完整内容。
13. **全局系统代理遵循与 `--proxy` / `--no-proxy` 调度规范**：
    * **默认缺省行为**：`cw` 所有子命令（无论是集群通信、文件传输还是自升级）默认遵循宿主系统代理与规则分流。系统优先读取 Windows 注册表 `Internet Settings`（`ProxyEnable` 与 `ProxyServer`），并自动校验 `ProxyOverride` 直连规则（`<local>`、局域网段、通配符等）；若注册表未开启，则平滑降级回退至标准环境变量（`HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY`）；环回地址（`localhost`、`127.0.0.1`、`::1`）默认享受强制物理直连保护，避免本地通信死锁；
    * **强制物理直连（`--no-proxy`）**：在局域网内进行 GB 级别海量文件传输（`cw cp`），或在 Windows 出现代理客户端异常退出导致注册表死代理残留时，**可显式附加 `--no-proxy`**（例如 `cw --no-proxy cp ...` 或 `cw update --no-proxy`），彻底断开所有代理隧道直连目标机器；
    * **定向代理穿透（`--proxy <url>`）**：若需跨公网或跳板机调度海外/隔离区 Worker，可通过全局持久标志指定特定代理（支持 HTTP/HTTPS/SOCKS5，例如 `cw --proxy socks5://127.0.0.1:10808 run ...`）。

---

## 二、标准 Agent 任务调度动线（Best Practices）

### 1. 集群就绪探测
在派发重要任务前，Agent 应先执行 `cw node` 确认目标机器是否在线以及硬件负载（CPU 算力与内存已用/总量）：
```bash
cw node
```

### 2. 即时探测与短命令快速执行 (推荐 -wc)
针对环境检查、查看版本、`git status`、小脚本等短生命周期命令，**直接使用 `-wc` 同步前台直出并自毁清理**（退出码严格对齐，零垃圾残留）：
```bash
# 在远端节点执行快速探测 (屏幕实时输出，退出码对齐，执行完自毁清理)
cw run -n DESKTOP-4090 -wc "nvidia-smi"
cw run -n DESKTOP-4090 -wc "git status"

# 亦可指定工作目录
cw run -n DESKTOP-4090 -wc --dir "D:/workspace" "python -V"
```

### 3. 长程作业派发与凭证提取 (默认异步)
针对模型训练、全量编译、长跑服务等长时间任务，使用默认异步派发：
```bash
cw run -n <node_name> [--token <token>] --name <readable_name> --dir "<working_dir>" "<command>"
```
**输出特征**：
```text
[OK] Job job-1a2b3c4d dispatched to node 'DESKTOP-4090' (PID: 12345)
Use 'cw logs job-1a2b3c4d -f' to stream live logs.
```
Agent 应通过正则提取出 `job-[a-f0-9]+` 作为后续任务生命周期的句柄（Handle）。

### 4. 任务状态轮询与监控
Agent 应周期性调用 `cw ps` 跟踪任务执行（支持 `-n <node>` 定向节点加速查询）：
```bash
# 全集群轮询
cw ps

# 或仅轮询目标节点 (定向加速，网络开销最小)
cw ps -n <node_name>
```
**状态机与权威时序约定**：
* **确定性时序保证**：`cw ps` 与底层任务 API 严格遵循服务端权威排序——**`RUNNING` 活跃状态任务置顶优先呈现，其余终态任务严格按启动时间 `StartTime` 倒序排布（新任务在前，老任务在后）**。Agent 可直接从顶部获取当前活跃作业，或通过首条非 RUNNING 记录快速锁定最近一次执行结果；
* `RUNNING`：任务正在执行，伴随实时的 `CPU%` 与 `MEM(MB)` 开销；
* `COMPLETED`：执行完毕，退出码为 0；
* `FAILED`：执行失败，退出码非 0；
* `STOPPED`：被主动通过 `cw kill` 终止。

### 5. 日志审计与精准切片排查
```bash
# 查看启动初期前 50 行日志 (抓启动初期 ImportError / CUDA 初始化崩溃根因)
cw logs DESKTOP-4090:job-1a2b3c4d --head 50

# 查看末尾 100 行最新进展 (1MB 内短任务日志自动全量直出，超限保底末尾 100 行)
cw logs DESKTOP-4090:job-1a2b3c4d -n 100

# 查看指定行号区间
cw logs DESKTOP-4090:job-1a2b3c4d -L 120:160

# 强制查看全量完整日志 (流式直出)
cw logs DESKTOP-4090:job-1a2b3c4d --all

# 终止任务 (Win32 Job Object 连根查杀进程树)
cw kill DESKTOP-4090:job-1a2b3c4d

# 精准清理单个已终态任务 (释放磁盘 output.log 并移出账本)
cw clean DESKTOP-4090:job-1a2b3c4d -y
```

### 6. 文件传输与远程清理
```bash
# 传单文件到远端指定绝对路径 (目标路径父目录不存在时会自动递归创建)
cw cp ./input_data.csv DESKTOP-4090:D:/workspace/data.csv

# 递归上传整个目录 (带 -r 递归，-j 指定并发连接数)
cw cp -r ./workspace DESKTOP-4090:D:/workspace

# 跨机器直接中继拷贝目录 (零中转磁盘开销，内存管道直灌)
cw cp -r DESKTOP-4090:D:/workspace/output TEST-BOX:D:/workspace/output

# 读取配置文件 (1MB 内全量秒开；超 1MB 自动安全保底末尾 100 行防爆屏)
cw cat DESKTOP-4090:D:/workspace/metrics.json

# 文本精准切片 (看表头/看尾部/看区间)
cw cat --head 20 DESKTOP-4090:D:/workspace/data.csv
cw cat -n 50 DESKTOP-4090:D:/workspace/training.log
cw cat -L 100:150 DESKTOP-4090:D:/workspace/main.py

# 跨机或本地差异核验 (单文件对比，返回 Size 与完整 SHA-256)
cw diff ./train.py DESKTOP-4090:D:/workspace/train.py

# 递归比对两端目录树 (Pre-flight 同步前核验，默认排除相同文件，退出码 0 表示一致，1 表示存在差异)
cw diff -r ./workspace DESKTOP-4090:D:/workspace

# 任务结束后清理远程工作临时目录 (强制附加 -r -y 避免交互死锁)
cw rm -r -y DESKTOP-4090:D:/workspace/temp/
```

### 7. 本地 Web 控制台启动
```bash
# 启动嵌入式 Web 控制台 (推荐用户在独立终端运行，默认自动打开 http://127.0.0.1:19001)
cw ui

# 亦可指定端口或禁用自动唤起浏览器
cw ui --port 19001 --no-open
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

---

## 四、工程开发与工具链规范（开发者与 Agent 红线）

1. **前端工具链统一使用 Bun（严禁 npm / yarn / pnpm）**：
   * 前端位于 `web/` 目录，依赖管理单一事实来源为 `web/bun.lock`；
   * **依赖安装**：在 `web/` 目录下执行 `bun install`；
   * **产物编译**：执行 `bun run build`（产物输出至 `pkg/ui/dist/`，供 Go embed 内嵌打包）；
   * **单元测试**：执行 `bun run test`（调用 Vitest + JSDOM 进行组件与逻辑全量测试）；
   * **类型与模板校验**：执行 `bun run check`（执行 svelte-check 与 tsc）；
   * **红线**：严禁在前端目录执行 `npm install`、`npm test` 或生成 `package-lock.json`！

2. **后端工具链规范**：
   * Go 1.21+ 官方工具链；
   * **全量测试**：`go test ./...`；
   * **独立编译**：`go build -ldflags="-s -w" -o .\bin\cw.exe .`。

3. **全链路统一构建入口**：
   * 根目录下维护的 `.\build.bat`：自动按序触发 Bun 前端编译、Go 产物链接与系统打包；
   * 测试运行：`.\build.bat test`。

4. **测试环境物理隔离（严禁污染真实宿主配置）**：
   * 涉及 CLI 命令、Client 实例或配置文件读写的 Go 测试，**首行必须强制注入环境隔离**，严防单测写穿至宿主目录（`%USERPROFILE%\.cworker`）：
     ```go
     t.Setenv("USERPROFILE", t.TempDir())
     ```
   * 直接构造 `client.Client` 单测时，显式重定向 `cli.dataDir = t.TempDir()`。



