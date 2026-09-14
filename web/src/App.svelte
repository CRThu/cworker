<!-- web/src/App.svelte - cworker 现代化控制台单页应用主入口 -->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { Server, Zap, Folder, Key, RefreshCw, Check, X } from 'lucide-svelte';
  import ThemeToggle from './lib/components/ThemeToggle.svelte';
  import NodesView from './lib/components/NodesView.svelte';
  import JobsView from './lib/components/JobsView.svelte';
  import DualPaneFiles from './lib/components/DualPaneFiles.svelte';
  import LogTerminal from './lib/components/LogTerminal.svelte';
  import KillConfirmModal from './lib/components/KillConfirmModal.svelte';
  import RunJobModal from './lib/components/RunJobModal.svelte';
  import CleanJobsModal from './lib/components/CleanJobsModal.svelte';
  import AddNodeModal from './lib/components/AddNodeModal.svelte';
  import MyCardModal from './lib/components/MyCardModal.svelte';

  import { initTheme } from './lib/stores/theme';
  import {
    fetchOverview,
    fetchNodes,
    fetchJobs,
    addNode,
    removeNode,
    runJob,
    killJob,
    cleanJobs,
  } from './lib/api';
  import type { OverviewData, NodeInfo, KnownNode, JobInfo } from './lib/types';

  let currentTab: 'nodes' | 'jobs' | 'files' = 'nodes';

  // 全局数据状态
  let overview: OverviewData | null = null;
  let nodes: NodeInfo[] = [];
  let knownNodes: KnownNode[] = [];
  let jobs: JobInfo[] = [];

  // CPU 与内存历史走势记录 (用于 Sparkline 走势图，保留最近 25 个点)
  let cpuHistoryMap: Record<string, number[]> = {};
  let memHistoryMap: Record<string, number[]> = {};

  // 轮询控制 (默认 1 秒自动刷新)
  let isAutoRefresh = true;
  let refreshTimer: any = null;

  // 后端服务连接健康状态
  let isBackendConnected: boolean = true;
  let backendErrorMsg: string | null = null;

  // 模态框状态
  let isAddNodeOpen = false;
  let isRunJobOpen = false;
  let isCleanJobsOpen = false;
  let isMyCardOpen = false;
  let killJobTarget: JobInfo | null = null;

  // 实时日志抽屉状态
  let terminalJobId = '';
  let terminalJobNode = '';
  let terminalJobStatus = '';
  let isTerminalOpen = false;

  // Toast 消息队列
  interface Toast {
    id: number;
    text: string;
    type: 'success' | 'error';
  }
  let toasts: Toast[] = [];
  let toastIdSeq = 0;

  function showToast(text: string, type: 'success' | 'error' = 'success') {
    const id = ++toastIdSeq;
    toasts = [...toasts, { id, text, type }];
    setTimeout(() => {
      toasts = toasts.filter(t => t.id !== id);
    }, 3200);
  }

  // 数据刷新逻辑
  async function refreshData() {
    try {
      const [ovData, nodesData, jobsData] = await Promise.all([
        fetchOverview(),
        fetchNodes(),
        fetchJobs(),
      ]);

      overview = ovData;
      nodes = nodesData.nodes || [];
      knownNodes = nodesData.known || [];
      jobs = jobsData || [];
      isBackendConnected = true;
      backendErrorMsg = null;

      // 更新历史走势点队列
      nodes.forEach(n => {
        const cpu = n.status === 'ONLINE' && n.metrics ? n.metrics.cpu_percent : 0;
        const memPercent = n.status === 'ONLINE' && n.metrics && n.metrics.mem_total_mb > 0
          ? ((n.metrics.mem_total_mb - n.metrics.mem_free_mb) / n.metrics.mem_total_mb) * 100
          : 0;

        const currentCpuHist = cpuHistoryMap[n.name] || [];
        const currentMemHist = memHistoryMap[n.name] || [];

        cpuHistoryMap[n.name] = [...currentCpuHist, cpu].slice(-25);
        memHistoryMap[n.name] = [...currentMemHist, memPercent].slice(-25);
      });
      // 触发 Svelte 响应式更新
      cpuHistoryMap = { ...cpuHistoryMap };
      memHistoryMap = { ...memHistoryMap };
    } catch (e: any) {
      isBackendConnected = false;
      backendErrorMsg = e.message || '控制台服务通信中断';
    }
  }

  function startRefreshTimer() {
    stopRefreshTimer();
    refreshData();
    refreshTimer = setInterval(() => {
      if (isAutoRefresh) {
        refreshData();
      }
    }, 1000);
  }

  function stopRefreshTimer() {
    if (refreshTimer) {
      clearInterval(refreshTimer);
      refreshTimer = null;
    }
  }

  function toggleAutoRefresh() {
    isAutoRefresh = !isAutoRefresh;
    if (isAutoRefresh) {
      refreshData();
    }
  }

  // 节点操作
  async function handleAddNode(event: CustomEvent<any>) {
    try {
      await addNode(event.detail);
      showToast(`已成功添加并探活节点 '${event.detail.name}'`);
      isAddNodeOpen = false;
      refreshData();
    } catch (e: any) {
      showToast(`添加节点失败: ${e.message}`, 'error');
    }
  }

  async function handleRemoveNode(event: CustomEvent<string>) {
    const name = event.detail;
    if (!confirm(`确定要从已知节点账本中移除 '${name}' 吗？`)) return;
    try {
      await removeNode(name);
      showToast(`已移除节点 '${name}'`);
      refreshData();
    } catch (e: any) {
      showToast(`移除失败: ${e.message}`, 'error');
    }
  }

  // 任务派发与终止
  async function handleRunJob(event: CustomEvent<any>) {
    try {
      const info = await runJob(event.detail);
      showToast(`任务已派发: ${info.id} (PID: ${info.pid})`);
      isRunJobOpen = false;
      currentTab = 'jobs';
      refreshData();
    } catch (e: any) {
      showToast(`派发失败: ${e.message}`, 'error');
    }
  }

  function handleOpenKill(event: CustomEvent<JobInfo>) {
    killJobTarget = event.detail;
  }

  async function handleConfirmKill(event: CustomEvent<string>) {
    const jobId = event.detail;
    try {
      await killJob(jobId);
      showToast(`任务 ${jobId} 已终止，子进程树彻底清理`);
      killJobTarget = null;
      refreshData();
    } catch (e: any) {
      showToast(`终止任务失败: ${e.message}`, 'error');
    }
  }

  async function handleCleanJobs(event: CustomEvent<any>) {
    try {
      const data = await cleanJobs(event.detail);
      let count = 0;
      if (data && typeof data === 'object') {
        Object.values(data).forEach((r: any) => {
          count += r.cleaned_count || 0;
        });
      }
      showToast(`清理成功：清理了 ${count} 个历史任务`);
      isCleanJobsOpen = false;
      refreshData();
    } catch (e: any) {
      showToast(`清理失败: ${e.message}`, 'error');
    }
  }

  function handleViewLogs(event: CustomEvent<JobInfo>) {
    terminalJobId = event.detail.id;
    terminalJobNode = event.detail.node;
    terminalJobStatus = event.detail.status;
    isTerminalOpen = true;
  }

  onMount(() => {
    initTheme();
    startRefreshTimer();
  });

  onDestroy(() => {
    stopRefreshTimer();
  });
