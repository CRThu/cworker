<!-- web/src/lib/components/DualPaneFiles.svelte - FileZilla 风格左右双栏文件管理器 (双向拖拽互传、右键完整操作、目录置顶与盘符切换) -->
<script lang="ts">
  import { onMount } from 'svelte';
  import {
    Folder,
    FileText,
    ArrowRight,
    ArrowLeft,
    FolderUp,
    Download,
    FolderPlus,
    Trash2,
    RefreshCw,
    X,
  } from 'lucide-svelte';
  import { listDir, transfer, getRoots, makeDir, removePath } from '../api';
  import { formatBytes } from '../utils/format';
  import type { NodeInfo, FileItem, TransferProgress } from '../types';

  export let nodes: NodeInfo[] = [];

  // 左栏状态
  let leftNode: string = '';
  let leftPath: string = 'C:/';
  let leftFiles: FileItem[] = [];
  let leftRoots: string[] = ['C:/', 'D:/', 'E:/'];
  let leftLoading: boolean = false;
  let leftSelected: string | null = null;
  let leftError: string | null = null;

  // 右栏状态
  let rightNode: string = '';
  let rightPath: string = 'C:/';
  let rightFiles: FileItem[] = [];
  let rightRoots: string[] = ['C:/', 'D:/', 'E:/'];
  let rightLoading: boolean = false;
  let rightSelected: string | null = null;
  let rightError: string | null = null;

  // 拖拽传输状态
  let draggedItem: { pane: 'left' | 'right'; file: FileItem } | null = null;
  let dragOverPane: 'left' | 'right' | null = null;

  // 互传进度与状态
  let progress: TransferProgress = {
    active: false,
    statusText: '',
    percent: 0,
    finished: false,
  };

  // 新建文件夹：直接在文件夹列表顶部行内编辑创建 (符合 Windows/VSCode 现代文件管理器交互)
  let creatingFolderPane: 'left' | 'right' | null = null;
  let inlineFolderName: string = '';

  // 原地二次确认删除状态 (零弹窗轻量交互，行内/工具栏就地确认)
  let confirmingDelete: { pane: 'left' | 'right'; file: FileItem } | null = null;

  // 右键上下文菜单状态
  let contextMenu: {
    visible: boolean;
    x: number;
    y: number;
    pane: 'left' | 'right';
    file: FileItem | null;
  } = { visible: false, x: 0, y: 0, pane: 'left', file: null };

  $: leftSelectedFile = leftFiles.find(f => f.name === leftSelected) || null;
  $: rightSelectedFile = rightFiles.find(f => f.name === rightSelected) || null;

  // 文件夹严格置顶排序：目录第一，文件第二，组内名称升序
  function sortFiles(items: FileItem[]): FileItem[] {
    return [...items].sort((a, b) => {
      if (a.is_dir && !b.is_dir) return -1;
      if (!a.is_dir && b.is_dir) return 1;
      return a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' });
    });
  }

  onMount(() => {
    const onlineRemote = nodes.find(n => n.status === 'ONLINE');
    if (onlineRemote) {
      rightNode = onlineRemote.name;
    }
    loadRoots('left', leftNode);
    loadRoots('right', rightNode);
    loadLeft(leftNode, leftPath);
    loadRight(rightNode, rightPath);

    const onWindowClick = () => {
      if (contextMenu.visible) contextMenu.visible = false;
    };
    const onWindowKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        if (contextMenu.visible) contextMenu.visible = false;
        if (creatingFolderPane) creatingFolderPane = null;
        if (confirmingDelete) confirmingDelete = null;
      }
    };
    window.addEventListener('click', onWindowClick);
    window.addEventListener('keydown', onWindowKeyDown);
    return () => {
      window.removeEventListener('click', onWindowClick);
      window.removeEventListener('keydown', onWindowKeyDown);
    };
  });

  async function loadRoots(pane: 'left' | 'right', node: string) {
    try {
      const roots = await getRoots(node);
      if (roots && roots.length > 0) {
        if (pane === 'left') {
          leftRoots = roots;
        } else {
          rightRoots = roots;
        }
      }
    } catch {
      // 容错使用默认盘符
    }
  }

  async function loadLeft(node: string, path: string) {
    leftLoading = true;
    leftError = null;
    leftSelected = null;
    try {
      const raw = await listDir(node, path);
      leftFiles = sortFiles(raw);
      leftPath = path;
    } catch (e: any) {
      if (path !== '.') {
        try {
          const raw = await listDir(node, '.');
          leftFiles = sortFiles(raw);
          leftPath = '.';
          return;
        } catch {
          // 容错
        }
      }
      leftError = e.message || String(e);
      leftFiles = [];
    } finally {
      leftLoading = false;
    }
  }

  async function loadRight(node: string, path: string) {
    rightLoading = true;
    rightError = null;
    rightSelected = null;
    try {
      const raw = await listDir(node, path);
      rightFiles = sortFiles(raw);
      rightPath = path;
    } catch (e: any) {
      if (path !== '.') {
        try {
          const raw = await listDir(node, '.');
          rightFiles = sortFiles(raw);
          rightPath = '.';
          return;
        } catch {
          // 容错
        }
      }
      rightError = e.message || String(e);
      rightFiles = [];
    } finally {
      rightLoading = false;
    }
  }

  function handleLeftDrill(file: FileItem) {
    if (!file.is_dir) return;
    const clean = leftPath.replace(/[\\/]+$/, '');
    const nextPath = clean ? `${clean}/${file.name}` : file.name;
    loadLeft(leftNode, nextPath);
  }

  function handleRightDrill(file: FileItem) {
    if (!file.is_dir) return;
    const clean = rightPath.replace(/[\\/]+$/, '');
    const nextPath = clean ? `${clean}/${file.name}` : file.name;
    loadRight(rightNode, nextPath);
  }

  function handleLeftUp() {
    const trimmed = leftPath.replace(/[\\/]+$/, '');
    const lastSlash = Math.max(trimmed.lastIndexOf('/'), trimmed.lastIndexOf('\\'));
    if (lastSlash > 0) {
      let next = trimmed.slice(0, lastSlash);
      if (/^[A-Za-z]:$/.test(next)) next += '/';
      loadLeft(leftNode, next);
    } else if (lastSlash === 0) {
      loadLeft(leftNode, '/');
    } else if (/^[A-Za-z]:\/?$/.test(trimmed)) {
      // 盘符顶层
    } else {
      loadLeft(leftNode, 'C:/');
    }
  }

  function handleRightUp() {
    const trimmed = rightPath.replace(/[\\/]+$/, '');
    const lastSlash = Math.max(trimmed.lastIndexOf('/'), trimmed.lastIndexOf('\\'));
    if (lastSlash > 0) {
      let next = trimmed.slice(0, lastSlash);
      if (/^[A-Za-z]:$/.test(next)) next += '/';
      loadRight(rightNode, next);
    } else if (lastSlash === 0) {
      loadRight(rightNode, '/');
    } else if (/^[A-Za-z]:\/?$/.test(trimmed)) {
      // 盘符顶层
    } else {
      loadRight(rightNode, 'C:/');
    }
  }

  function handleDownload(node: string, basePath: string, fileName: string) {
    const cleanBase = basePath.replace(/[\\/]+$/, '');
    const filePath = cleanBase ? `${cleanBase}/${fileName}` : fileName;
    const query = new URLSearchParams({ node, path: filePath });
    window.open(`/api/ui/fs/download?${query.toString()}`, '_blank');
  }

  // 跨栏传输: 左 -> 右
  async function transferLeftToRight(file?: FileItem) {
    const item = file || leftFiles.find(f => f.name === leftSelected);
    if (!item) return;
    const cleanSrc = leftPath.replace(/[\\/]+$/, '');
    let cleanDst = rightPath.replace(/[\\/]+$/, '') || '.';
    if (/^[A-Za-z]:$/.test(cleanDst)) cleanDst += '/';
    const srcPath = cleanSrc ? `${cleanSrc}/${item.name}` : item.name;
    const dstPath = cleanDst;

    await executeTransfer(leftNode, srcPath, rightNode, dstPath, item.is_dir, 'left-to-right');
  }

  // 跨栏传输: 右 -> 左
  async function transferRightToLeft(file?: FileItem) {
    const item = file || rightFiles.find(f => f.name === rightSelected);
    if (!item) return;
    const cleanSrc = rightPath.replace(/[\\/]+$/, '');
    let cleanDst = leftPath.replace(/[\\/]+$/, '') || '.';
    if (/^[A-Za-z]:$/.test(cleanDst)) cleanDst += '/';
    const srcPath = cleanSrc ? `${cleanSrc}/${item.name}` : item.name;
    const dstPath = cleanDst;

    await executeTransfer(rightNode, srcPath, leftNode, dstPath, item.is_dir, 'right-to-left');
  }

  async function executeTransfer(
    srcNode: string,
    srcPath: string,
    dstNode: string,
    dstPath: string,
    recursive: boolean,
    direction: 'left-to-right' | 'right-to-left'
  ) {
    progress = {
      active: true,
      statusText: '正在准备传输...',
      percent: 0,
      finished: false,
    };

    try {
      await transfer(
        {
          src_node: srcNode,
          src_path: srcPath,
          dst_node: dstNode,
          dst_path: dstPath,
          recursive,
          concurrency: 8,
        },
        (frame) => {
          if (frame.type === 'progress') {
            const filesText = frame.total_files && frame.total_files > 1
              ? ` (${frame.completed_files ?? 0}/${frame.total_files} 项)`
              : '';
            const sizeText = frame.total_bytes && frame.total_bytes > 0
              ? ` · ${formatBytes(frame.transferred_bytes || 0)} / ${formatBytes(frame.total_bytes)}`
              : '';
            const speedText = frame.speed_bps && frame.speed_bps > 0
              ? ` · ${formatBytes(frame.speed_bps)}/s`
              : '';

            progress = {
              ...progress,
              active: true,
              percent: frame.percent ?? progress.percent,
              statusText: `正在传输${filesText}${sizeText}${speedText}`,
            };
          } else if (frame.type === 'done') {
            progress = {
              ...progress,
              percent: 100,
              finished: true,
              statusText: '传输完成',
            };
          }
        }
      );

      progress = {
        ...progress,
        active: true,
        statusText: '传输完成',
        percent: 100,
        finished: true,
      };

      if (direction === 'left-to-right') {
        loadRight(rightNode, rightPath);
      } else {
        loadLeft(leftNode, leftPath);
      }

      setTimeout(() => {
        if (progress.finished && !progress.error) {
          progress.active = false;
        }
      }, 3000);
    } catch (e: any) {
      progress = {
        active: true,
        statusText: `传输失败: ${e.message || String(e)}`,
        percent: 100,
        finished: true,
        error: e.message,
      };
    }
  }

  // 拖拽交互处理
  function handleDragStart(e: DragEvent, pane: 'left' | 'right', file: FileItem) {
    draggedItem = { pane, file };
    if (e.dataTransfer) {
      e.dataTransfer.setData('text/plain', file.name);
      e.dataTransfer.effectAllowed = 'copyMove';
    }
  }

  function handleDragOver(e: DragEvent, pane: 'left' | 'right') {
    if (!draggedItem || draggedItem.pane === pane) return;
    e.preventDefault();
    if (e.dataTransfer) {
      e.dataTransfer.dropEffect = 'copy';
    }
    dragOverPane = pane;
  }

  async function handleDrop(e: DragEvent, targetPane: 'left' | 'right') {
    e.preventDefault();
    dragOverPane = null;
    if (!draggedItem || draggedItem.pane === targetPane) return;
    const file = draggedItem.file;
    draggedItem = null;

    if (targetPane === 'right') {
      await transferLeftToRight(file);
    } else {
      await transferRightToLeft(file);
    }
  }

  // 新建文件夹 (文件夹列表内快捷创建)
  function startInlineMkdir(pane: 'left' | 'right') {
    creatingFolderPane = pane;
    inlineFolderName = '新文件夹';
  }

  function cancelInlineMkdir() {
    creatingFolderPane = null;
    inlineFolderName = '';
  }

  async function confirmInlineMkdir(pane: 'left' | 'right') {
    if (!inlineFolderName.trim()) return;
    const node = pane === 'left' ? leftNode : rightNode;
    const basePath = pane === 'left' ? leftPath : rightPath;
    const cleanBase = basePath.replace(/[\\/]+$/, '');
    const fullPath = cleanBase ? `${cleanBase}/${inlineFolderName.trim()}` : inlineFolderName.trim();

    try {
      await makeDir(node, fullPath);
      creatingFolderPane = null;
      inlineFolderName = '';
      if (pane === 'left') {
        await loadLeft(leftNode, leftPath);
      } else {
        await loadRight(rightNode, rightPath);
      }
    } catch (e: any) {
      alert(`创建文件夹失败: ${e.message || String(e)}`);
    }
  }

  // 原地二次确认删除执行 (零弹窗，行内/工具栏就地确认)
  async function executeDelete(pane: 'left' | 'right', file: FileItem) {
    confirmingDelete = null;
    const node = pane === 'left' ? leftNode : rightNode;
    const basePath = pane === 'left' ? leftPath : rightPath;
    const cleanBase = basePath.replace(/[\\/]+$/, '');
    const fullPath = cleanBase ? `${cleanBase}/${file.name}` : file.name;

    try {
      await removePath(node, fullPath, file.is_dir);
      if (pane === 'left') {
        if (leftSelected === file.name) leftSelected = null;
        await loadLeft(leftNode, leftPath);
      } else {
        if (rightSelected === file.name) rightSelected = null;
        await loadRight(rightNode, rightPath);
      }
    } catch (e: any) {
      alert(`删除失败: ${e.message || String(e)}`);
    }
  }

  // 右键上下文菜单交互
  function handleContextMenu(e: MouseEvent, pane: 'left' | 'right', file: FileItem | null) {
    e.preventDefault();
    e.stopPropagation();
    contextMenu = {
      visible: true,
      x: e.clientX,
      y: e.clientY,
      pane,
      file,
    };
  }
