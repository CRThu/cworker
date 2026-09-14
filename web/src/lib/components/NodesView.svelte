<!-- web/src/lib/components/NodesView.svelte - 节点矩阵视图 (含 Task Manager 风格 SVG 走势图与一键复制) -->
<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import { Plus, Trash2, Copy, Check, Radio, WifiOff } from 'lucide-svelte';
  import Sparkline from './Sparkline.svelte';
  import { copyToClipboard } from '../utils/format';
  import type { NodeInfo, KnownNode, OverviewData } from '../types';

  export let nodes: NodeInfo[] = [];
  export let knownNodes: KnownNode[] = [];
  export let overview: OverviewData | null = null;
  export let cpuHistoryMap: Record<string, number[]> = {};
  export let memHistoryMap: Record<string, number[]> = {};

  const dispatch = createEventDispatcher();
  let copiedNode: string | null = null;

  $: knownMap = (() => {
    const map: Record<string, KnownNode> = {};
    (knownNodes || []).forEach(k => {
      map[k.name.toLowerCase()] = k;
    });
    return map;
  })();

  $: totalFreeMemMB = (overview?.total_free_mem_mb && overview.total_free_mem_mb > 0)
    ? overview.total_free_mem_mb
    : nodes.filter(n => n.status === 'ONLINE').reduce((sum, n) => sum + (n.metrics?.mem_free_mb || 0), 0);

  $: totalMemMB = (overview?.total_mem_mb && overview.total_mem_mb > 0)
    ? overview.total_mem_mb
    : nodes.filter(n => n.status === 'ONLINE').reduce((sum, n) => sum + (n.metrics?.mem_total_mb || 0), 0);

  function openAddModal() {
    dispatch('openAddNode');
  }

  function handleRemove(name: string) {
    dispatch('removeNode', name);
  }

  async function handleCopyToken(token: string, nodeName: string) {
    if (!token) return;
    const ok = await copyToClipboard(token);
    if (ok) {
      copiedNode = nodeName;
      setTimeout(() => copiedNode = null, 1800);
    }
  }
</script>

