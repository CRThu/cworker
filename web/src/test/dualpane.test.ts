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
});
