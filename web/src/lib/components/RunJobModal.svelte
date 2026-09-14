<!-- web/src/lib/components/RunJobModal.svelte - 派发新任务模态框 (默认智能随机命名) -->
<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import { generateJobName } from '../utils/format';
  import type { NodeInfo } from '../types';

  export let open: boolean = false;
  export let nodes: NodeInfo[] = [];

  const dispatch = createEventDispatcher();

  let targetNode: string = '';
  let jobName: string = '';
  let jobDir: string = '';
  let jobCmd: string = '';

  // 模态框打开时自动预填一个随机名称
  $: if (open) {
    if (!jobName) {
      jobName = generateJobName();
    }
    if (!targetNode && nodes.length > 0) {
      const onlineNode = nodes.find(n => n.status === 'ONLINE');
      targetNode = onlineNode ? onlineNode.name : nodes[0].name;
    }
  }

  function handleClose() {
    dispatch('close');
  }

  function handleSubmit() {
    if (!jobCmd.trim()) return;
    dispatch('submit', {
      node: targetNode,
      name: jobName.trim() || generateJobName(),
      dir: jobDir.trim(),
      command: jobCmd.trim(),
    });
    jobCmd = '';
    jobName = '';
    jobDir = '';
  }

  function randomizeName() {
    jobName = generateJobName();
  }
</script>

{#if open}
  <div class="modal-overlay" on:click|self={handleClose}>
    <div class="modal-box">
      <div class="modal-header">
        <h3 class="modal-title">派发新任务</h3>
        <button class="btn-icon close-btn" on:click={handleClose}>&times;</button>
      </div>

      <form on:submit|preventDefault={handleSubmit}>
        <div class="modal-body">
          <div class="form-group">
            <label for="run-node-select" class="form-label">目标 Worker 节点 *</label>
            <select id="run-node-select" class="select w-full" bind:value={targetNode} required>
              {#each nodes as n}
                <option value={n.name}>
                  {n.name} ({n.status === 'ONLINE' ? '在线' : '离线'})
                </option>
              {/each}
              {#if nodes.length === 0}
                <option value="">暂无可用节点</option>
              {/if}
            </select>
          </div>

          <div class="form-group">
            <div class="label-with-action">
              <label for="run-job-name" class="form-label">任务名称</label>
              <button type="button" class="btn-text-sm" on:click={randomizeName}>随机生成</button>
            </div>
            <input
              id="run-job-name"
              type="text"
              class="input w-full"
              bind:value={jobName}
              placeholder="默认自动生成"
            />
          </div>

          <div class="form-group">
            <label for="run-job-dir" class="form-label">工作目录</label>
            <input
              id="run-job-dir"
              type="text"
              class="input w-full"
              bind:value={jobDir}
              placeholder="留空使用默认目录"
            />
          </div>

          <div class="form-group">
            <label for="run-job-cmd" class="form-label">执行命令 *</label>
            <textarea
              id="run-job-cmd"
              class="input w-full mono textarea"
              bind:value={jobCmd}
              placeholder="输入命令，例如: python main.py"
              rows="3"
              required
            ></textarea>
          </div>
        </div>

        <div class="modal-footer">
          <button type="button" class="btn btn-secondary" on:click={handleClose}>取消</button>
          <button type="submit" class="btn btn-primary" disabled={!jobCmd.trim()}>派发任务</button>
        </div>
      </form>
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
    max-width: 500px;
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
    color: var(--text-main);
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
    gap: 12px;
  }
  .form-group {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .form-label {
    font-size: 12px;
    font-weight: 600;
    color: var(--text-muted);
  }
  .label-with-action {
    display: flex;
    justify-content: space-between;
    align-items: center;
  }
  .btn-text-sm {
    background: none;
    border: none;
    color: var(--primary);
    font-size: 11px;
    cursor: pointer;
  }
  .w-full {
    width: 100%;
  }
  .mono {
    font-family: var(--font-mono);
  }
  .textarea {
    resize: vertical;
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