<div class="nodes-view">
  <!-- 顶层关键度量卡片矩阵 -->
  <div class="stats-grid">
    <div class="stat-card">
      <div class="stat-label">总节点</div>
      <div class="stat-value">{overview?.nodes_count || nodes.length}</div>
      <div class="stat-sub">{overview?.online_count || 0} 在线</div>
    </div>
    <div class="stat-card">
      <div class="stat-label">活跃任务</div>
      <div class="stat-value text-primary">{overview?.active_jobs || 0}</div>
      <div class="stat-sub">运行中</div>
    </div>
    <div class="stat-card">
      <div class="stat-label">平均 CPU</div>
      <div class="stat-value">{(overview?.avg_cpu || 0).toFixed(1)}%</div>
      <div class="stat-sub">在线均值</div>
    </div>
    <div class="stat-card">
      <div class="stat-label">可用内存</div>
      <div class="stat-value">
        {(totalFreeMemMB / 1024).toFixed(1)}G{totalMemMB > 0 ? ` / ${(totalMemMB / 1024).toFixed(1)}G` : ''}
      </div>
      <div class="stat-sub">可分配 / 总量</div>
    </div>
  </div>

  <!-- 节点数据表格与专用操作工具栏 -->
  <div class="table-card">
    <div class="table-toolbar">
      <div class="toolbar-title">
        <span>节点列表</span>
        <span class="badge badge-completed">{nodes.length} 台</span>
      </div>
      <div class="toolbar-actions">
        <button class="btn btn-primary btn-sm" type="button" on:click={openAddModal}>
          <Plus size={14} />
          <span>添加节点</span>
        </button>
      </div>
    </div>

    <div class="table-responsive">
      <table class="data-table">
        <thead>
          <tr>
            <th style="width: 46px; text-align: center;">状态</th>
            <th>名称</th>
            <th>地址</th>
            <th style="min-width: 130px;">主机 CPU</th>
            <th style="min-width: 150px;">主机内存</th>
            <th style="width: 65px; text-align: center;">任务数</th>
            <th style="text-align: right; width: 140px;">操作</th>
          </tr>
        </thead>
        <tbody>
          {#each nodes as n, idx (n.name + '-' + idx)}
            {@const isOnline = n.status === 'ONLINE'}
            {@const cpu = isOnline && n.metrics ? n.metrics.cpu_percent : 0}
            {@const memTotalMB = isOnline && n.metrics ? n.metrics.mem_total_mb : 0}
            {@const memUsedMB = isOnline && n.metrics ? (n.metrics.mem_total_mb - n.metrics.mem_free_mb) : 0}
            {@const memPercent = memTotalMB > 0 ? (memUsedMB / memTotalMB) * 100 : 0}
            {@const known = knownMap[n.name.toLowerCase()]}
            {@const token = known?.token || ''}
            {@const cpuHistory = cpuHistoryMap[n.name] || [cpu]}
            {@const memHistory = memHistoryMap[n.name] || [memPercent]}

            <tr>
              <td style="text-align: center;">
                {#if isOnline}
                  <span class="status-icon-badge online" title="在线 (ONLINE)">
                    <Radio size={14} />
                  </span>
                {:else}
                  <span class="status-icon-badge offline" title="离线 (OFFLINE)">
                    <WifiOff size={14} />
                  </span>
                {/if}
              </td>
              <td><strong>{n.name}</strong></td>
              <td class="mono muted-text">{n.address}</td>
              <td>
                {#if isOnline}
                  <div class="metric-with-sparkline">
                    <Sparkline values={cpuHistory} color="#f97316" width={70} height={20} />
                    <span class="metric-val mono">{cpu.toFixed(1)}%</span>
                  </div>
                {:else}
                  <span class="dim-text">-</span>
                {/if}
              </td>
              <td>
                {#if isOnline}
                  <div class="metric-with-sparkline">
                    <Sparkline values={memHistory} color="#38bdf8" width={70} height={20} />
                    <span class="metric-val mono">{(memUsedMB / 1024).toFixed(1)}G / {(memTotalMB / 1024).toFixed(1)}G</span>
                  </div>
                {:else}
                  <span class="dim-text">-</span>
                {/if}
              </td>
              <td style="text-align: center;">
                <span class="badge {n.active_jobs > 0 ? 'badge-running' : 'badge-completed'}">
                  {n.active_jobs}
                </span>
              </td>
              <td style="text-align: right;">
                <div class="row-actions">
                  {#if token}
                    <button
                      class="btn btn-secondary btn-sm"
                      type="button"
                      on:click={() => handleCopyToken(token, n.name)}
                      title="复制访问 Token"
                    >
                      {#if copiedNode === n.name}
                        <Check size={12} class="text-success" />
                        <span>已复制</span>
                      {:else}
                        <Copy size={12} />
                        <span>Token</span>
                      {/if}
                    </button>
                  {/if}
                  <button
                    class="btn btn-danger btn-sm"
                    type="button"
                    on:click={() => handleRemove(n.name)}
                    title="从账本移除节点"
                  >
                    <Trash2 size={12} />
                    <span>移除</span>
                  </button>
                </div>
              </td>
            </tr>
          {/each}

          {#if nodes.length === 0}
            <tr>
              <td colspan="7" class="empty-cell">
                当前账本中暂无 Worker 节点。点击右上角 "+ 添加节点" 进行添加。
              </td>
            </tr>
          {/if}
        </tbody>
      </table>
    </div>
  </div>
</div>

<style>
  .nodes-view {
    display: flex;
    flex-direction: column;
    gap: 20px;
    width: 100%;
  }
  .stats-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
    gap: 14px;
  }
  .stat-card {
    background: var(--bg-surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 16px 18px;
    box-shadow: var(--shadow-sm);
  }
  .stat-label {
    font-size: 12px;
    color: var(--text-dim);
    font-weight: 500;
  }
  .stat-value {
    font-size: 26px;
    font-weight: 700;
    color: var(--text-main);
    margin: 4px 0 2px;
    line-height: 1.1;
  }
  .text-primary {
    color: var(--primary);
  }
  .stat-sub {
    font-size: 11px;
    color: var(--text-muted);
  }
  .table-toolbar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 12px 16px;
    border-bottom: 1px solid var(--border);
    background: var(--bg-surface);
  }
  .toolbar-title {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 14px;
    font-weight: 600;
  }
  .table-responsive {
    overflow-x: auto;
  }
  .metric-with-sparkline {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .metric-val {
    font-size: 12px;
    color: var(--text-main);
    white-space: nowrap;
  }
  .status-icon-badge {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 24px;
    height: 24px;
    border-radius: 6px;
  }
  .status-icon-badge.online {
    color: #22c55e;
    background: rgba(34, 197, 94, 0.12);
  }
  .status-icon-badge.offline {
    color: #94a3b8;
    background: rgba(148, 163, 184, 0.12);
  }
  .row-actions {
    display: inline-flex;
    align-items: center;
    justify-content: flex-end;
    gap: 6px;
  }
  .mono {
    font-family: var(--font-mono);
  }
  .muted-text {
    font-size: 12px;
    color: var(--text-muted);
  }
  .dim-text {
    font-size: 12px;
    color: var(--text-dim);
  }
  .empty-cell {
    text-align: center;
    color: var(--text-dim);
    padding: 40px 16px;
  }
</style>