</script>

<div class="app-layout">
  <!-- 左侧固定侧边栏 -->
  <aside class="sidebar">
    <div class="sidebar-header">
      <span class="logo-badge">🥕</span>
      <div class="brand-title">
        cworker <span class="console-tag">v{overview?.version || '1.2.0'}</span>
      </div>
    </div>

    <nav class="sidebar-nav">
      <button
        type="button"
        class="nav-item {currentTab === 'nodes' ? 'active' : ''}"
        on:click={() => currentTab = 'nodes'}
      >
        <Server size={16} class="nav-icon" />
        <span class="nav-text">节点</span>
        <span class="nav-badge">{nodes.length}</span>
      </button>

      <button
        type="button"
        class="nav-item {currentTab === 'jobs' ? 'active' : ''}"
        on:click={() => currentTab = 'jobs'}
      >
        <Zap size={16} class="nav-icon" />
        <span class="nav-text">任务</span>
        <span class="nav-badge">{overview?.active_jobs || 0}</span>
      </button>

      <button
        type="button"
        class="nav-item {currentTab === 'files' ? 'active' : ''}"
        on:click={() => currentTab = 'files'}
      >
        <Folder size={16} class="nav-icon" />
        <span class="nav-text">文件</span>
      </button>
    </nav>

    <div class="sidebar-footer">
      <div class="cluster-status-pill">
        <span class="status-indicator">
          <span class="status-dot {isBackendConnected ? ((overview?.online_count || 0) > 0 ? 'online' : 'idle') : 'offline'}"></span>
          <span>{isBackendConnected ? `${overview?.online_count || 0} 在线` : '服务离线'}</span>
        </span>
        <span class="divider">|</span>
        <span>{isBackendConnected ? `${overview?.active_jobs || 0} 运行中` : '断开'}</span>
      </div>

      <button class="btn btn-secondary btn-sidebar" type="button" on:click={() => isMyCardOpen = true}>
        <Key size={14} />
        <span>本机名片</span>
      </button>
    </div>
  </aside>

  <!-- 右侧主工作区 -->
  <main class="main-workspace">
    <!-- 顶部导航工作区状态条 -->
    <header class="top-bar">
      <div class="view-title">
        {#if currentTab === 'nodes'}
          <Server size={18} />
          <span>节点</span>
        {:else if currentTab === 'jobs'}
          <Zap size={18} />
          <span>任务</span>
        {:else}
          <Folder size={18} />
          <span>文件互传</span>
        {/if}
      </div>

      <div class="top-actions">
        <!-- 自动刷新与后台健康状态脉冲指示灯 (极简绿点) -->
        <button
          type="button"
          class="refresh-indicator {!isBackendConnected ? 'disconnected' : (isAutoRefresh ? 'active' : 'paused')}"
          title="{!isBackendConnected ? `服务通信中断: ${backendErrorMsg} (点击重试)` : (isAutoRefresh ? '自动同步中 (点击暂停)' : '已暂停自动同步 (点击恢复)')}"
          aria-label="{!isBackendConnected ? '服务离线' : (isAutoRefresh ? '自动同步中' : '已暂停')}"
          on:click={() => {
            if (!isBackendConnected) {
              refreshData();
            } else {
              toggleAutoRefresh();
            }
          }}
        >
          <span class="pulse-dot"></span>
        </button>

        <!-- 黑白主题切换 -->
        <ThemeToggle />
      </div>
    </header>

    <!-- 后端断连警报栏 -->
    {#if !isBackendConnected}
      <div class="backend-offline-banner">
        <span class="banner-dot"></span>
        <span>控制台服务通信中断：{backendErrorMsg || '无法连接到后端'}</span>
        <button type="button" class="btn-banner-retry" on:click={refreshData}>立即重试</button>
      </div>
    {/if}

    <!-- 工作区主内容区 -->
    <div class="content-container">
      {#if currentTab === 'nodes'}
        <NodesView
          {nodes}
          {knownNodes}
          {overview}
          {cpuHistoryMap}
          {memHistoryMap}
          on:openAddNode={() => isAddNodeOpen = true}
          on:removeNode={handleRemoveNode}
        />
      {:else if currentTab === 'jobs'}
        <JobsView
          {jobs}
          {nodes}
          on:openRun={() => isRunJobOpen = true}
          on:openClean={() => isCleanJobsOpen = true}
          on:viewLogs={handleViewLogs}
          on:killJob={handleOpenKill}
        />
      {:else}
        <DualPaneFiles {nodes} />
      {/if}
    </div>
  </main>
</div>

<!-- 模态弹窗挂载 -->
<AddNodeModal
  open={isAddNodeOpen}
  on:close={() => isAddNodeOpen = false}
  on:submit={handleAddNode}
/>

<RunJobModal
  open={isRunJobOpen}
  {nodes}
  on:close={() => isRunJobOpen = false}
  on:submit={handleRunJob}
/>

<CleanJobsModal
  open={isCleanJobsOpen}
  {nodes}
  on:close={() => isCleanJobsOpen = false}
  on:submit={handleCleanJobs}
/>

<KillConfirmModal
  open={!!killJobTarget}
  job={killJobTarget}
  on:close={() => killJobTarget = null}
  on:confirm={handleConfirmKill}
/>

<MyCardModal
  open={isMyCardOpen}
  card={overview?.local_card || null}
  on:close={() => isMyCardOpen = false}
/>

<LogTerminal
  open={isTerminalOpen}
  jobId={terminalJobId}
  node={terminalJobNode}
  status={terminalJobStatus}
  on:close={() => isTerminalOpen = false}
/>

<!-- Toast 容器 -->
<div class="toast-container">
  {#each toasts as t (t.id)}
    <div class="toast {t.type}">
      {#if t.type === 'success'}
        <Check size={14} />
      {:else}
        <X size={14} />
      {/if}
      <span>{t.text}</span>
    </div>
  {/each}
</div>

<style>
  .app-layout {
    display: flex;
    height: 100vh;
    max-height: 100vh;
    width: 100vw;
    overflow: hidden;
    background-color: var(--bg-base);
  }
  .sidebar {
    width: 176px;
    background: var(--bg-surface);
    border-right: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    flex-shrink: 0;
  }
  .sidebar-header {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 12px 14px;
    border-bottom: 1px solid var(--border-subtle);
  }
  .logo-badge {
    font-size: 20px;
  }
  .brand-title {
    font-size: 14px;
    font-weight: 700;
    color: var(--text-main);
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .console-tag {
    font-size: 10px;
    background: var(--primary-subtle);
    color: var(--primary);
    padding: 1px 5px;
    border-radius: 4px;
    font-weight: 600;
  }
  .sidebar-nav {
    display: flex;
    flex-direction: column;
    gap: 3px;
    padding: 10px 8px;
    flex: 1;
  }
  .nav-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 7px 10px;
    border-radius: 6px;
    border: none;
    background: transparent;
    color: var(--text-muted);
    font-size: 13px;
    font-weight: 500;
    cursor: pointer;
    transition: all 0.15s ease;
    text-align: left;
    width: 100%;
  }
  .nav-item:hover {
    background: var(--bg-hover);
    color: var(--text-main);
  }
  .nav-item.active {
    background: var(--primary-subtle);
    color: var(--primary);
    font-weight: 600;
  }
  .nav-icon {
    font-size: 15px;
  }
  .nav-text {
    flex: 1;
  }
  .nav-badge {
    font-size: 11px;
    background: var(--bg-elevated);
    padding: 1px 6px;
    border-radius: 9999px;
    color: var(--text-dim);
  }
  .nav-item.active .nav-badge {
    background: var(--primary);
    color: #ffffff;
  }
  .sidebar-footer {
    padding: 8px;
    border-top: 1px solid var(--border-subtle);
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .cluster-status-pill {
    display: flex;
    align-items: center;
    justify-content: space-between;
    background: var(--bg-elevated);
    border-radius: 9999px;
    padding: 4px 8px;
    font-size: 10px;
    color: var(--text-muted);
  }
  .btn-sidebar {
    padding: 6px 8px;
    font-size: 12px;
    justify-content: center;
  }
  .status-indicator {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .status-dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    transition: all 0.3s ease;
  }
  .status-dot.online {
    background: var(--success);
    box-shadow: 0 0 6px var(--success);
  }
  .status-dot.idle {
    background: var(--text-dim);
  }
  .status-dot.offline {
    background: var(--danger);
    box-shadow: 0 0 6px var(--danger);
    animation: pulse 1s infinite ease-in-out;
  }
  .divider {
    color: var(--border);
  }
  .btn-sidebar {
    width: 100%;
    font-size: 12px;
    padding: 6px 12px;
  }
  .main-workspace {
    flex: 1;
    height: 100%;
    display: flex;
    flex-direction: column;
    min-width: 0;
    min-height: 0;
    overflow: hidden;
  }
  .top-bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 12px 24px;
    background: var(--bg-surface);
    border-bottom: 1px solid var(--border);
    position: sticky;
    top: 0;
    z-index: 40;
  }
  .view-title {
    font-size: 16px;
    font-weight: 700;
    color: var(--text-main);
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .top-actions {
    display: flex;
    align-items: center;
    gap: 12px;
  }
  .refresh-indicator {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 24px;
    height: 24px;
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 50%;
    padding: 0;
    cursor: pointer;
    transition: all 0.15s ease;
  }
  .refresh-indicator:hover {
    border-color: var(--text-dim);
    background: var(--bg-hover);
  }
  .refresh-indicator.active .pulse-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background-color: var(--success);
    box-shadow: 0 0 6px var(--success);
    animation: pulse 1.5s infinite ease-in-out;
  }
  .refresh-indicator.paused .pulse-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background-color: var(--text-dim);
  }
  .refresh-indicator.disconnected {
    border-color: var(--danger);
    background: var(--danger-subtle);
  }
  .refresh-indicator.disconnected:hover {
    border-color: var(--danger);
  }
  .refresh-indicator.disconnected .pulse-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background-color: var(--danger);
    box-shadow: 0 0 6px var(--danger);
    animation: pulse 1s infinite ease-in-out;
  }
  .backend-offline-banner {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 10px;
    background: var(--danger-subtle);
    border-bottom: 1px solid var(--danger);
    color: var(--danger);
    padding: 8px 16px;
    font-size: 12px;
    font-weight: 500;
  }
  .banner-dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--danger);
    box-shadow: 0 0 6px var(--danger);
    animation: pulse 1s infinite ease-in-out;
  }
  .btn-banner-retry {
    background: var(--bg-surface);
    border: 1px solid var(--danger);
    color: var(--danger);
    padding: 2px 8px;
    border-radius: 4px;
    font-size: 11px;
    font-weight: 600;
    cursor: pointer;
    transition: all 0.15s ease;
  }
  .btn-banner-retry:hover {
    background: var(--danger);
    color: #ffffff;
  }
  @keyframes pulse {
    0%, 100% { transform: scale(1); opacity: 1; }
    50% { transform: scale(1.3); opacity: 0.6; }
  }
  .content-container {
    padding: 24px;
    flex: 1;
    display: flex;
    flex-direction: column;
    min-height: 0;
    overflow-y: auto;
  }
  .toast-container {
    position: fixed;
    bottom: 24px;
    right: 24px;
    display: flex;
    flex-direction: column;
    gap: 8px;
    z-index: 2000;
  }
  .toast {
    display: flex;
    align-items: center;
    gap: 8px;
    background: var(--bg-surface);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 10px 16px;
    font-size: 13px;
    color: var(--text-main);
    box-shadow: var(--shadow-lg);
    animation: toast-in 0.2s ease;
  }
  .toast.success {
    border-left: 4px solid var(--success);
  }
  .toast.error {
    border-left: 4px solid var(--danger);
  }
  @keyframes toast-in {
    from { transform: translateY(10px); opacity: 0; }
    to { transform: translateY(0); opacity: 1; }
  }
</style>
