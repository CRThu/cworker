<!-- web/src/lib/components/LogTerminal.svelte - 实时流式终端抽屉 (全套 SVG 矢量图标，支持断开保持与重新加载) -->
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

  $: if (open && jobId) {
    connectWs();
  } else if (!open) {
    disconnectWs();
  }

  function connectWs() {
    disconnectWs();
    rawBuffer = '';
    renderedHtml = '';
    wsStatus = '连接中...';

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const params = new URLSearchParams();
    params.set('job_id', jobId);
    if (node) params.set('node', node);
    const wsUrl = `${protocol}//${window.location.host}/api/ui/jobs/stream?${params.toString()}`;

    try {
      ws = new WebSocket(wsUrl);
      ws.onopen = () => {
        isConnected = true;
        wsStatus = '实时传输中';
      };

      ws.onmessage = (event) => {
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
        // 若 WebSocket 未接收到任何日志，自动通过 REST API 回退拉取完整历史日志
        if (!rawBuffer.trim()) {
          await fetchFallbackLogs();
        } else {
          wsStatus = status === 'RUNNING' ? '连接已断开' : '传输完毕';
        }
      };

      ws.onerror = async () => {
        isConnected = false;
        // 连接异常时同样尝试通过 REST 回退拉取
        if (!rawBuffer.trim()) {
          await fetchFallbackLogs();
        } else {
          wsStatus = status === 'RUNNING' ? '连接异常中断' : '连接断开';
        }
      };
    } catch (e: any) {
      fetchFallbackLogs();
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
        wsStatus = '已加载完整日志';
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
    if (ws) {
      if (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING) {
        ws.close();
      }
      ws = null;
    }
    isConnected = false;
  }

  function handleClear() {
    rawBuffer = '';
    renderedHtml = '';
  }

  function handleReload() {
    rawBuffer = '';
    renderedHtml = '';
    connectWs();
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

<svelte:window on:keydown={handleKeydown} />

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
