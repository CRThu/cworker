<!-- web/src/lib/components/JobsView.svelte - 任务治理视图 (含多选节点、0ms实时搜索过滤与规范操作栏) -->
<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import { Search, X, Plus, Trash2 } from 'lucide-svelte';
  import MultiSelect from './MultiSelect.svelte';
  import { formatUptime } from '../utils/format';
  import type { JobInfo, NodeInfo, JobStatus } from '../types';

  export let jobs: JobInfo[] = [];
  export let nodes: NodeInfo[] = [];
  export let onOpenRun: (() => void) | undefined = undefined;
  export let onOpenClean: (() => void) | undefined = undefined;
  export let onViewLogs: ((job: JobInfo) => void) | undefined = undefined;
  export let onKillJob: ((job: JobInfo) => void) | undefined = undefined;

  const dispatch = createEventDispatcher();

  let selectedNodes: string[] = [];
  let statusFilter: string = '';
  let searchQuery: string = '';

  $: nodeNames = nodes.map(n => n.name);

  // 纯客户端 0ms 即时过滤 (杜绝并发网络请求回包乱序与页面跳动)
  $: filteredJobs = jobs.filter(j => {
    // 1. 节点多选过滤
    if (selectedNodes.length > 0 && !selectedNodes.includes(j.node)) {
      return false;
    }
    // 2. 状态过滤
    if (statusFilter && j.status !== statusFilter) {
      return false;
    }
    // 3. 搜索词匹配 (ID、名称、命令、节点)
    if (searchQuery.trim()) {
      const q = searchQuery.trim().toLowerCase();
      const matchId = j.id.toLowerCase().includes(q);
      const matchName = (j.name || '').toLowerCase().includes(q);
      const matchCmd = j.command.toLowerCase().includes(q);
      const matchNode = j.node.toLowerCase().includes(q);
      if (!matchId && !matchName && !matchCmd && !matchNode) {
        return false;
      }
    }
    return true;
  });

  function openRunModal() {
    if (onOpenRun) onOpenRun();
    dispatch('openRun');
  }

  function openCleanModal() {
    if (onOpenClean) onOpenClean();
    dispatch('openClean');
  }

  function formatStatus(status: JobStatus): string {
    switch (status) {
      case 'RUNNING': return '运行中';
      case 'COMPLETED': return '已完成';
      case 'FAILED': return '已失败';
      case 'STOPPED': return '已终止';
      default: return status;
    }
  }

  function handleViewLogs(job: JobInfo) {
    if (onViewLogs) onViewLogs(job);
    dispatch('viewLogs', job);
  }

  function handleKill(job: JobInfo) {
    if (onKillJob) onKillJob(job);
    dispatch('killJob', job);
  }
</script>

