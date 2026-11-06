<!-- web/src/lib/components/LogTerminal.svelte - 实时流式终端抽屉 (全套 SVG 矢量图标，支持断开保持、弹性自动重连与重新加载) -->
<script lang="ts">
  import { onMount, onDestroy, createEventDispatcher } from 'svelte';
  import { Terminal, RotateCcw, Trash2, Download, X } from 'lucide-svelte';
  import { ansiToHtml } from '../utils/ansi';

  export let jobId: string = '';
  export let node: string = '';
  export let status: string = 'RUNNING';
  export let open: boolean = false;
  export let onClose: (() => void) | undefined = undefined;

  const dispatch = createEventDispatcher();

  let outputEl: HTMLDivElement;
  let rawBuffer = '';
  let renderedHtml = '';
  let autoScroll = true;
  let ws: WebSocket | null = null;
  let wsStatus = '未连接';
  let isConnected = false;

  // 弹性重连状态机
  let reconnectTimer: any = null;
  let reconnectAttempts = 0;
  let isManualClose = false;
  let needsBufferReset = false;

  $: if (open && jobId) {
    isManualClose = false;
    connectWs(false);
  } else if (!open) {
    disconnectWs();
  }

  $: if (status && status !== 'RUNNING') {
    // 任务状态终态（如 COMPLETED/FAILED/STOPPED），取消重连定时器
    clearReconnectTimer();
  }

  function clearReconnectTimer() {
    if (reconnectTimer) {
      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
  }

  function scheduleReconnect(customPrefix?: string) {
    if (!open || !jobId || isManualClose) return;
    if (status !== 'RUNNING' && rawBuffer.trim()) return;

    clearReconnectTimer();

    // 指数退避调度 (1s -> 2s -> 3s -> 最大 5s)
    const delays = [1000, 2000, 3000, 5000];
    const delay = delays[Math.min(reconnectAttempts, delays.length - 1)];
    reconnectAttempts++;

    const sec = Math.round(delay / 1000);
    const prefix = customPrefix || '连接已断开';
    wsStatus = `${prefix}，${sec} 秒后自动重试...`;

    reconnectTimer = setTimeout(() => {
      if (!open || isManualClose) return;
      connectWs(true);
    }, delay);
  }

  function connectWs(isReconnect = false) {
    // 关闭已有连接但保持 isManualClose 为 false
    if (ws) {
      try {
        if (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING) {
          ws.close();
        }
      } catch (_) {}
      ws = null;
    }
    clearReconnectTimer();
    isConnected = false;

    if (!isReconnect) {
      rawBuffer = '';
      renderedHtml = '';
      needsBufferReset = false;
      reconnectAttempts = 0;
      wsStatus = '连接中...';
    } else {
      // 重连模式：保持已渲染内容避免白屏闪烁，待第一包新推流数据到达时原子重置
      needsBufferReset = true;
      wsStatus = '正在重连...';
    }

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const params = new URLSearchParams();
    params.set('job_id', jobId);
    if (node) params.set('node', node);
    const wsUrl = `${protocol}//${window.location.host}/api/ui/jobs/stream?${params.toString()}`;

    try {
      ws = new WebSocket(wsUrl);
      ws.onopen = () => {
        isConnected = true;
        reconnectAttempts = 0;
        clearReconnectTimer();
        wsStatus = '实时传输中';
      };

      ws.onmessage = (event) => {
        if (needsBufferReset) {
          rawBuffer = '';
          renderedHtml = '';
          needsBufferReset = false;
        }
        const text = event.data;
        rawBuffer += text;
        renderedHtml += ansiToHtml(text);
        if (autoScroll && outputEl) {
          requestAnimationFrame(() => {
            if (outputEl) outputEl.scrollTop = outputEl.scrollHeight;
          });
        }
      };

      ws.onclose = async () => {
        isConnected = false;
        if (isManualClose || !open) return;

        // 若任务已处于终态
        if (status !== 'RUNNING') {
          if (!rawBuffer.trim()) {
            await fetchFallbackLogs();
          } else {
            wsStatus = '传输完毕';
          }
          return;
        }

        // 任务仍处于 RUNNING 状态，但推流连接关闭 (网络闪断、连接中断或服务重启)
        if (!rawBuffer.trim()) {
          await fetchFallbackLogs();
          if (wsStatus.startsWith('加载失败')) {
            scheduleReconnect(wsStatus);
          } else {
            scheduleReconnect();
          }
        } else {
          wsStatus = '连接已断开';
          scheduleReconnect();
        }
      };

      ws.onerror = async () => {
        isConnected = false;
        if (isManualClose || !open) return;
        // 浏览器标准下 onerror 之后通常触发 onclose；若部分测试 mock 未派发 onclose 则兜底调度
      };
    } catch (e: any) {
      if (!isManualClose && open) {
        fetchFallbackLogs().then(() => {
          scheduleReconnect();
        });
      }
    }
  }

  async function fetchFallbackLogs() {
    wsStatus = '正在加载日志...';
    try {
      const query = new URLSearchParams();
      query.set('job_id', jobId);
      if (node) query.set('node', node);
      query.set('lines', '500');
      const res = await fetch(`/api/ui/jobs/logs?${query.toString()}`);
      if (!res.ok) {
        const errText = await res.text().catch(() => '');
        wsStatus = `加载失败: ${errText || res.statusText || '服务异常'}`;
        return;
      }
      const text = await res.text();
      if (text) {
        rawBuffer = text;
        renderedHtml = ansiToHtml(text);
        wsStatus = status === 'RUNNING' ? '已加载完整日志' : '传输完毕';
        if (autoScroll && outputEl) {
          requestAnimationFrame(() => {
            if (outputEl) outputEl.scrollTop = outputEl.scrollHeight;
          });
        }
      } else {
        wsStatus = '暂无日志输出';
      }
    } catch (e: any) {
      wsStatus = `加载失败: ${e?.message || '网络连接异常'}`;
    }
  }

  function disconnectWs() {
    isManualClose = true;
    clearReconnectTimer();
    reconnectAttempts = 0;
    if (ws) {
      try {
        if (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING) {
          ws.close();
        }
      } catch (_) {}
      ws = null;
    }
    isConnected = false;
  }

  function handleOnline() {
    // 监听网络恢复事件，快速触发重连
    if (open && jobId && !isConnected && (status === 'RUNNING' || !rawBuffer.trim())) {
      clearReconnectTimer();
      reconnectAttempts = 0;
      connectWs(true);
    }
  }

  function handleClear() {
    rawBuffer = '';
    renderedHtml = '';
  }

  function handleReload() {
    clearReconnectTimer();
    reconnectAttempts = 0;
    rawBuffer = '';
    renderedHtml = '';
    connectWs(false);
  }

  function handleDownload() {
    const blob = new Blob([rawBuffer], { type: 'text/plain;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `${jobId || 'job'}.log`;
    a.click();
    URL.revokeObjectURL(url);
  }

  function handleClose() {
    disconnectWs();
    if (onClose) onClose();
    dispatch('close');
  }

  function handleKeydown(e: KeyboardEvent) {
    if (open && e.key === 'Escape') {
      e.preventDefault();
      handleClose();
    }
  }

  onDestroy(() => {
    disconnectWs();
  });
</script>

<svelte:window on:keydown={handleKeydown} on:online={handleOnline} />

{#if open}
  <div class="terminal-modal-overlay" role="presentation" on:click|self={handleClose} on:keydown={(e) => e.key === 'Escape' && handleClose()}>
    <div class="terminal-box">
      <!-- 终端顶栏 -->
      <div class="terminal-header">
        <div class="terminal-title">
          <Terminal size={18} />
          <span class="title-text">日志</span>
          <span class="job-id-pill mono">{jobId}</span>
          {#if node}
            <span class="node-badge mono">{node}</span>
          {/if}
          <span class="badge {status === 'RUNNING' ? 'badge-running' : 'badge-completed'}">
            {status === 'RUNNING' ? '运行中' : (status === 'COMPLETED' ? '已完成' : (status === 'FAILED' ? '已失败' : '已终止'))}
          </span>
        </div>

        <!-- 矢量控制图标工具栏 -->
        <div class="terminal-toolbar">
          <label class="autoscroll-toggle">
            <input type="checkbox" bind:checked={autoScroll} />
            <span>自动滚屏</span>
          </label>

          <button class="btn-icon" title="重新载入历史日志" on:click={handleReload}>
            <RotateCcw size={16} />
          </button>

          <button class="btn-icon" title="清屏" on:click={handleClear}>
            <Trash2 size={16} />
          </button>

          <button class="btn-icon" title="下载日志文件 (.log)" on:click={handleDownload}>
            <Download size={16} />
          </button>

          <button class="btn-icon close-btn" title="关闭抽屉" on:click={handleClose}>
            <X size={18} />
          </button>
        </div>
      </div>

      <!-- 终端内容区 -->
      <div class="terminal-body mono" bind:this={outputEl}>
        {#if renderedHtml}
          <!-- eslint-disable-next-line svelte/no-at-html-tags -->
          {@html renderedHtml}
        {:else if wsStatus.startsWith('加载失败')}
          <div class="empty-hint text-danger">{wsStatus}</div>
        {:else}
          <div class="empty-hint">{wsStatus === '正在加载日志...' ? '正在加载日志...' : '暂无日志输出'}</div>
        {/if}
      </div>

      <!-- 终端底栏状态提示 -->
      <div class="terminal-footer">
        <span class="footer-hint">{wsStatus}</span>
        <button class="btn btn-secondary btn-sm" on:click={handleClose}>关闭</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .terminal-modal-overlay {
    position: fixed;
    inset: 0;
    background-color: rgba(0, 0, 0, 0.75);
    backdrop-filter: blur(3px);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 1000;
  }
  .terminal-box {
    background: #090d16;
    border: 1px solid var(--border);
    border-radius: 8px;
    width: 92%;
    max-width: 900px;
    height: 80vh;
    display: flex;
    flex-direction: column;
    box-shadow: var(--shadow-lg);
    overflow: hidden;
  }
  .terminal-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 10px 16px;
    background: #0f172a;
    border-bottom: 1px solid #1e293b;
    color: #f8fafc;
  }
  .terminal-title {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 13px;
    font-weight: 600;
  }
  .job-id-pill {
    color: var(--primary);
    background: rgba(249, 115, 22, 0.15);
    padding: 2px 6px;
    border-radius: 4px;
    font-size: 12px;
  }
  .terminal-toolbar {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .autoscroll-toggle {
    display: flex;
    align-items: center;
    gap: 4px;
    font-size: 12px;
    color: #94a3b8;
    cursor: pointer;
    user-select: none;
    margin-right: 4px;
  }
  .close-btn {
    font-size: 20px;
    line-height: 1;
    color: #94a3b8;
  }
  .terminal-body {
    flex: 1;
    background: #030712;
    color: #e2e8f0;
    padding: 14px 16px;
    overflow-y: auto;
    font-size: 12px;
    line-height: 1.55;
    white-space: pre-wrap;
    word-break: break-all;
  }
  .empty-hint {
    color: #64748b;
    text-align: center;
    padding: 32px 0;
  }
  .empty-hint.text-danger {
    color: var(--danger);
  }
  .terminal-footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 8px 16px;
    background: #0f172a;
    border-top: 1px solid #1e293b;
    font-size: 12px;
    color: #94a3b8;
  }
  .footer-hint {
    font-size: 12px;
  }
  .mono {
    font-family: var(--font-mono);
  }
</style>