</script>

<div class="dual-pane-container">
  <!-- 左右双栏文件管理器布局 -->
  <div class="panes-grid">
    <!-- 左栏 (Panel A) -->
    <div
      class="file-pane {dragOverPane === 'left' ? 'drag-over' : ''}"
      role="region"
      aria-label="左侧文件管理器"
      on:contextmenu={(e) => handleContextMenu(e, 'left', null)}
      on:dragover={(e) => handleDragOver(e, 'left')}
      on:dragleave={() => dragOverPane = null}
      on:drop={(e) => handleDrop(e, 'left')}
    >
      <div class="pane-header">
        <div class="pane-top-row">
          <label class="pane-label" for="left-node-select">节点:</label>
          <select
            id="left-node-select"
            class="select pane-select"
            bind:value={leftNode}
            on:change={() => {
              loadRoots('left', leftNode);
              loadLeft(leftNode, leftPath);
            }}
          >
            <option value="">本地</option>
            {#each nodes as n}
              <option value={n.name}>{n.name} ({n.status})</option>
            {/each}
          </select>

          <!-- 盘符快捷入口 -->
          <div class="drive-chips">
            {#each leftRoots as root, idx (root + '-' + idx)}
              <button
                type="button"
                class="drive-chip {leftPath.startsWith(root.replace('/', '')) ? 'active' : ''}"
                on:click={() => loadLeft(leftNode, root)}
              >
                {root}
              </button>
            {/each}
          </div>
        </div>

        <div class="pane-path-row">
          <button class="btn btn-secondary btn-sm btn-nav-icon" on:click={handleLeftUp} title="返回上一级">
            <FolderUp size={14} />
          </button>
          <input
            type="text"
            class="input pane-path-input mono"
            bind:value={leftPath}
            on:keydown={(e) => e.key === 'Enter' && loadLeft(leftNode, leftPath)}
          />
          <button class="btn btn-secondary btn-sm btn-nav-icon" on:click={() => loadLeft(leftNode, leftPath)} title="刷新目录">
            <RefreshCw size={13} />
          </button>
          <div class="toolbar-sep"></div>
          <button class="btn btn-secondary btn-sm btn-toolbar-action" type="button" on:click={() => startInlineMkdir('left')} title="新建文件夹">
            <FolderPlus size={14} />
            <span>新建</span>
          </button>
        </div>
      </div>

      <div class="pane-body">
        {#if leftLoading}
          <div class="pane-status">扫描目录中...</div>
        {:else if leftError}
          <div class="pane-status text-danger">{leftError}</div>
        {:else}
          <div class="file-table-wrap">
            <table class="data-table">
              <thead>
                <tr>
                  <th style="width: 32px;"></th>
                  <th>名称</th>
                  <th style="width: 85px;">大小</th>
                  <th style="text-align: right; width: 110px;">操作</th>
                </tr>
              </thead>
              <tbody>
                {#if creatingFolderPane === 'left'}
                  <tr class="inline-mkdir-row">
                    <td class="file-icon">
                      <Folder size={15} color="var(--primary)" />
                    </td>
                    <td>
                      <input
                        type="text"
                        class="input inline-mkdir-input mono"
                        bind:value={inlineFolderName}
                        placeholder="文件夹名称..."
                        on:keydown={(e) => {
                          if (e.key === 'Enter') {
                            e.preventDefault();
                            confirmInlineMkdir('left');
                          } else if (e.key === 'Escape') {
                            cancelInlineMkdir();
                          }
                        }}
                      />
                    </td>
                    <td class="mono text-sm muted">&lt;DIR&gt;</td>
                    <td class="action-cell" style="text-align: right;">
                      <div class="inline-mkdir-actions">
                        <button
                          class="btn btn-primary btn-xs"
                          type="button"
                          disabled={!inlineFolderName.trim()}
                          on:click={() => confirmInlineMkdir('left')}
                        >
                          确定
                        </button>
                        <button
                          class="btn btn-secondary btn-xs"
                          type="button"
                          on:click={cancelInlineMkdir}
                        >
                          取消
                        </button>
                      </div>
                    </td>
                  </tr>
                {/if}
                {#each leftFiles as f, idx (f.name + '-' + idx)}
                  <tr
                    class="file-row {leftSelected === f.name ? 'selected' : ''}"
                    draggable="true"
                    on:dragstart={(e) => handleDragStart(e, 'left', f)}
                    on:click={() => leftSelected = f.name}
                    on:contextmenu={(e) => handleContextMenu(e, 'left', f)}
                  >
                    <td class="file-icon">
                      {#if f.is_dir}
                        <Folder size={15} color="var(--primary)" />
                      {:else}
                        <FileText size={15} color="var(--text-dim)" />
                      {/if}
                    </td>
                    <td>
                      {#if f.is_dir}
                        <button
                          type="button"
                          class="dir-link"
                          on:click|stopPropagation={() => handleLeftDrill(f)}
                        >
                          {f.name}
                        </button>
                      {:else}
                        <span>{f.name}</span>
                      {/if}
                    </td>
                    <td class="mono text-sm muted">{f.is_dir ? '<DIR>' : formatBytes(f.size)}</td>
                    <td class="action-cell" style="text-align: right;">
                      {#if confirmingDelete && confirmingDelete.pane === 'left' && confirmingDelete.file.name === f.name}
                        <div class="inline-row-confirm">
                          <span class="inline-confirm-tip">确定？</span>
                          <button
                            class="btn btn-danger btn-xs"
                            type="button"
                            title="确定删除"
                            on:click|stopPropagation={() => executeDelete('left', f)}
                          >
                            确定
                          </button>
                          <button
                            class="btn btn-secondary btn-xs"
                            type="button"
                            title="取消"
                            on:click|stopPropagation={() => confirmingDelete = null}
                          >
                            取消
                          </button>
                        </div>
                      {:else}
                        <button
                          class="btn-action-icon"
                          type="button"
                          title="传到右侧"
                          on:click|stopPropagation={() => transferLeftToRight(f)}
                        >
                          <ArrowRight size={13} />
                        </button>
                        {#if !f.is_dir}
                          <button
                            class="btn-action-icon"
                            type="button"
                            title="下载"
                            on:click|stopPropagation={() => handleDownload(leftNode, leftPath, f.name)}
                          >
                            <Download size={13} />
                          </button>
                        {/if}
                        <button
                          class="btn-action-icon btn-danger-icon"
                          type="button"
                          title="删除"
                          on:click|stopPropagation={() => confirmingDelete = { pane: 'left', file: f }}
                        >
                          <Trash2 size={13} />
                        </button>
                      {/if}
                    </td>
                  </tr>
                {/each}
                {#if leftFiles.length === 0}
                  <tr class="empty-row">
                    <td colspan="4" class="empty-cell">目录为空</td>
                  </tr>
                {/if}
              </tbody>
            </table>
          </div>
        {/if}
      </div>

      <div class="pane-footer">
        <span>共 {leftFiles.length} 项</span>
        {#if leftSelected}
          <span class="selected-tag">已选: {leftSelected}</span>
        {/if}
      </div>
    </div>

    <!-- 右栏 (Panel B) -->
    <div
      class="file-pane {dragOverPane === 'right' ? 'drag-over' : ''}"
      role="region"
      aria-label="右侧文件管理器"
      on:contextmenu={(e) => handleContextMenu(e, 'right', null)}
      on:dragover={(e) => handleDragOver(e, 'right')}
      on:dragleave={() => dragOverPane = null}
      on:drop={(e) => handleDrop(e, 'right')}
    >
      <div class="pane-header">
        <div class="pane-top-row">
          <label class="pane-label" for="right-node-select">节点:</label>
          <select
            id="right-node-select"
            class="select pane-select"
            bind:value={rightNode}
            on:change={() => {
              loadRoots('right', rightNode);
              loadRight(rightNode, rightPath);
            }}
          >
            <option value="">本地</option>
            {#each nodes as n}
              <option value={n.name}>{n.name} ({n.status})</option>
            {/each}
          </select>

          <!-- 盘符快捷入口 -->
          <div class="drive-chips">
            {#each rightRoots as root, idx (root + '-' + idx)}
              <button
                type="button"
                class="drive-chip {rightPath.startsWith(root.replace('/', '')) ? 'active' : ''}"
                on:click={() => loadRight(rightNode, root)}
              >
                {root}
              </button>
            {/each}
          </div>
        </div>

        <div class="pane-path-row">
          <button class="btn btn-secondary btn-sm btn-nav-icon" on:click={handleRightUp} title="返回上一级">
            <FolderUp size={14} />
          </button>
          <input
            type="text"
            class="input pane-path-input mono"
            bind:value={rightPath}
            on:keydown={(e) => e.key === 'Enter' && loadRight(rightNode, rightPath)}
          />
          <button class="btn btn-secondary btn-sm btn-nav-icon" on:click={() => loadRight(rightNode, rightPath)} title="刷新目录">
            <RefreshCw size={13} />
          </button>
          <div class="toolbar-sep"></div>
          <button class="btn btn-secondary btn-sm btn-toolbar-action" type="button" on:click={() => startInlineMkdir('right')} title="新建文件夹">
            <FolderPlus size={14} />
            <span>新建</span>
          </button>
        </div>
      </div>

      <div class="pane-body">
        {#if rightLoading}
          <div class="pane-status">扫描目录中...</div>
        {:else if rightError}
          <div class="pane-status text-danger">{rightError}</div>
        {:else}
          <div class="file-table-wrap">
            <table class="data-table">
              <thead>
                <tr>
                  <th style="width: 32px;"></th>
                  <th>名称</th>
                  <th style="width: 85px;">大小</th>
                  <th style="text-align: right; width: 110px;">操作</th>
                </tr>
              </thead>
              <tbody>
                {#if creatingFolderPane === 'right'}
                  <tr class="inline-mkdir-row">
                    <td class="file-icon">
                      <Folder size={15} color="var(--primary)" />
                    </td>
                    <td>
                      <input
                        type="text"
                        class="input inline-mkdir-input mono"
                        bind:value={inlineFolderName}
                        placeholder="文件夹名称..."
                        on:keydown={(e) => {
                          if (e.key === 'Enter') {
                            e.preventDefault();
                            confirmInlineMkdir('right');
                          } else if (e.key === 'Escape') {
                            cancelInlineMkdir();
                          }
                        }}
                      />
                    </td>
                    <td class="mono text-sm muted">&lt;DIR&gt;</td>
                    <td class="action-cell" style="text-align: right;">
                      <div class="inline-mkdir-actions">
                        <button
                          class="btn btn-primary btn-xs"
                          type="button"
                          disabled={!inlineFolderName.trim()}
                          on:click={() => confirmInlineMkdir('right')}
                        >
                          确定
                        </button>
                        <button
                          class="btn btn-secondary btn-xs"
                          type="button"
                          on:click={cancelInlineMkdir}
                        >
                          取消
                        </button>
                      </div>
                    </td>
                  </tr>
                {/if}
                {#each rightFiles as f, idx (f.name + '-' + idx)}
                  <tr
                    class="file-row {rightSelected === f.name ? 'selected' : ''}"
                    draggable="true"
                    on:dragstart={(e) => handleDragStart(e, 'right', f)}
                    on:click={() => rightSelected = f.name}
                    on:contextmenu={(e) => handleContextMenu(e, 'right', f)}
                  >
                    <td class="file-icon">
                      {#if f.is_dir}
                        <Folder size={15} color="var(--primary)" />
                      {:else}
                        <FileText size={15} color="var(--text-dim)" />
                      {/if}
                    </td>
                    <td>
                      {#if f.is_dir}
                        <button
                          type="button"
                          class="dir-link"
                          on:click|stopPropagation={() => handleRightDrill(f)}
                        >
                          {f.name}
                        </button>
                      {:else}
                        <span>{f.name}</span>
                      {/if}
                    </td>
                    <td class="mono text-sm muted">{f.is_dir ? '<DIR>' : formatBytes(f.size)}</td>
                    <td class="action-cell" style="text-align: right;">
                      {#if confirmingDelete && confirmingDelete.pane === 'right' && confirmingDelete.file.name === f.name}
                        <div class="inline-row-confirm">
                          <span class="inline-confirm-tip">确定？</span>
                          <button
                            class="btn btn-danger btn-xs"
                            type="button"
                            title="确定删除"
                            on:click|stopPropagation={() => executeDelete('right', f)}
                          >
                            确定
                          </button>
                          <button
                            class="btn btn-secondary btn-xs"
                            type="button"
                            title="取消"
                            on:click|stopPropagation={() => confirmingDelete = null}
                          >
                            取消
                          </button>
                        </div>
                      {:else}
                        <button
                          class="btn-action-icon"
                          type="button"
                          title="传到左侧"
                          on:click|stopPropagation={() => transferRightToLeft(f)}
                        >
                          <ArrowLeft size={13} />
                        </button>
                        {#if !f.is_dir}
                          <button
                            class="btn-action-icon"
                            type="button"
                            title="下载"
                            on:click|stopPropagation={() => handleDownload(rightNode, rightPath, f.name)}
                          >
                            <Download size={13} />
                          </button>
                        {/if}
                        <button
                          class="btn-action-icon btn-danger-icon"
                          type="button"
                          title="删除"
                          on:click|stopPropagation={() => confirmingDelete = { pane: 'right', file: f }}
                        >
                          <Trash2 size={13} />
                        </button>
                      {/if}
                    </td>
                  </tr>
                {/each}
                {#if rightFiles.length === 0}
                  <tr class="empty-row">
                    <td colspan="4" class="empty-cell">目录为空</td>
                  </tr>
                {/if}
              </tbody>
            </table>
          </div>
        {/if}
      </div>

      <div class="pane-footer">
        <span>共 {rightFiles.length} 项</span>
        {#if rightSelected}
          <span class="selected-tag">已选: {rightSelected}</span>
        {/if}
      </div>
    </div>
  </div>

  <!-- 底部传输与下载进度指示 (非阻塞流式) -->
  {#if progress.active}
    <div class="progress-banner {progress.error ? 'error' : (progress.finished ? 'success' : '')}">
      <div class="progress-info">
        <div class="progress-status-left">
          {#if !progress.finished && !progress.error}
            <span class="progress-pulse-dot"></span>
          {/if}
          <span>{progress.statusText}</span>
        </div>
        <div class="progress-actions">
          <span class="mono font-semibold">{progress.percent}%</span>
          <button
            class="btn-banner-close"
            type="button"
            title="关闭进度横幅"
            aria-label="关闭进度横幅"
            on:click={() => progress = { ...progress, active: false }}
          >
            <X size={13} />
          </button>
        </div>
      </div>
      <div class="progress-track">
        <div class="progress-fill" style="width: {progress.percent}%;"></div>
      </div>
    </div>
  {/if}
</div>

<!-- 右键上下文浮动菜单 -->
{#if contextMenu.visible}
  <div
    class="context-menu"
    style="top: {contextMenu.y}px; left: {contextMenu.x}px;"
    role="menu"
    tabindex="0"
    on:click|stopPropagation
    on:keydown={(e) => e.key === 'Escape' && (contextMenu.visible = false)}
  >
    {#if contextMenu.file}
      <button
        type="button"
        class="menu-item"
        on:click={() => {
          if (contextMenu.pane === 'left') transferLeftToRight(contextMenu.file!);
          else transferRightToLeft(contextMenu.file!);
          contextMenu.visible = false;
        }}
      >
        <ArrowRight size={13} />
        <span>发送至对侧</span>
      </button>

      {#if !contextMenu.file.is_dir}
        <button
          type="button"
          class="menu-item"
          on:click={() => {
            const node = contextMenu.pane === 'left' ? leftNode : rightNode;
            const path = contextMenu.pane === 'left' ? leftPath : rightPath;
            handleDownload(node, path, contextMenu.file!.name);
            contextMenu.visible = false;
          }}
        >
          <Download size={13} />
          <span>下载文件</span>
        </button>
      {/if}

      <button
        type="button"
        class="menu-item"
        on:click={() => {
          startInlineMkdir(contextMenu.pane);
          contextMenu.visible = false;
        }}
      >
        <FolderPlus size={13} />
        <span>新建文件夹</span>
      </button>

      <div class="menu-divider"></div>

      <button
        type="button"
        class="menu-item text-danger"
        on:click={() => {
          confirmingDelete = { pane: contextMenu.pane, file: contextMenu.file! };
          if (contextMenu.pane === 'left') leftSelected = contextMenu.file!.name;
          else rightSelected = contextMenu.file!.name;
          contextMenu.visible = false;
        }}
      >
        <Trash2 size={13} />
        <span>删除{contextMenu.file.is_dir ? '文件夹' : '文件'}</span>
      </button>
    {:else}
      <button
        type="button"
        class="menu-item"
        on:click={() => {
          startInlineMkdir(contextMenu.pane);
          contextMenu.visible = false;
        }}
      >
        <FolderPlus size={13} />
        <span>新建文件夹</span>
      </button>
      <button
        type="button"
        class="menu-item"
        on:click={() => {
          if (contextMenu.pane === 'left') loadLeft(leftNode, leftPath);
          else loadRight(rightNode, rightPath);
          contextMenu.visible = false;
        }}
      >
        <RefreshCw size={13} />
        <span>刷新目录</span>
      </button>
    {/if}
  </div>
{/if}

<style>
  .dual-pane-container {
    display: flex;
    flex-direction: column;
    gap: 10px;
    width: 100%;
    flex: 1;
    min-height: 0;
    height: 100%;
    overflow: hidden;
  }
  .progress-banner {
    background: var(--bg-surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 10px 14px;
    box-shadow: var(--shadow-sm);
    display: flex;
    flex-direction: column;
    gap: 6px;
    flex-shrink: 0;
  }
  .progress-banner.success {
    border-color: var(--success);
    background: var(--success-subtle);
  }
  .progress-banner.error {
    border-color: var(--danger);
    background: var(--danger-subtle);
  }
  .progress-info {
    display: flex;
    justify-content: space-between;
    align-items: center;
    font-size: 12px;
  }
  .progress-status-left {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .progress-pulse-dot {
    display: inline-block;
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--primary);
    box-shadow: 0 0 6px var(--primary);
    animation: pulse 1s infinite ease-in-out;
  }
  .progress-track {
    height: 5px;
    background: var(--bg-input);
    border-radius: 3px;
    overflow: hidden;
  }
  .progress-fill {
    height: 100%;
    background: var(--primary);
    border-radius: 3px;
    transition: width 0.2s ease;
  }
  .progress-banner.success .progress-fill {
    background: var(--success);
  }
  .progress-banner.error .progress-fill {
    background: var(--danger);
  }
  .progress-actions {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .btn-banner-close {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    background: transparent;
    border: none;
    border-radius: 4px;
    padding: 2px;
    color: var(--text-dim);
    cursor: pointer;
    transition: all 0.15s ease;
  }
  .btn-banner-close:hover {
    background: var(--bg-hover);
    color: var(--text-main);
  }
  .panes-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 14px;
    flex: 1;
    min-height: 0;
  }
  @media (max-width: 860px) {
    .panes-grid {
      grid-template-columns: 1fr;
    }
  }
  .file-pane {
    background: var(--bg-surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    display: flex;
    flex-direction: column;
    overflow: hidden;
    box-shadow: var(--shadow-sm);
    transition: border-color 0.2s ease, background 0.2s ease;
    height: 100%;
    min-height: 0;
  }
  .file-pane.drag-over {
    border-color: var(--primary);
    background: var(--primary-subtle);
  }
  .pane-header {
    background: var(--bg-elevated);
    padding: 10px 14px;
    border-bottom: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    gap: 8px;
    flex-shrink: 0;
  }
  .pane-top-row {
    display: flex;
    align-items: center;
    gap: 8px;
    height: 28px;
  }
  .pane-label {
    font-size: 12px;
    font-weight: 600;
    color: var(--text-muted);
    width: 32px;
    flex-shrink: 0;
    height: 28px;
    line-height: 28px;
    display: flex;
    align-items: center;
  }
  .pane-select {
    flex: 1;
    font-size: 12px;
    height: 28px;
    line-height: 26px;
    padding: 0 8px;
    box-sizing: border-box;
    display: flex;
    align-items: center;
  }
  .drive-chips {
    display: flex;
    gap: 4px;
    align-items: center;
    flex-shrink: 0;
    height: 28px;
  }
  .drive-chip {
    height: 28px;
    line-height: 26px;
    padding: 0 8px;
    font-size: 11px;
    font-family: var(--font-mono);
    font-weight: 600;
    border-radius: 4px;
    border: 1px solid var(--border);
    background: var(--bg-surface);
    color: var(--text-muted);
    cursor: pointer;
    box-shadow: 0 1px 2px rgba(0, 0, 0, 0.04);
    transition: all 0.15s ease;
    box-sizing: border-box;
    display: inline-flex;
    align-items: center;
    justify-content: center;
  }
  .drive-chip:hover {
    background: var(--bg-hover);
    color: var(--primary);
    border-color: var(--primary);
  }
  .drive-chip.active {
    background: var(--primary-subtle);
    color: var(--primary);
    border-color: var(--primary);
  }
  .pane-path-row {
    display: flex;
    gap: 6px;
    align-items: center;
    height: 28px;
  }
  .pane-path-input {
    flex: 1;
    font-size: 12px;
    padding: 2px 8px;
    height: 28px;
    box-sizing: border-box;
  }
  .pane-body {
    flex: 1;
    overflow-y: auto;
    min-height: 0;
  }
  .file-table-wrap {
    width: 100%;
  }
  .file-table-wrap .data-table thead th {
    position: sticky;
    top: 0;
    z-index: 10;
    background: var(--bg-elevated);
    box-shadow: 0 1px 0 var(--border);
    padding: 8px 12px;
    font-size: 12px;
  }
  .file-table-wrap .data-table td {
    padding: 4px 12px;
    height: 34px;
    box-sizing: border-box;
    vertical-align: middle;
  }
  .file-row {
    cursor: grab;
    user-select: none;
  }
  .file-row:active {
    cursor: grabbing;
  }
  .file-row:hover {
    background-color: var(--bg-hover);
  }
  .file-row.selected {
    background-color: var(--primary-subtle);
  }
  .file-icon {
    font-size: 14px;
    text-align: center;
  }
  .dir-link {
    background: none;
    border: none;
    color: var(--primary);
    font-weight: 500;
    cursor: pointer;
    text-align: left;
    font-size: 13px;
    padding: 0;
  }
  .dir-link:hover {
    text-decoration: underline;
  }
  .action-cell {
    white-space: nowrap;
  }
  .btn-action-icon {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 26px;
    height: 26px;
    border-radius: 4px;
    border: 1px solid var(--border);
    background: var(--bg-surface);
    color: var(--text-muted);
    box-shadow: 0 1px 2px rgba(0, 0, 0, 0.05);
    cursor: pointer;
    transition: all 0.15s ease;
    margin-left: 2px;
  }
  .btn-action-icon:hover {
    border-color: var(--primary);
    color: var(--primary);
    background: var(--bg-hover);
    box-shadow: 0 2px 4px rgba(0, 0, 0, 0.08);
  }
  .btn-action-icon.btn-danger-icon:hover {
    border-color: var(--danger);
    color: var(--danger);
  }
  .pane-footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 6px 14px;
    background: var(--bg-elevated);
    border-top: 1px solid var(--border-subtle);
    font-size: 11px;
    color: var(--text-dim);
  }
  .selected-tag {
    color: var(--primary);
    font-weight: 600;
  }
  .pane-status {
    padding: 32px;
    text-align: center;
    color: var(--text-dim);
    font-size: 13px;
  }
  .text-danger {
    color: var(--danger);
  }
  .empty-row,
  .file-table-wrap .data-table tbody tr.empty-row,
  .file-table-wrap .data-table tbody tr.empty-row:hover {
    background-color: transparent !important;
    cursor: default !important;
  }
  .empty-cell {
    text-align: center;
    color: var(--text-dim);
    padding: 48px 16px;
    font-size: 13px;
    cursor: default !important;
    user-select: none;
    border-bottom: none !important;
  }
  .mono {
    font-family: var(--font-mono);
  }
  .text-sm {
    font-size: 12px;
  }
  .muted {
    color: var(--text-muted);
  }
  .font-semibold {
    font-weight: 600;
  }

  /* 右键上下文浮动菜单 */
  .context-menu {
    position: fixed;
    z-index: 9999;
    background: var(--bg-surface);
    border: 1px solid var(--border);
    border-radius: 6px;
    box-shadow: var(--shadow-md);
    padding: 4px;
    min-width: 140px;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .menu-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 10px;
    font-size: 12px;
    color: var(--text-main);
    background: none;
    border: none;
    border-radius: 4px;
    cursor: pointer;
    text-align: left;
    transition: background 0.15s ease;
  }
  .menu-item:hover {
    background: var(--bg-hover);
  }
  .menu-divider {
    height: 1px;
    background: var(--border-subtle);
    margin: 2px 0;
  }

  /* 顶部导航与操作按钮组 */
  .toolbar-sep {
    width: 1px;
    height: 18px;
    background-color: var(--border);
    margin: 0 4px;
    align-self: center;
  }
  .btn-nav-icon {
    padding: 5px 8px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
  }
  .btn-toolbar-action {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 4px 8px;
    font-size: 12px;
    white-space: nowrap;
  }

  /* 列表顶部行内新建文件夹交互 */
  /* 列表顶部行内新建文件夹交互 */
  .inline-mkdir-row td {
    background: var(--primary-subtle);
    border-bottom: 1px solid var(--border);
    padding: 4px 12px;
    height: 34px;
    box-sizing: border-box;
    vertical-align: middle;
  }
  .inline-mkdir-input {
    width: 100%;
    height: 24px;
    font-size: 12px;
    padding: 2px 8px;
    box-sizing: border-box;
  }
  .inline-mkdir-actions {
    display: inline-flex;
    align-items: center;
    justify-content: flex-end;
    gap: 4px;
  }
  .btn-xs {
    height: 24px;
    line-height: 22px;
    padding: 0 8px;
    font-size: 11px;
    box-sizing: border-box;
  }

  /* 原地行内二次确认控件 (零弹窗轻量交互) */
  .inline-row-confirm {
    display: inline-flex;
    align-items: center;
    justify-content: flex-end;
    gap: 4px;
    animation: fadeIn 0.15s ease-in-out;
  }
  .inline-confirm-tip {
    font-size: 11px;
    color: var(--danger);
    font-weight: 500;
  }
  @keyframes fadeIn {
    from { opacity: 0; transform: scale(0.96); }
    to { opacity: 1; transform: scale(1); }
  }
</style>
