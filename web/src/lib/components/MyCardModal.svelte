<!-- web/src/lib/components/MyCardModal.svelte - 本机安全名片与配对命令 (无眼睛按钮，极简一键复制) -->
<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import { copyToClipboard } from '../utils/format';
  import type { LocalCardInfo } from '../types';

  export let open: boolean = false;
  export let card: LocalCardInfo | null = null;

  const dispatch = createEventDispatcher();
  let tokenCopied = false;
  let cmdCopied = false;

  $: hostname = card?.name || '-';
  $: token = card?.token || '';
  $: pairingCmd = token ? `cw node add ${hostname} --token ${token}` : `cw node add ${hostname}`;

  function handleClose() {
    dispatch('close');
  }

  async function copyToken() {
    if (!token) return;
    const ok = await copyToClipboard(token);
    if (ok) {
      tokenCopied = true;
      setTimeout(() => tokenCopied = false, 2000);
    }
  }

  async function copyPairingCmd() {
    const ok = await copyToClipboard(pairingCmd);
    if (ok) {
      cmdCopied = true;
      setTimeout(() => cmdCopied = false, 2000);
    }
  }
</script>

{#if open}
  <div class="modal-overlay" on:click|self={handleClose}>
    <div class="modal-box">
      <div class="modal-header">
        <h3 class="modal-title">🔑 本机节点名片 (Identity)</h3>
        <button class="btn-icon close-btn" on:click={handleClose}>&times;</button>
      </div>

      <div class="modal-body">
        <div class="meta-card">
          <div class="meta-row">
            <span class="meta-label">本机节点标识:</span>
            <span class="meta-val highlight">{hostname}</span>
          </div>
          <div class="meta-row">
            <span class="meta-label">默认服务端口:</span>
            <span class="meta-val mono">19000</span>
          </div>
        </div>

        <div class="form-group">
          <label for="my-card-token" class="form-label">安全认证 Token (Ed25519)</label>
          <div class="input-with-btn">
            <input
              id="my-card-token"
              type="text"
              class="input mono flex-1"
              value={token ? (token.length > 20 ? token.slice(0, 16) + '••••••••' : token) : '未生成/未配置'}
              readonly
            />
            <button class="btn btn-secondary btn-sm" type="button" on:click={copyToken} disabled={!token}>
              {tokenCopied ? '✔ 已复制' : '复制 Token'}
            </button>
          </div>
        </div>

        <div class="form-group">
          <label for="my-card-cmd" class="form-label">远端机器一键配对命令</label>
          <div class="input-with-btn">
            <input
              id="my-card-cmd"
              type="text"
              class="input mono flex-1"
              value={pairingCmd}
              readonly
            />
            <button class="btn btn-primary btn-sm" type="button" on:click={copyPairingCmd}>
              {cmdCopied ? '✔ 已复制' : '复制命令'}
            </button>
          </div>
          <span class="hint-text">在其他控制机终端粘贴该命令，即可瞬间纳管本机。</span>
        </div>
      </div>

      <div class="modal-footer">
        <button type="button" class="btn btn-secondary" on:click={handleClose}>关闭</button>
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
    max-width: 520px;
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
    gap: 14px;
  }
  .meta-card {
    background: var(--bg-elevated);
    border-radius: 6px;
    padding: 10px 14px;
    display: flex;
    flex-direction: column;
    gap: 6px;
    font-size: 13px;
  }
  .meta-row {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .meta-label {
    color: var(--text-muted);
    width: 100px;
  }
  .meta-val {
    color: var(--text-main);
  }
  .highlight {
    font-weight: 600;
    color: var(--primary);
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
  .input-with-btn {
    display: flex;
    gap: 8px;
  }
  .flex-1 {
    flex: 1;
  }
  .mono {
    font-family: var(--font-mono);
    font-size: 12px;
  }
  .hint-text {
    font-size: 11px;
    color: var(--text-dim);
  }
  .modal-footer {
    display: flex;
    justify-content: flex-end;
    padding: 12px 18px;
    background: var(--bg-elevated);
    border-top: 1px solid var(--border-subtle);
  }
</style>
