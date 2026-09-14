<!-- web/src/lib/components/CleanJobsModal.svelte - 历史任务与日志清理模态框 (支持多节点勾选与精炼文案) -->
<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import MultiSelect from './MultiSelect.svelte';
  import type { NodeInfo } from '../types';

  export let open: boolean = false;
  export let nodes: NodeInfo[] = [];

  const dispatch = createEventDispatcher();

  let selectedNodes: string[] = [];
  let cleanAll: boolean = false;
  let days: number = 7;

  $: nodeNames = nodes.map(n => n.name);

  function handleClose() {
    dispatch('close');
  }

  function handleSubmit() {
    dispatch('submit', {
      nodes: selectedNodes.length > 0 ? selectedNodes : undefined,
      node: selectedNodes.length === 1 ? selectedNodes[0] : (selectedNodes.length === 0 ? '' : undefined),
      all: cleanAll,
      days: cleanAll ? 0 : days,
    });
  }
</script>

{#if open}
  <div class="modal-overlay" role="presentation" on:click|self={handleClose}>
    <div class="modal-box">
      <div class="modal-header">
        <h3 class="modal-title">清理历史任务</h3>
        <button class="btn-icon close-btn" on:click={handleClose}>&times;</button>
      </div>

      <form on:submit|preventDefault={handleSubmit}>
        <div class="modal-body">
          <div class="form-group">
            <span class="form-label">目标节点 (支持多选)</span>
            <MultiSelect
              options={nodeNames}
              bind:selected={selectedNodes}
              placeholder="全部节点"
            />
          </div>

          <div class="form-group">
            <span class="form-label">清理策略</span>
            <div class="radio-options">
              <label class="radio-label">
                <input type="radio" name="cleanStrategy" checked={!cleanAll} on:change={() => cleanAll = false} />
                <span>清理超过</span>
                <input
                  type="number"
                  class="input num-input"
                  min="1"
                  max="365"
                  bind:value={days}
                  disabled={cleanAll}
                />
                <span>天的已结束任务</span>
              </label>

              <label class="radio-label">
                <input type="radio" name="cleanStrategy" checked={cleanAll} on:change={() => cleanAll = true} />
                <span>清理全部已结束任务</span>
              </label>
            </div>
          </div>
        </div>

        <div class="modal-footer">
          <button type="button" class="btn btn-secondary" on:click={handleClose}>取消</button>
          <button type="submit" class="btn btn-primary">执行清理</button>
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
    max-width: 480px;
    box-shadow: var(--shadow-lg);
    overflow: hidden;
  }
  .modal-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 14px 20px;
    border-bottom: 1px solid var(--border);
    background: var(--bg-elevated);
  }
  .modal-title {
    margin: 0;
    font-size: 15px;
    font-weight: 600;
    color: var(--text-main);
  }
  .modal-body {
    padding: 20px;
    display: flex;
    flex-direction: column;
    gap: 16px;
  }
  .form-group {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .form-label {
    font-size: 13px;
    font-weight: 500;
    color: var(--text-main);
  }
  .radio-options {
    display: flex;
    flex-direction: column;
    gap: 10px;
    margin-top: 4px;
  }
  .radio-label {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 13px;
    color: var(--text-muted);
    cursor: pointer;
  }
  .num-input {
    width: 70px;
    padding: 4px 8px;
    text-align: center;
  }
  .modal-footer {
    display: flex;
    justify-content: flex-end;
    gap: 10px;
    padding: 12px 20px;
    border-top: 1px solid var(--border);
    background: var(--bg-elevated);
  }
  .close-btn {
    font-size: 18px;
  }
</style>
