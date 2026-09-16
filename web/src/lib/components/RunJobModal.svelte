<!-- web/src/lib/components/RunJobModal.svelte - 派发新任务模态框 (支持多节点并发派发、智能随机命名) -->
<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import { Folder } from 'lucide-svelte';
  import { generateJobName } from '../utils/format';
  import DirPickerModal from './DirPickerModal.svelte';
  import MultiSelect from './MultiSelect.svelte';
  import type { NodeInfo } from '../types';

  export let open: boolean = false;
  export let nodes: NodeInfo[] = [];
  export let onSubmit: ((detail: any) => void) | undefined = undefined;
  export let onClose: (() => void) | undefined = undefined;

  const dispatch = createEventDispatcher();

  let selectedNodes: string[] = [];
  let jobName: string = '';
  let jobDir: string = '';
  let jobCmd: string = '';
  let showDirPicker: boolean = false;
  let wasOpen: boolean = false;

  $: nodeNames = nodes.map(n => n.name);

  // 模态框打开时仅在首次打开时预填随机名称与首选在线节点
  $: if (open && !wasOpen) {
    wasOpen = true;
    if (!jobName) {
      jobName = generateJobName();
    }
    if (nodes.length > 0) {
      const onlineNode = nodes.find(n => n.status === 'ONLINE');
      selectedNodes = [onlineNode ? onlineNode.name : nodes[0].name];
    }
  } else if (!open) {
    wasOpen = false;
  }

  function handleClose() {
    if (onClose) onClose();
    dispatch('close');
  }

  function handleKeydown(e: KeyboardEvent) {
    if (open && e.key === 'Escape' && !showDirPicker) {
      e.preventDefault();
      handleClose();
    }
  }

  function handleSubmit() {
    if (!jobCmd.trim() || selectedNodes.length === 0) return;
    const payload = {
      nodes: selectedNodes,
      node: selectedNodes[0] || '',
      name: jobName.trim() || generateJobName(),
      dir: jobDir.trim(),
      command: jobCmd.trim(),
    };
    if (onSubmit) onSubmit(payload);
    dispatch('submit', payload);
    jobCmd = '';
    jobName = '';
    jobDir = '';
  }

  function randomizeName() {
    jobName = generateJobName();
  }
</script>

<svelte:window on:keydown={handleKeydown} />

{#if open}
  <div class="modal-overlay" role="presentation" on:click|self={handleClose}>
    <div class="modal-box">
      <div class="modal-header">
        <h3 class="modal-title">派发新任务</h3>
        <button class="btn-icon close-btn" on:click={handleClose}>&times;</button>
      </div>

      <form on:submit|preventDefault={handleSubmit}>
        <div class="modal-body">
          <div class="form-group">
            <span class="form-label">目标 Worker 节点 (支持多选) *</span>
            <MultiSelect
              options={nodeNames}
              bind:selected={selectedNodes}
              placeholder="请选择目标节点"
            />
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
            <div class="input-with-action">
              <input
                id="run-job-dir"
                type="text"
                class="input w-full"
                bind:value={jobDir}
                placeholder="留空使用默认目录"
              />
              <button
                type="button"
                class="btn btn-secondary btn-browse"
                on:click={() => showDirPicker = true}
                title="弹出选择目录窗口"
              >
                <Folder size={14} />
                <span>浏览</span>
              </button>
            </div>
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
          <button type="submit" class="btn btn-primary" disabled={!jobCmd.trim() || selectedNodes.length === 0}>派发任务</button>
        </div>
      </form>
    </div>
  </div>

  <DirPickerModal
    open={showDirPicker}
    node={selectedNodes.length > 0 ? selectedNodes[0] : ''}
    initialPath={jobDir}
    on:select={(e) => {
      jobDir = e.detail.path;
      showDirPicker = false;
    }}
    on:close={() => showDirPicker = false}
  />
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
  .input-with-action {
    display: flex;
    gap: 8px;
    align-items: center;
  }
  .btn-browse {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    white-space: nowrap;
    padding: 0 12px;
    height: 34px;
    font-size: 12px;
    box-sizing: border-box;
  }
</style>
