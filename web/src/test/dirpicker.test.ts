// web/src/test/dirpicker.test.ts - 跨节点物理工作目录选择模态框单元测试
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import DirPickerModal from '../lib/components/DirPickerModal.svelte';
import * as api from '../lib/api';

vi.mock('../lib/api', () => ({
  listDir: vi.fn(),
  getRoots: vi.fn(),
}));

describe('DirPickerModal component', () => {
  const mockRoots = ['C:/', 'D:/'];
  const mockItems = [
    { name: 'Projects', is_dir: true, size: 0, mod_time: '2026-09-14T10:00:00Z' },
    { name: 'Downloads', is_dir: true, size: 0, mod_time: '2026-09-14T11:00:00Z' },
    { name: 'notes.txt', is_dir: false, size: 1024, mod_time: '2026-09-14T12:00:00Z' }, // 文件，应当被过滤掉
  ];

  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getRoots).mockResolvedValue(mockRoots);
    vi.mocked(api.listDir).mockResolvedValue(mockItems);
  });

  it('should not render anything when open is false', () => {
    const { container } = render(DirPickerModal, {
      props: { open: false, node: 'remote-node' },
    });
    expect(container.querySelector('.modal-overlay')).toBeNull();
  });

  it('should render drives and only folder entries when open is true', async () => {
    const { container, getAllByText, getByText, queryByText } = render(DirPickerModal, {
      props: { open: true, node: 'remote-node', initialPath: 'C:/' },
    });

    await new Promise((r) => setTimeout(r, 20));

    expect(container.querySelector('.modal-overlay')).not.toBeNull();
    // 验证盘符与路径显示
    expect(getAllByText('C:/').length).toBeGreaterThanOrEqual(1);
    expect(getByText('D:/')).toBeTruthy();
    // 验证仅展示文件夹
    expect(getByText('Projects')).toBeTruthy();
    expect(getByText('Downloads')).toBeTruthy();
    // 纯文本文件应当被过滤
    expect(queryByText('notes.txt')).toBeNull();
  });

  it('should drill down into folder on double click', async () => {
    const { getByText } = render(DirPickerModal, {
      props: { open: true, node: '', initialPath: 'C:/' },
    });

    await new Promise((r) => setTimeout(r, 20));

    const folderRow = getByText('Projects');
    await fireEvent.dblClick(folderRow);

    await new Promise((r) => setTimeout(r, 20));

    // 验证下钻调用 listDir 拼接路径
    expect(api.listDir).toHaveBeenCalledWith('', 'C:/Projects');
  });

  it('should dispatch select event with selected directory on confirm', async () => {
    let selectedPath = '';
    const { getByText } = render(DirPickerModal, {
      props: {
        open: true,
        node: 'remote-box',
        initialPath: 'C:/',
        onSelect: (p: string) => {
          selectedPath = p;
        },
      },
    });

    await new Promise((r) => setTimeout(r, 20));

    // 单选 Projects 文件夹
    const folderRow = getByText('Projects');
    await fireEvent.click(folderRow);

    // 点击确定选择
    const confirmBtn = getByText('确定选择');
    await fireEvent.click(confirmBtn);

    expect(selectedPath).toBe('C:/Projects');
  });

  it('should dispatch close event when clicking close or cancel button', async () => {
    let closed = false;
    const { getByText } = render(DirPickerModal, {
      props: {
        open: true,
        node: '',
        initialPath: 'C:/',
        onClose: () => {
          closed = true;
        },
      },
    });

    const cancelBtn = getByText('取消');
    await fireEvent.click(cancelBtn);
    expect(closed).toBe(true);
  });
});
