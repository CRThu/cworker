// web/src/test/dualpane.test.ts - 双栏文件管理器原地确认与行内操作集成测试
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import DualPaneFiles from '../lib/components/DualPaneFiles.svelte';
import * as api from '../lib/api';

vi.mock('../lib/api', () => ({
  listDir: vi.fn(),
  getRoots: vi.fn(),
  makeDir: vi.fn(),
  removePath: vi.fn(),
  transfer: vi.fn(),
}));

describe('DualPaneFiles in-situ operations and keyboard shortcuts', () => {
  const mockNodes: any[] = [
    { name: 'local-node', address: '127.0.0.1:19000', status: 'ONLINE' },
    { name: 'remote-node', address: '192.168.1.50:19000', status: 'ONLINE' },
  ];

  const mockFiles = [
    { name: 'docs', is_dir: true, size: 0, mod_time: '' },
    { name: 'report.pdf', is_dir: false, size: 1048576, mod_time: '' },
    { name: 'test.txt', is_dir: false, size: 2048, mod_time: '' },
  ];

  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getRoots).mockResolvedValue(['C:/', 'D:/']);
    vi.mocked(api.listDir).mockResolvedValue(mockFiles);
    vi.mocked(api.makeDir).mockResolvedValue(undefined);
    vi.mocked(api.removePath).mockResolvedValue(undefined);
  });

  it('should render file tables and not render full-screen modal overlays', async () => {
    const { container } = render(DualPaneFiles, {
      props: { nodes: mockNodes },
    });

    // 验证模态弹窗遮罩已被彻底移除 (零弹窗轻量交互)
    expect(container.querySelector('.modal-overlay')).toBeNull();
    // 验证左右两栏结构
    const panes = container.querySelectorAll('.file-pane');
    expect(panes.length).toBe(2);
  });

  it('should display inline mkdir row when clicking "新建", and cancel on Escape', async () => {
    const { container } = render(DualPaneFiles, {
      props: { nodes: mockNodes },
    });

    await new Promise(r => setTimeout(r, 20));

    // 点击左侧工具栏 [新建] 按钮
    const mkdirBtn = container.querySelector('.btn-toolbar-action') as HTMLButtonElement;
    expect(mkdirBtn).not.toBeNull();
    await fireEvent.click(mkdirBtn);

    // 验证表格顶部出现行内新建文件夹编辑框
    const inlineRow = container.querySelector('.inline-mkdir-row');
    expect(inlineRow).not.toBeNull();
    const input = inlineRow?.querySelector('input') as HTMLInputElement;
    expect(input).not.toBeNull();
    expect(input.value).toBe('新文件夹');

    // 按 Escape 键取消
    await fireEvent.keyDown(window, { key: 'Escape' });
    expect(container.querySelector('.inline-mkdir-row')).toBeNull();
  });

  it('should trigger in-situ delete confirm on row delete button, and confirm delete', async () => {
    const { container } = render(DualPaneFiles, {
      props: { nodes: mockNodes },
    });

    await new Promise(r => setTimeout(r, 20));

    // 找到首行操作列中的 [删除] 按钮
    const rowDeleteBtn = container.querySelector('.btn-danger-icon') as HTMLButtonElement;
    expect(rowDeleteBtn).not.toBeNull();
    await fireEvent.click(rowDeleteBtn);

    // 验证原地出现确认提示（无全局模态框）
    expect(container.querySelector('.modal-overlay')).toBeNull();
    const inlineConfirm = container.querySelector('.inline-row-confirm');
    expect(inlineConfirm).not.toBeNull();
    expect(inlineConfirm?.textContent).toContain('确定？');

    // 点击原地 [确定] 按钮执行删除
    const confirmBtn = inlineConfirm?.querySelector('.btn-danger') as HTMLButtonElement;
    expect(confirmBtn).not.toBeNull();
    await fireEvent.click(confirmBtn);

    expect(api.removePath).toHaveBeenCalledWith('', 'C:/docs', true);
  });

  it('should trigger in-situ row delete confirm and cancel on Escape', async () => {
    const { container } = render(DualPaneFiles, {
      props: { nodes: mockNodes },
    });

    await new Promise(r => setTimeout(r, 20));

    // 点击行内操作列中的 [删除] 按钮
    const rowDeleteBtn = container.querySelector('.btn-danger-icon') as HTMLButtonElement;
    expect(rowDeleteBtn).not.toBeNull();
    await fireEvent.click(rowDeleteBtn);

    // 验证行内出现原地确认提示
    const inlineConfirm = container.querySelector('.inline-row-confirm');
    expect(inlineConfirm).not.toBeNull();
    expect(inlineConfirm?.textContent).toContain('确定？');

    // 按 Escape 键取消
    await fireEvent.keyDown(window, { key: 'Escape' });
    expect(container.querySelector('.inline-row-confirm')).toBeNull();
  });

  it('should display progress banner with close button and close on click', async () => {
    // 模拟 transfer 会调用 onProgress
    vi.mocked(api.transfer).mockImplementation(async (_req, onProgress) => {
      if (onProgress) {
        onProgress({
          type: 'progress',
          percent: 50,
          total_files: 2,
          completed_files: 1,
          active_files: ['fileA.txt'],
        });
      }
      await new Promise(r => setTimeout(r, 50));
    });

    const { container } = render(DualPaneFiles, {
      props: { nodes: mockNodes },
    });

    await new Promise(r => setTimeout(r, 20));

    // 点击对侧传输按钮
    const sendBtn = container.querySelector('.btn-action-icon') as HTMLButtonElement;
    expect(sendBtn).not.toBeNull();
    await fireEvent.click(sendBtn);

    await new Promise(r => setTimeout(r, 20));

    // 验证进度横幅显示
    const banner = container.querySelector('.progress-banner');
    expect(banner).not.toBeNull();

    // 验证关闭按钮存在并可点击消除横幅
    const closeBtn = container.querySelector('.btn-banner-close') as HTMLButtonElement;
    expect(closeBtn).not.toBeNull();
    await fireEvent.click(closeBtn);

    // 验证横幅已消失
    expect(container.querySelector('.progress-banner')).toBeNull();
  });

  it('should allow manual dismissal of error banner on transfer failure', async () => {
    vi.mocked(api.transfer).mockRejectedValue(new Error('connection refused'));

    const { container } = render(DualPaneFiles, {
      props: { nodes: mockNodes },
    });

    await new Promise(r => setTimeout(r, 20));

    const sendBtn = container.querySelector('.btn-action-icon') as HTMLButtonElement;
    await fireEvent.click(sendBtn);

    await new Promise(r => setTimeout(r, 50));

    // 验证错误横幅出现且带 error 类
    const errorBanner = container.querySelector('.progress-banner.error');
    expect(errorBanner).not.toBeNull();
    expect(errorBanner?.textContent).toContain('connection refused');

    // 点击关闭按钮
    const closeBtn = container.querySelector('.btn-banner-close') as HTMLButtonElement;
    expect(closeBtn).not.toBeNull();
    await fireEvent.click(closeBtn);

    // 验证错误横幅被彻底清除
    expect(container.querySelector('.progress-banner')).toBeNull();
  });

  it('should correctly format bulk files progress and present clean single-bar layout without clutter', async () => {
    vi.mocked(api.transfer).mockImplementation(async (_req, onProgress) => {
      if (onProgress) {
        onProgress({
          type: 'progress',
          percent: 50,
          total_files: 50,
          completed_files: 25,
          total_bytes: 524288000,
          transferred_bytes: 262144000,
          speed_bps: 10485760,
        });
      }
      await new Promise(r => setTimeout(r, 50));
    });

    const { container } = render(DualPaneFiles, {
      props: { nodes: mockNodes },
    });

    await new Promise(r => setTimeout(r, 20));
    const sendBtn = container.querySelector('.btn-action-icon') as HTMLButtonElement;
    await fireEvent.click(sendBtn);

    await new Promise(r => setTimeout(r, 20));

    const banner = container.querySelector('.progress-banner');
    expect(banner).not.toBeNull();
    // 验证状态文本专业精炼，精准指示多文件项数、体量与速率
    expect(banner?.textContent).toContain('(25/50 项)');
    expect(banner?.textContent).toContain('250 MB / 500 MB');
    expect(banner?.textContent).toContain('10 MB/s');
    expect(banner?.textContent).toContain('50%');

    // 严密断言：杜绝繁杂的折叠开关、活跃槽位文字与芯片列表，呈现与单文件完全一致的纯净单进度条
    expect(container.querySelector('.btn-banner-toggle')).toBeNull();
    expect(container.querySelector('.progress-files-detail')).toBeNull();
    expect(container.querySelector('.progress-file-chips')).toBeNull();
  });

  it('should call getRoots with selected node when switching node in left pane', async () => {
    const { container } = render(DualPaneFiles, {
      props: { nodes: mockNodes },
    });

    await new Promise(r => setTimeout(r, 20));

    // 初始渲染时：左侧默认为空字符串（本地），右侧默认选择首个在线节点 'local-node'
    expect(api.getRoots).toHaveBeenCalledWith('');
    expect(api.getRoots).toHaveBeenCalledWith('local-node');

    // 切换左侧选择到 'remote-node'
    const leftSelect = container.querySelector('#left-node-select') as HTMLSelectElement;
    expect(leftSelect).not.toBeNull();
    leftSelect.value = 'remote-node';
    await fireEvent.change(leftSelect);

    await new Promise(r => setTimeout(r, 20));
    // 验证向后端获取盘符时正确传递了目标远端节点名
    expect(api.getRoots).toHaveBeenCalledWith('remote-node');
  });

  it('should switch drive on drive chip click, drill into folder on link click, and navigate up', async () => {
    const { container, getByText } = render(DualPaneFiles, {
      props: { nodes: mockNodes },
    });

    await new Promise(r => setTimeout(r, 20));

    // 1. 点击盘符 D:/
    const dChip = Array.from(container.querySelectorAll('.drive-chips .drive-chip')).find(c => c.textContent?.trim() === 'D:/') as HTMLButtonElement;
    expect(dChip).toBeDefined();
    await fireEvent.click(dChip);
    expect(api.listDir).toHaveBeenCalledWith('', 'D:/');

    // 2. 点击左栏文件夹链接 docs 进入下级目录
    const leftPane = container.querySelectorAll('.file-pane')[0];
    const docsLink = leftPane.querySelector('.dir-link') as HTMLButtonElement;
    expect(docsLink).not.toBeNull();
    await fireEvent.click(docsLink);
    expect(api.listDir).toHaveBeenCalledWith('', 'D:/docs');

    // 3. 点击返回上一级按钮
    const upBtn = container.querySelector('.pane-path-row .btn-nav-icon[title="返回上一级"]') as HTMLButtonElement;
    expect(upBtn).not.toBeNull();
    await fireEvent.click(upBtn);
    expect(api.listDir).toHaveBeenCalledWith('', 'D:/');
  });

  it('should open context menu on right click on file row and container, and perform actions', async () => {
    const { container, getByText } = render(DualPaneFiles, {
      props: { nodes: mockNodes },
    });

    await new Promise(r => setTimeout(r, 20));

    // 1. 在首个文件行上右键触发上下文菜单
    const firstRow = container.querySelector('tbody tr') as HTMLTableRowElement;
    expect(firstRow).not.toBeNull();
    await fireEvent.contextMenu(firstRow, { clientX: 100, clientY: 150 });

    const contextMenu = container.querySelector('.context-menu');
    expect(contextMenu).not.toBeNull();
    expect(getByText('发送至对侧')).toBeTruthy();
    expect(getByText('新建文件夹')).toBeTruthy();

    // 2. 点击发送至对侧
    const sendMenuItem = getByText('发送至对侧');
    await fireEvent.click(sendMenuItem);
    expect(api.transfer).toHaveBeenCalled();
    expect(container.querySelector('.context-menu')).toBeNull();

    // 3. 在空白容器区域右键触发上下文菜单并点击刷新
    const paneContainer = container.querySelector('.file-pane') as HTMLElement;
    await fireEvent.contextMenu(paneContainer, { clientX: 200, clientY: 250 });

    expect(container.querySelector('.context-menu')).not.toBeNull();
    const refreshMenuItem = getByText('刷新目录');
    expect(refreshMenuItem).toBeTruthy();
    await fireEvent.click(refreshMenuItem);
    expect(api.listDir).toHaveBeenCalled();
    expect(container.querySelector('.context-menu')).toBeNull();

    // 4. 在空白区域再次右键，按 Escape 关闭上下文菜单
    await fireEvent.contextMenu(paneContainer, { clientX: 200, clientY: 250 });
    expect(container.querySelector('.context-menu')).not.toBeNull();
    await fireEvent.keyDown(window, { key: 'Escape' });
    expect(container.querySelector('.context-menu')).toBeNull();
  });

  it('should transfer file from right to left on button click and trigger context delete', async () => {
    const { container } = render(DualPaneFiles, {
      props: { nodes: mockNodes },
    });

    await new Promise(r => setTimeout(r, 20));

    // 1. 右栏点击首行操作列中的 [传到左侧] 按钮
    const rightPane = container.querySelectorAll('.file-pane')[1];
    const transferBtn = rightPane.querySelector('.btn-action-icon[title="传到左侧"]') as HTMLButtonElement;
    expect(transferBtn).not.toBeNull();
    await fireEvent.click(transferBtn);
    expect(api.transfer).toHaveBeenCalled();

    // 2. 在右栏首个文件上右键触发删除
    const rightFirstRow = rightPane.querySelector('tbody tr') as HTMLTableRowElement;
    await fireEvent.contextMenu(rightFirstRow, { clientX: 300, clientY: 150 });
    const deleteMenuItem = Array.from(container.querySelectorAll('.context-menu .menu-item')).find(b => b.textContent?.includes('删除')) as HTMLButtonElement;
    expect(deleteMenuItem).toBeDefined();
    await fireEvent.click(deleteMenuItem);

    // 验证原地出现二次确认
    const confirmBox = rightPane.querySelector('.inline-row-confirm');
    expect(confirmBox).not.toBeNull();
  });

  it('should confirm inline mkdir when clicking "确定" in inline row', async () => {
    const { container } = render(DualPaneFiles, {
      props: { nodes: mockNodes },
    });

    await new Promise(r => setTimeout(r, 20));

    // 点击左栏工具栏 [新建]
    const mkdirBtn = container.querySelector('.btn-toolbar-action') as HTMLButtonElement;
    await fireEvent.click(mkdirBtn);

    const inlineRow = container.querySelector('.inline-mkdir-row');
    expect(inlineRow).not.toBeNull();

    const confirmBtn = inlineRow?.querySelector('.btn-primary') as HTMLButtonElement;
    expect(confirmBtn).not.toBeNull();
    await fireEvent.click(confirmBtn);

    expect(api.makeDir).toHaveBeenCalledWith('', 'C:/新文件夹');
  });

  it('should open download url when clicking download action icon on file row', async () => {
    const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null);

    const { container } = render(DualPaneFiles, {
      props: { nodes: mockNodes },
    });

    await new Promise(r => setTimeout(r, 20));

    // 找到非目录文件行的下载按钮
    const downloadBtn = container.querySelector('.btn-action-icon[title="下载"]') as HTMLButtonElement;
    expect(downloadBtn).not.toBeNull();
    await fireEvent.click(downloadBtn);

    expect(openSpy).toHaveBeenCalled();
    expect(openSpy.mock.calls[0][0]).toContain('/api/ui/fs/download?');
    openSpy.mockRestore();
  });

  it('should transfer file when dragging from left pane and dropping onto right pane', async () => {
    const { container } = render(DualPaneFiles, {
      props: { nodes: mockNodes },
    });

    await new Promise(r => setTimeout(r, 20));

    const panes = container.querySelectorAll('.file-pane');
    const leftPane = panes[0];
    const rightPane = panes[1];

    const leftFirstRow = leftPane.querySelector('tbody tr') as HTMLTableRowElement;
    expect(leftFirstRow).not.toBeNull();

    // 触发 dragstart
    const mockDataTransfer = {
      setData: vi.fn(),
      effectAllowed: '',
      dropEffect: '',
    };
    await fireEvent.dragStart(leftFirstRow, { dataTransfer: mockDataTransfer });

    // 触发 dragover 在右栏
    await fireEvent.dragOver(rightPane, { dataTransfer: mockDataTransfer });

    // 触发 drop 在右栏
    await fireEvent.drop(rightPane);

    expect(api.transfer).toHaveBeenCalled();
  });
});



