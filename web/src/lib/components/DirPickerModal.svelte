<!-- web/src/lib/components/DirPickerModal.svelte - 跨节点物理工作目录选择模态框 (支持盘符切换、文件夹逐级下钻、路径手动输入与回填) -->
<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import { Folder, FolderUp, RefreshCw, X, HardDrive } from 'lucide-svelte';
  import { listDir, getRoots } from '../api';
  import type { FileItem } from '../types';

  export let open: boolean = false;
  export let node: string = '';
  export let initialPath: string = '';
  export let onSelect: ((path: string) => void) | undefined = undefined;
  export let onClose: (() => void) | undefined = undefined;

  const dispatch = createEventDispatcher();

  let currentPath: string = 'C:/';
  let pathInput: string = 'C:/';
  let roots: string[] = ['C:/', 'D:/', 'E:/'];
  let dirs: FileItem[] = [];
  let loading: boolean = false;
  let error: string | null = null;
  let selectedDir: string | null = null;

  // 当模态框打开时，加载对应节点的盘符与目录
  $: if (open) {
    initPicker();
  }

  async function initPicker() {
    selectedDir = null;
    error = null;
    let targetPath = initialPath && initialPath.trim() ? initialPath.trim() : '';

    try {
      const fetchedRoots = await getRoots(node);
      if (fetchedRoots && fetchedRoots.length > 0) {
        roots = fetchedRoots;
        if (!targetPath) {
          targetPath = roots[0];
        }
      }
    } catch {
      // 容错使用默认盘符
    }

    if (!targetPath) {
      targetPath = 'C:/';
    }
    await loadDirectory(targetPath);
  }

  async function loadDirectory(path: string) {
    loading = true;
    error = null;
    selectedDir = null;
    try {
      const raw = await listDir(node, path);
      // 仅保留目录项并按名称排序
      dirs = raw
        .filter(item => item.is_dir)
        .sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' }));
      currentPath = path;
      pathInput = path;
    } catch (e: any) {
      error = e.message || String(e);
      dirs = [];
    } finally {
      loading = false;
    }
  }

  function handleUp() {
    const trimmed = currentPath.replace(/[\\/]+$/, '');
    const lastSlash = Math.max(trimmed.lastIndexOf('/'), trimmed.lastIndexOf('\\'));
    if (lastSlash > 0) {
      let next = trimmed.slice(0, lastSlash);
      if (/^[A-Za-z]:$/.test(next)) next += '/';
      loadDirectory(next);
    } else if (lastSlash === 0) {
      loadDirectory('/');
    } else if (/^[A-Za-z]:\/?$/.test(trimmed)) {
      // 已经在顶层盘符
    } else {
      loadDirectory('C:/');
    }
  }

  function enterDir(dirName: string) {
    const clean = currentPath.replace(/[\\/]+$/, '');
    const nextPath = clean ? `${clean}/${dirName}` : dirName;
    loadDirectory(nextPath);
  }

  function handlePathSubmit() {
    if (!pathInput.trim()) return;
    loadDirectory(pathInput.trim());
  }

  function handleConfirm() {
    let finalPath = currentPath;
    if (selectedDir) {
      const clean = currentPath.replace(/[\\/]+$/, '');
      finalPath = clean ? `${clean}/${selectedDir}` : selectedDir;
    }
    // Windows 根盘符规整化
    if (/^[A-Za-z]:$/.test(finalPath)) {
      finalPath += '/';
    }
    if (onSelect) onSelect(finalPath);
    dispatch('select', { path: finalPath });
    handleClose();
  }

  function handleClose() {
    if (onClose) onClose();
    dispatch('close');
  }
</script>

