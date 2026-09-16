<!-- web/src/lib/components/AddNodeModal.svelte - 添加 Worker 节点模态框 -->
<script lang="ts">
  import { createEventDispatcher } from 'svelte';

  export let open: boolean = false;
  export let onSubmit: ((detail: any) => void) | undefined = undefined;
  export let onClose: (() => void) | undefined = undefined;

  const dispatch = createEventDispatcher();

  let name: string = '';
  let target: string = '';
  let token: string = '';

  function handleClose() {
    if (onClose) onClose();
    dispatch('close');
  }

  function handleSubmit() {
    if (!name.trim() || !target.trim() || !token.trim()) return;
    const payload = {
      name: name.trim(),
      target: target.trim(),
      token: token.trim(),
    };
    if (onSubmit) onSubmit(payload);
    dispatch('submit', payload);
    name = '';
    target = '';
    token = '';
  }

  function handleKeydown(e: KeyboardEvent) {
    if (open && e.key === 'Escape') {
      e.preventDefault();
      handleClose();
    }
  }
</script>

<svelte:window on:keydown={handleKeydown} />

{#if open}
  <div class="modal-overlay" role="presentation" on:click|self={handleClose} on:keydown={(e) => e.key === 'Escape' && handleClose()}>
    <div class="modal-box">
      <div class="modal-header">
        <h3 class="modal-title">添加 Worker 节点</h3>
        <button class="btn-icon close-btn" on:click={handleClose}>&times;</button>
      </div>

      <form on:submit|preventDefault={handleSubmit}>
        <div class="modal-body">
          <div class="form-group">
            <label for="add-node-name" class="form-label">节点名称 *</label>
            <input
              id="add-node-name"
              type="text"
              class="input w-full"
              bind:value={name}
              placeholder="例如：DESKTOP-4090 或 worker-lab"
              required
            />
          </div>

          <div class="form-group">
            <label for="add-node-target" class="form-label">目标地址 (计算机名 / IP:端口) *</label>
            <input
              id="add-node-target"
              type="text"
              class="input w-full mono"
              bind:value={target}
              placeholder="例如：DESKTOP-4090 或 192.168.1.105:19000"
              required
            />
            <div class="form-hint">支持计算机名、IP 或 Tailscale，缺省端口 19000</div>
          </div>

          <div class="form-group">
            <label for="add-node-token" class="form-label">安全认证 Token *</label>
            <input
              id="add-node-token"
              type="password"
              class="input w-full mono"
              bind:value={token}
              placeholder="输入远端 Worker 的安全 Token"
              required
            />
            <div class="form-hint">远端节点运行 'cw show' 获取 Token</div>
          </div>
        </div>

        <div class="modal-footer">
          <button type="button" class="btn btn-secondary" on:click={handleClose}>取消</button>
          <button type="submit" class="btn btn-primary" disabled={!name.trim() || !target.trim() || !token.trim()}>保存</button>
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
  .w-full {
    width: 100%;
  }
  .mono {
    font-family: var(--font-mono);
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