<div class="jobs-view">
  <!-- 综合过滤与操作工具栏 -->
  <div class="filter-toolbar">
    <div class="filter-group">
      <!-- 节点多选下拉框 (支持全选与清空) -->
      <MultiSelect
        options={nodeNames}
        bind:selected={selectedNodes}
        placeholder="全部节点"
      />

      <!-- 状态筛选 Pills -->
      <div class="status-pills">
        <button
          type="button"
          class="pill-btn {statusFilter === '' ? 'active' : ''}"
          on:click={() => statusFilter = ''}
        >
          全部
        </button>
        <button
          type="button"
          class="pill-btn {statusFilter === 'RUNNING' ? 'active' : ''}"
          on:click={() => statusFilter = 'RUNNING'}
        >
          运行中
        </button>
        <button
          type="button"
          class="pill-btn {statusFilter === 'COMPLETED' ? 'active' : ''}"
          on:click={() => statusFilter = 'COMPLETED'}
        >
          已完成
        </button>
        <button
          type="button"
          class="pill-btn {statusFilter === 'FAILED' ? 'active' : ''}"
          on:click={() => statusFilter = 'FAILED'}
        >
          失败
        </button>
        <button
          type="button"
          class="pill-btn {statusFilter === 'STOPPED' ? 'active' : ''}"
          on:click={() => statusFilter = 'STOPPED'}
        >
          已终止
        </button>
      </div>

      <!-- 0ms 客户端即时搜索输入框 -->
      <div class="search-wrap">
        <span class="search-icon">
          <Search size={14} />
        </span>
        <input
          type="text"
          class="input search-input"
          placeholder="搜索 ID / 名称 / 命令 / 节点..."
          bind:value={searchQuery}
        />
        {#if searchQuery}
          <button type="button" class="btn-icon clear-search" on:click={() => searchQuery = ''}>
            <X size={14} />
          </button>
        {/if}
      </div>
    </div>

    <!-- 右侧操作按钮组 -->
    <div class="action-group">
      <button class="btn btn-secondary btn-sm" type="button" on:click={openCleanModal}>
        <Trash2 size={13} />
        <span>清理</span>
      </button>
      <button class="btn btn-primary btn-sm" type="button" on:click={openRunModal}>
        <Plus size={14} />
        <span>派发任务</span>
      </button>
    </div>
  </div>

  <!-- 任务数据表格 -->
  <div class="table-card">
    <div class="table-responsive">
      <table class="data-table">
        <thead>
          <tr>
            <th style="width: 105px;">ID</th>
            <th style="width: 76px;">节点</th>
            <th style="min-width: 120px;">名称</th>
            <th style="width: 76px; text-align: center;">状态</th>
            <th style="width: 65px; text-align: right;" title="进程树 CPU 开销">CPU</th>
            <th style="width: 65px; text-align: right;" title="进程树内存开销">内存</th>
            <th style="width: 65px; text-align: right;">时长</th>
            <th>命令</th>
            <th style="text-align: right; width: 105px;">操作</th>
          </tr>
        </thead>
        <tbody>
          {#each filteredJobs as j, idx (j.id + '-' + idx)}
            {@const isRunning = j.status === 'RUNNING'}
            {@const uptime = formatUptime(j.start_time, j.end_time)}
            {@const cpuStr = isRunning && j.metrics ? `${j.metrics.cpu_percent.toFixed(1)}%` : '-'}
            {@const memStr = isRunning && j.metrics ? `${j.metrics.memory_mb}M` : '-'}
            {@const cmdDisplay = j.command.length > 40 ? j.command.slice(0, 36) + '...' : j.command}

            <tr>
              <td class="mono font-semibold text-primary cell-nowrap">{j.id}</td>
              <td class="cell-nowrap"><span class="node-badge mono">{j.node}</span></td>
              <td class="cell-nowrap"><strong>{j.name || '-'}</strong></td>
              <td style="text-align: center;" class="cell-nowrap">
                <span class="badge {
                  j.status === 'RUNNING' ? 'badge-running' :
                  j.status === 'COMPLETED' ? 'badge-completed' :
                  j.status === 'FAILED' ? 'badge-failed' : 'badge-stopped'
                }">
                  {formatStatus(j.status)}
                </span>
              </td>
              <td class="mono text-sm cell-nowrap" style="text-align: right;">{cpuStr}</td>
              <td class="mono text-sm cell-nowrap" style="text-align: right;">{memStr}</td>
              <td class="text-muted text-sm cell-nowrap" style="text-align: right;">{uptime}</td>
              <td class="mono cmd-text text-sm" title={j.command}>{cmdDisplay}</td>
              <td class="cell-nowrap" style="text-align: right;">
                <button
                  class="btn btn-secondary btn-sm"
                  type="button"
                  on:click={() => handleViewLogs(j)}
                >
                  日志
                </button>
                {#if isRunning}
                  <button
                    class="btn btn-danger btn-sm"
                    type="button"
                    style="margin-left: 6px;"
                    on:click={() => handleKill(j)}
                  >
                    终止
                  </button>
                {/if}
              </td>
            </tr>
          {/each}

          {#if filteredJobs.length === 0}
            <tr>
              <td colspan="9" class="empty-cell">
                {#if jobs.length === 0}
                  暂无任务记录
                {:else}
                  无匹配任务
                {/if}
              </td>
            </tr>
          {/if}
        </tbody>
      </table>
    </div>
  </div>
</div>

<style>
  .jobs-view {
    display: flex;
    flex-direction: column;
    gap: 16px;
    width: 100%;
  }
  .filter-toolbar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 12px;
    background: var(--bg-surface);
    padding: 10px 14px;
    border-radius: 8px;
    border: 1px solid var(--border);
  }
  .filter-group {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 10px;
    flex: 1;
  }
  .status-pills {
    display: flex;
    background: var(--bg-elevated);
    padding: 2px;
    border-radius: 6px;
    border: 1px solid var(--border-subtle);
  }
  .pill-btn {
    padding: 4px 10px;
    font-size: 12px;
    border: none;
    background: transparent;
    color: var(--text-muted);
    border-radius: 4px;
    cursor: pointer;
    transition: all 0.15s ease;
  }
  .pill-btn.active {
    background: var(--bg-surface);
    color: var(--primary);
    font-weight: 600;
    box-shadow: var(--shadow-sm);
  }
  .search-wrap {
    position: relative;
    display: flex;
    align-items: center;
  }
  .search-icon {
    position: absolute;
    left: 10px;
    color: var(--text-dim);
    pointer-events: none;
  }
  .search-input {
    padding-left: 30px;
    padding-right: 26px;
    width: 240px;
  }
  .clear-search {
    position: absolute;
    right: 6px;
    padding: 2px 6px;
    font-size: 14px;
    color: var(--text-dim);
  }
  .action-group {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .table-responsive {
    overflow-x: auto;
  }
  .node-badge {
    font-size: 11px;
    background: var(--bg-elevated);
    padding: 2px 6px;
    border-radius: 4px;
    color: var(--text-muted);
  }
  .font-semibold {
    font-weight: 600;
  }
  .text-primary {
    color: var(--primary);
  }
  .text-sm {
    font-size: 12px;
  }
  .text-muted {
    color: var(--text-muted);
  }
  .cmd-text {
    color: var(--text-dim);
    max-width: 260px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .empty-cell {
    text-align: center;
    color: var(--text-dim);
    padding: 40px 16px;
  }
  .mono {
    font-family: var(--font-mono);
  }
  .cell-nowrap {
    white-space: nowrap;
  }
</style>
