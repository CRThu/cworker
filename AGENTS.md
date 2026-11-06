# cworker AI Agent 集成与调用指南 (AGENTS.md)

> 本文档专为 AI Agent、自动化脚本与 LLM 工具链编写，提供最高确定性、无歧义的 `cw` 工具调用契约。

- **官方仓库**：[https://github.com/crthu/cworker](https://github.com/crthu/cworker)
- **权威部署**：`~/.cworker/bin/cw.exe`
- **自升级指令**：`cw update -y`（从 GitHub Releases 自动拉包原子替换升级）

---

## 一、给 Agent 的核心调用红线与契约

1. **宿主原生物理环境（零沙盒）与高危操作二次确认**：
   * 目标 Worker 拥有真实宿主用户权限，**无容器/沙盒隔离，任何删除与写入破坏性不可逆**；
   * **二次确认红线**：严禁未经用户授权递归删除非自建目录或强杀未知任务。执行高危操作前必须向用户提示：目标节点、物理路径/任务句柄与风险，获得显式确认后方可执行。

2. **环境隔离与显式 `--dir` 规范**：
   * Worker 默认以 Windows 系统服务（`SYSTEM` 权限）常驻，**仅加载系统环境变量（System PATH），不继承用户 PATH 与 `%USERPROFILE%`**；
   * 远程派发任务 **必须显式指定 `--dir "<working_dir>"`**（避免默认回退至 `System32` 导致找不到文件）；解释器优先使用绝对路径或在命令中显式激活环境。

3. **任务调度模式选择（即时探测 `-wc` vs 长程异步）**：
   * **即时探测（强制优先推荐 `-wc`）**：`cw run -n <node> -wc "<cmd>"`，前台同步阻塞流式直出，退出码严格对齐，执行完自动销毁远端临时任务与日志，零垃圾残留；
   * **长程异步任务（默认）**：`cw run` 毫秒级异步返回 `job-<hex>` Handle；通过 `cw ps` 轮询状态，通过 `cw logs <job_id>` 审计输出。

4. **参数隔离透传（`--`）与多 Shell 调度（`--shell`）**：
   * **参数透传（推荐使用 `--`）**：针对含空格、双引号或自带 flags（如 `-n`、`-c`、`--help`）的命令，**推荐使用 `--` 隔离透传**：`cw run [flags] -- <cmd> [args...]`。`--` 之后的参数保真直传，完全避免 CLI 参数冲突，内部引号无需反斜杠转义；
   * **多 Shell 调度（`-s, --shell`）**：
     * `cmd`（默认）：2ms 极速秒启，加装外层防破坏双引号包裹，完美兼容 `%PATH%`、`dir`、`echo`、`&&`；
     * `pwsh` / `powershell`：面向 PowerShell 专有 Cmdlet 与管道任务（如 `Get-Process | Select-Object`），内置终止错误拦截、UTF-8 编码与真实退出码捕获；
     * `bash`：面向 WSL / Git Bash / Linux 节点；
   * **本地管道转义规范**：凡命令中包含本地终端保留字符（如 `|`、`>`、`&&`），整条命令必须用双引号包裹传递（如 `cw run --shell pwsh "Get-Process | Select-Object -First 3"`），防止本地终端提前劫持管道。

5. **脚本自动化必须附加 `-y` 避免死锁**：
   * **删除目录**：`cw rm -r -y <path>`（必须带 `-r` 和 `-y`，缺 `-y` 会在终端阻塞等待交互 `[y/N]` 导致死锁超时）；
   * **任务历史清理**：`cw clean [<node>:]<job_id> -y` 或 `cw clean <--days <n> | --all> -y`（必须带 `-y`）。

6. **目录递归操作必须附加 `-r`**：
   * 传输目录必须加 `-r`：`cw cp -r <src> <dest>`；
   * 递归比对目录必须加 `-r`：`cw diff -r <src> <dest>`。

7. **确定性退出码契约（自动化判断依据）**：
   * `cw run -w`：退出码严格对齐远端进程退出码（非零即失败，Ctrl+C 中断返回 130 并连根强杀远端 Win32 Job Object 进程树）；
   * `cw diff`：`0` 两端完全一致；`1` 存在变动（有修改/新增/删除）；`2` 异常错误（文件缺失、网络异常等）。

8. **网络拓扑策略（集群纯直连，Update 智能系统代理）**：
   * 集群交互命令（`run`, `ps`, `cp`, `logs`, `diff` 等）**彻底强制直连（无代理）**，免疫 Tailscale CGNAT 劫持与系统死代理残留；若需穿透跳板机，显式传 `--proxy <url>`；
   * 仅公网自升级（`cw update`）默认遵循 Windows 注册表与环境变量代理。

9. **文本切片安全防线（`cw cat` 与 `cw logs`）**：
   * 内容 $\le$ 1MB 默认全量输出；> 1MB 自动截取末尾 100 行并提示 `--all`；
   * 切片参数：`-n, --tail <N>`（末尾 N 行）、`--head <N>`（开头 N 行）、`-L <start:end>`（指定区间）、`--all`（全量直出）。

10. **401 凭证自愈**：若报错 401 Unauthorized，在命令中追加 `--token <token>` 派发，握手成功后客户端会自动持久化记住该有效 Token。

---

## 二、标准 Agent 任务调度动线（Cheat Sheet）

### 1. 快速探测与短命令执行 (-wc 同步直出)
```bash
# 探测远端环境
cw run -n workstation -wc "nvidia-smi"
cw run -n workstation -wc --dir "D:/workspace" "git status"

# 复杂参数或自身带 flags：使用 -- 隔离透传 (免转义地狱)
cw run -n workstation -wc -- git commit -m "fix: resolve memory leak"
cw run -n workstation -wc -- python train.py -n 10 --input "C:\Program Files\data.csv"

# 调度 PowerShell 管道 (用双引号包裹防本地终端提前截断)
cw run -n workstation -wc --shell pwsh "Get-Process | Select-Object -First 3"
```

### 2. 长程作业派发与生命周期监控 (默认异步)
```bash
# 1. 异步派发，正则提取 job-<hex> 句柄
cw run -n workstation --name "train-job" --dir "D:/workspace" "python train.py"
# 返回: [OK] Job job-1a2b3c4d dispatched to node 'workstation' (PID: 12345)

# 2. 轮询状态 (RUNNING 活跃任务权威置顶，终态任务按启动时间倒序)
cw ps -n workstation

# 3. 日志审计与排查
cw logs workstation:job-1a2b3c4d --head 50   # 看启动初期崩溃根因
cw logs workstation:job-1a2b3c4d -n 100      # 看末尾最新进展
cw logs workstation:job-1a2b3c4d -f          # 实时跟随日志流

# 4. 强杀与清理现场 (Win32 Job Object 连根强杀)
cw kill workstation:job-1a2b3c4d
cw clean workstation:job-1a2b3c4d -y
```

### 3. 文件互传、核验与清理
```bash
# 上传目录 (必须加 -r，-j 指定并发)
cw cp -r ./project_dir workstation:D:/workspace

# 比对两端目录一致性 (根据退出码 0/1 判定是否需要同步)
cw diff -r ./project_dir workstation:D:/workspace

# 文本切片读取
cw cat -n 50 workstation:D:/workspace/output.log

# 递归删除远程临时目录 (强制加 -r -y 防死锁)
cw rm -r -y workstation:D:/workspace/temp/
```

---

## 三、工程开发与工具链红线（开发者与 Agent 必须遵守）

1. **前端统一使用 Bun（严禁 npm / yarn / pnpm）**：
   * 前端位于 `web/`，单一事实来源为 `web/bun.lock`；
   * 安装依赖：`bun install`；构建产物：`bun run build`；测试：`bun run test`；类型检查：`bun run check`。
2. **后端统一工具链**：Go 1.21+，全量测试：`go test ./...`，全量打包：`.\build.bat`。
3. **测试环境物理隔离**：涉及 CLI、Client 或配置读写的 Go 单测，**首行必须注入隔离**：
   ```go
   t.Setenv("USERPROFILE", t.TempDir())
   ```
