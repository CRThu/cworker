<!-- web/src/lib/components/KillConfirmModal.svelte - 精炼且专业的强制终止确认框 -->
<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import { AlertTriangle, X } from 'lucide-svelte';
  import type { JobInfo } from '../types';

  export let job: JobInfo | null = null;
  export let open: boolean = false;
  export let onConfirm: ((id: string) => void) | undefined = undefined;

  const dispatch = createEventDispatcher();

  function handleConfirm() {
    if (job) {
      if (onConfirm) onConfirm(job.id);
      dispatch('confirm', job.id);
    }
  }

  function handleClose() {
    dispatch('close');
  }
</script>

{#if open && job}
  <div class="modal-overlay" role="presentation" on:click|self={handleClose} on:keydown={(e) => e.key === 'Escape' && handleClose()}>
    <div class="modal-box">
      <div class="modal-header">
        <h3 class="modal-title">终止任务</h3>
        <button class="btn-icon close-btn" on:click={handleClose}>
          <X size={18} />
        </button>
      </div>

      <div class="modal-body">
        <div class="alert-box">
          <AlertTriangle size={18} />
          <span>确定终止该任务？将彻底清除其全部进程树。</span>
        </div>

        <div class="job-meta">
          <div class="meta-row">
            <span class="label">任务 ID:</span>
            <span class="value mono">{job.id}</span>
          </div>
          <div class="meta-row">
            <span class="label">节点:</span>
            <span class="value">{job.node}</span>
          </div>
          <div class="meta-row">
            <span class="label">命令:</span>
            <span class="value mono cmd">{job.command}</span>
          </div>
        </div>
      </div>

      <div class="modal-footer">
        <button class="btn btn-secondary" on:click={handleClose}>取消</button>
        <button class="btn btn-danger" on:click={handleConfirm}>终止任务</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .modal-overlay {
    position: fixed;
    inset: 0;
    background-color: rgba(0, 0, 0, 0.65);
    backdrop-filter: blur(2px);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 1000;
  }
  .modal-box {
    background: var(--bg-surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    width: 90%;
    max-width: 440px;
    box-shadow: var(--shadow-lg);
    overflow: hidden;
  }
  .modal-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 14px 18px;
    border-bottom: 1px solid var(--border-subtle);
  }
  .modal-title {
    font-size: 15px;
    font-weight: 600;
    color: var(--danger);
  }
  .close-btn {
    font-size: 20px;
    line-height: 1;
    padding: 2px 6px;
  }
  .modal-body {
    padding: 16px 18px;
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  .alert-box {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    padding: 10px 12px;
    border-radius: 6px;
    background-color: var(--danger-subtle);
    color: var(--danger);
    font-size: 13px;
    line-height: 1.4;
  }
  .job-meta {
    background: var(--bg-elevated);
    padding: 10px 12px;
    border-radius: 6px;
    display: flex;
    flex-direction: column;
    gap: 6px;
    font-size: 12px;
  }
  .meta-row {
    display: flex;
    gap: 8px;
  }
  .label {
    color: var(--text-dim);
    width: 60px;
    flex-shrink: 0;
  }
  .value {
    color: var(--text-main);
    word-break: break-all;
  }
  .mono {
    font-family: var(--font-mono);
  }
  .cmd {
    color: var(--text-muted);
  }
  .modal-footer {
    display: flex;
    justify-content: flex-end;
    gap: 10px;
    padding: 12px 18px;
    background: var(--bg-elevated);
    border-top: 1px solid var(--border-subtle);
  }
</style>