{#if open}
  <div class="modal-overlay" role="presentation" on:click|self={handleClose} on:keydown={(e) => e.key === 'Escape' && handleClose()}>
    <div class="dir-picker-box">
      <div class="modal-header">
        <div class="header-title-group">
          <HardDrive size={16} color="var(--primary)" />
          <h3 class="modal-title">选择工作目录 — {node ? `节点 [${node}]` : '本地节点'}</h3>
        </div>
        <button class="btn-icon close-btn" type="button" on:click={handleClose}>
          <X size={16} />
        </button>
      </div>

      <div class="picker-body">
        <!-- 盘符快捷切换 -->
        <div class="drives-row">
          <span class="drives-label">盘符:</span>
          <div class="drive-chips">
            {#each roots as root, idx (root + '-' + idx)}
              <button
                type="button"
                class="drive-chip {currentPath.startsWith(root.replace('/', '')) ? 'active' : ''}"
                on:click={() => loadDirectory(root)}
              >
                {root}
              </button>
            {/each}
          </div>
        </div>

        <!-- 路径导航与搜索栏 -->
        <div class="nav-bar">
          <button class="btn btn-secondary btn-sm nav-icon-btn" type="button" on:click={handleUp} title="返回上一级">
            <FolderUp size={14} />
          </button>
          <form class="path-form" on:submit|preventDefault={handlePathSubmit}>
            <input
              type="text"
              class="input path-input mono"
              bind:value={pathInput}
              placeholder="输入物理路径并按回车..."
            />
          </form>
          <button class="btn btn-secondary btn-sm nav-icon-btn" type="button" on:click={() => loadDirectory(currentPath)} title="刷新">
            <RefreshCw size={13} class={loading ? 'spinning' : ''} />
          </button>
        </div>

        <!-- 目录列表区域 -->
        <div class="dir-list-wrap">
          {#if loading && dirs.length === 0}
            <div class="picker-state muted">正在读取目录清单...</div>
          {:else if error}
            <div class="picker-state error">
              <span>读取失败: {error}</span>
              <button class="btn btn-secondary btn-xs" type="button" on:click={() => loadDirectory(currentPath)}>重试</button>
            </div>
          {:else}
            <div class="dir-table-wrap">
              <table class="dir-table">
                <thead>
                  <tr>
                    <th style="width: 28px;"></th>
                    <th>文件夹名称</th>
                    <th style="width: 130px; text-align: right;">修改时间</th>
                  </tr>
                </thead>
                <tbody>
                  {#each dirs as item (item.name)}
                    <tr
                      class="dir-row {selectedDir === item.name ? 'selected' : ''}"
                      on:click={() => selectedDir = item.name}
                      on:dblclick={() => enterDir(item.name)}
                    >
                      <td class="dir-icon-cell">
                        <Folder size={15} color="var(--primary)" />
                      </td>
                      <td class="dir-name-cell">
                        <span class="dir-name-text">{item.name}</span>
                      </td>
                      <td class="dir-modtime-cell mono muted">
                        {item.mod_time ? item.mod_time.slice(0, 19).replace('T', ' ') : '-'}
                      </td>
                    </tr>
                  {/each}
                  {#if dirs.length === 0}
                    <tr>
                      <td colspan="3" class="empty-dirs muted">该目录下没有子文件夹</td>
                    </tr>
                  {/if}
                </tbody>
              </table>
            </div>
          {/if}
        </div>
      </div>

      <!-- 底部确认与取消 -->
      <div class="modal-footer">
        <div class="current-selection mono text-sm" title={selectedDir ? `${currentPath.replace(/[\\/]+$/, '')}/${selectedDir}` : currentPath}>
          <span class="muted">当前选择:</span>
          <span class="path-display font-semibold">
            {selectedDir ? `${currentPath.replace(/[\\/]+$/, '')}/${selectedDir}` : currentPath}
          </span>
        </div>
        <div class="footer-actions">
          <button type="button" class="btn btn-secondary" on:click={handleClose}>取消</button>
          <button type="button" class="btn btn-primary" on:click={handleConfirm}>确定选择</button>
        </div>
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
    z-index: 1100;
  }
  .dir-picker-box {
    background: var(--bg-surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    width: 90%;
    max-width: 600px;
    height: 480px;
    box-shadow: var(--shadow-lg);
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }
  .modal-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 12px 16px;
    border-bottom: 1px solid var(--border-subtle);
    flex-shrink: 0;
  }
  .header-title-group {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .modal-title {
    font-size: 14px;
    font-weight: 600;
    margin: 0;
    color: var(--text-main);
  }
  .close-btn {
    background: transparent;
    border: none;
    color: var(--text-dim);
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 4px;
    border-radius: 4px;
  }
  .close-btn:hover {
    background: var(--bg-hover);
    color: var(--text-main);
  }
  .picker-body {
    flex: 1;
    display: flex;
    flex-direction: column;
    padding: 12px 16px;
    gap: 10px;
    min-height: 0;
  }
  .drives-row {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-shrink: 0;
  }
  .drives-label {
    font-size: 12px;
    color: var(--text-dim);
  }
  .drive-chips {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
  }
  .drive-chip {
    padding: 2px 8px;
    font-size: 11px;
    font-family: var(--font-mono);
    border: 1px solid var(--border);
    border-radius: 4px;
    background: var(--bg-surface);
    color: var(--text-main);
    cursor: pointer;
    transition: all 0.15s ease;
  }
  .drive-chip:hover {
    background: var(--bg-hover);
  }
  .drive-chip.active {
    background: var(--primary-subtle);
    border-color: var(--primary);
    color: var(--primary);
    font-weight: 600;
  }
  .nav-bar {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-shrink: 0;
  }
  .nav-icon-btn {
    padding: 4px 8px;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .path-form {
    flex: 1;
  }
  .path-input {
    width: 100%;
    font-size: 12px;
    padding: 4px 8px;
    box-sizing: border-box;
  }
  .dir-list-wrap {
    flex: 1;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--bg-surface-raised, var(--bg-surface));
    overflow: hidden;
    position: relative;
    display: flex;
    flex-direction: column;
    min-height: 0;
  }
  .dir-table-wrap {
    flex: 1;
    overflow-y: auto;
  }
  .dir-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
  }
  .dir-table th {
    position: sticky;
    top: 0;
    background: var(--bg-surface);
    padding: 6px 10px;
    text-align: left;
    color: var(--text-dim);
    font-size: 11px;
    font-weight: 500;
    border-bottom: 1px solid var(--border-subtle);
    z-index: 1;
  }
  .dir-row {
    cursor: pointer;
    border-bottom: 1px solid var(--border-subtle);
    transition: background 0.1s ease;
  }
  .dir-row:hover {
    background: var(--bg-hover);
  }
  .dir-row.selected {
    background: var(--primary-subtle) !important;
  }
  .dir-icon-cell {
    padding: 6px 4px 6px 10px;
    vertical-align: middle;
    width: 24px;
  }
  .dir-name-cell {
    padding: 6px 10px;
    vertical-align: middle;
    color: var(--text-main);
  }
  .dir-name-text {
    font-weight: 500;
  }
  .dir-modtime-cell {
    padding: 6px 10px;
    text-align: right;
    font-size: 11px;
    vertical-align: middle;
  }
  .empty-dirs,
  .picker-state {
    padding: 40px 16px;
    text-align: center;
    font-size: 12px;
  }
  .picker-state.error {
    color: var(--danger);
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 8px;
  }
  .modal-footer {
    padding: 10px 16px;
    border-top: 1px solid var(--border-subtle);
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    flex-shrink: 0;
  }
  .current-selection {
    display: flex;
    align-items: center;
    gap: 6px;
    overflow: hidden;
    max-width: 60%;
  }
  .path-display {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--text-main);
  }
  .footer-actions {
    display: flex;
    gap: 8px;
    flex-shrink: 0;
  }
  :global(.spinning) {
    animation: spin 1s linear infinite;
  }
  @keyframes spin {
    from { transform: rotate(0deg); }
    to { transform: rotate(360deg); }
  }
  .mono {
    font-family: var(--font-mono);
  }
  .text-sm {
    font-size: 12px;
  }
  .muted {
    color: var(--text-dim);
  }
  .font-semibold {
    font-weight: 600;
  }
  .btn-xs {
    padding: 2px 8px;
    font-size: 11px;
  }
</style>
