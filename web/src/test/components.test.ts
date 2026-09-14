// web/src/test/components.test.ts - Svelte 关键组件集成测试
import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import KillConfirmModal from '../lib/components/KillConfirmModal.svelte';
import Sparkline from '../lib/components/Sparkline.svelte';
import NodesView from '../lib/components/NodesView.svelte';
import AddNodeModal from '../lib/components/AddNodeModal.svelte';
import CleanJobsModal from '../lib/components/CleanJobsModal.svelte';
import RunJobModal from '../lib/components/RunJobModal.svelte';
import type { JobInfo } from '../lib/types';

vi.mock('../lib/api', () => ({
  getRoots: vi.fn().mockResolvedValue(['C:/', 'D:/']),
  listDir: vi.fn().mockResolvedValue([
    { name: 'projects', is_dir: true, size: 0, mod_time: '' },
  ]),
}));

describe('KillConfirmModal', () => {
  const mockJob: JobInfo = {
    id: 'job-test-1234',
    name: 'swift-falcon-100',
    node: 'worker-node-1',
    command: 'python run.py',
    status: 'RUNNING',
    pid: 9999,
    start_time: new Date().toISOString(),
  };

  it('should not render anything when open is false', () => {
    const { container } = render(KillConfirmModal, {
      props: { open: false, job: mockJob },
    });
    expect(container.querySelector('.modal-overlay')).toBeNull();
  });

  it('should render concise warning text when open is true', () => {
    const { getByText, container } = render(KillConfirmModal, {
      props: { open: true, job: mockJob },
    });
    expect(container.querySelector('.modal-overlay')).not.toBeNull();
    // 验证精炼文案，消除冗余说教
    expect(getByText(/确定终止该任务？将彻底清除其全部进程树/i)).toBeTruthy();
    expect(getByText('job-test-1234')).toBeTruthy();
    expect(getByText('worker-node-1')).toBeTruthy();
  });

  it('should dispatch confirm event when clicking confirm button', async () => {
    let confirmedId = '';
    const { container } = render(KillConfirmModal, {
      props: {
        open: true,
        job: mockJob,
        onConfirm: (id: string) => {
          confirmedId = id;
        },
      },
    });

    const confirmBtn = container.querySelector('.btn-danger') as HTMLElement;
    expect(confirmBtn).not.toBeNull();
    await fireEvent.click(confirmBtn);
    expect(confirmedId).toBe('job-test-1234');
  });
});

describe('Sparkline component', () => {
  it('should render SVG with polyline and polygon when given historical values', () => {
    const { container } = render(Sparkline, {
      props: { values: [10, 25, 45, 80, 20], max: 100, color: '#f97316' },
    });
    const svg = container.querySelector('svg');
    expect(svg).not.toBeNull();
    const polyline = container.querySelector('polyline');
    expect(polyline).not.toBeNull();
    expect(polyline?.getAttribute('stroke')).toBe('#f97316');
    const polygon = container.querySelector('polygon');
    expect(polygon).not.toBeNull();
  });

  it('should render safely without throwing when given empty or single value', () => {
    const { container } = render(Sparkline, {
      props: { values: [] },
    });
    const svg = container.querySelector('svg');
    expect(svg).not.toBeNull();
  });
});

describe('NodesView component layout and interactions', () => {
  const mockNodes: any[] = [
    {
      name: 'DESKTOP-4090',
      address: '100.93.237.16:19000',
      status: 'ONLINE',
      active_jobs: 2,
      metrics: { cpu_percent: 15.0, mem_total_mb: 32768, mem_free_mb: 16384, disk_free_mb: 100000 },
      version: 'v1.4.0',
    },
    {
      name: 'OFFLINE-BOX',
      address: '192.168.1.200:19000',
      status: 'OFFLINE',
      active_jobs: 0,
    },
  ];

  const mockKnownNodes: any[] = [
    { name: 'DESKTOP-4090', target: '100.93.237.16:19000', token: 'token-abc-123' },
    { name: 'OFFLINE-BOX', target: '192.168.1.200:19000', token: '' },
  ];

  it('should remove standalone Token column header to save horizontal space', () => {
    const { container } = render(NodesView, {
      props: {
        nodes: mockNodes,
        knownNodes: mockKnownNodes,
      },
    });

    const ths = Array.from(container.querySelectorAll('thead th')).map(th => th.textContent?.trim());
    expect(ths).toContain('状态');
    expect(ths).toContain('名称');
    expect(ths).toContain('地址');
    expect(ths).toContain('主机 CPU');
    expect(ths).toContain('主机内存');
    expect(ths).toContain('任务数');
    expect(ths).toContain('操作');
    // 独立 Token 列已移除，防止页面水平溢出
    expect(ths).not.toContain('Token');
  });

  it('should render compact status icons for online and offline states', () => {
    const { container } = render(NodesView, {
      props: {
        nodes: mockNodes,
        knownNodes: mockKnownNodes,
      },
    });

    const onlineBadges = container.querySelectorAll('.status-icon-badge.online');
    expect(onlineBadges.length).toBe(1);
    expect(onlineBadges[0].getAttribute('title')).toContain('ONLINE');

    const offlineBadges = container.querySelectorAll('.status-icon-badge.offline');
    expect(offlineBadges.length).toBe(1);
    expect(offlineBadges[0].getAttribute('title')).toContain('OFFLINE');
  });

  it('should merge Token copy button into operations column', () => {
    const { container, getByText } = render(NodesView, {
      props: {
        nodes: mockNodes,
        knownNodes: mockKnownNodes,
      },
    });

    // 拥有 token 的节点在操作列中有 [Token] 复制按钮
    const rows = container.querySelectorAll('tbody tr');
    expect(rows.length).toBe(2);

    const firstRowActions = rows[0].querySelector('.row-actions');
    expect(firstRowActions?.textContent).toContain('Token');
    expect(firstRowActions?.textContent).toContain('移除');

    // 没有 token 的节点仅有 [移除] 按钮
    const secondRowActions = rows[1].querySelector('.row-actions');
    expect(secondRowActions?.textContent).not.toContain('Token');
    expect(secondRowActions?.textContent).toContain('移除');
  });

  it('should format available and total memory in 26.0G / 31.6G format', () => {
    const { container } = render(NodesView, {
      props: {
        nodes: mockNodes,
        overview: {
          nodes_count: 2,
          online_count: 1,
          active_jobs: 2,
          avg_cpu: 15.0,
          total_free_mem_mb: 26624,
          total_mem_mb: 32358,
          nodes: mockNodes,
          local_card: { name: 'local', ip: '127.0.0.1', port: 19000, token: '' },
        },
      },
    });

    const statCards = container.querySelectorAll('.stat-card');
    const memCard = Array.from(statCards).find(c => c.querySelector('.stat-label')?.textContent?.includes('可用内存'));
    expect(memCard).toBeDefined();
    expect(memCard?.querySelector('.stat-value')?.textContent?.trim()).toBe('26.0G / 31.6G');
    expect(memCard?.querySelector('.stat-sub')?.textContent?.trim()).toBe('可分配 / 总量');
  });
});

describe('AddNodeModal validation and computer name support', () => {
  it('should require Token as a mandatory field and support computer name in address hint', () => {
    const { container, getByText } = render(AddNodeModal, {
      props: { open: true },
    });

    // 目标地址应明确提示计算机名支持
    expect(getByText(/目标地址 \(计算机名 \/ IP:端口\)/)).toBeTruthy();
    const addrInput = container.querySelector('#add-node-target') as HTMLInputElement;
    expect(addrInput).not.toBeNull();
    expect(addrInput.placeholder).toContain('DESKTOP-4090');
    expect(addrInput.hasAttribute('required')).toBe(true);

    // Token 字段为必填项 (required)
    const tokenInput = container.querySelector('input[type="password"]') as HTMLInputElement;
    expect(tokenInput).not.toBeNull();
    expect(tokenInput.hasAttribute('required')).toBe(true);
  });
});

describe('CleanJobsModal multi-node selection and text optimization', () => {
  it('should render MultiSelect for workers and remove obsolete RUNNING text', () => {
    const { container, queryByText } = render(CleanJobsModal, {
      props: {
        open: true,
        nodes: [
          { name: 'worker-1', address: '127.0.0.1:19000', status: 'ONLINE', active_jobs: 0 },
          { name: 'worker-2', address: '127.0.0.1:19001', status: 'ONLINE', active_jobs: 1 },
        ],
      },
    });

    // 验证 MultiSelect 组件已挂载
    const multiSelect = container.querySelector('.multiselect-container');
    expect(multiSelect).not.toBeNull();

    // 验证废弃说教文案已彻底移除
    expect(queryByText(/运行中 \(RUNNING\) 任务受底层保护/i)).toBeNull();
  });
});

describe('RunJobModal with directory browse', () => {
  it('should render browse button and open directory picker modal', async () => {
    const { container, getByText } = render(RunJobModal, {
      props: {
        open: true,
        nodes: [{ name: 'worker-1', address: '127.0.0.1:19000', status: 'ONLINE', active_jobs: 0 }],
      },
    });

    const browseBtn = container.querySelector('.btn-browse') as HTMLButtonElement;
    expect(browseBtn).not.toBeNull();
    expect(browseBtn.textContent).toContain('浏览');

    // 点击浏览按钮
    await fireEvent.click(browseBtn);
    await new Promise(r => setTimeout(r, 20));

    // 验证目录选择模态框已渲染
    const picker = container.querySelector('.dir-picker-box');
    expect(picker).not.toBeNull();
    expect(getByText(/选择工作目录/)).toBeTruthy();

    // 在目录选择器中点击确定选择
    const confirmBtn = picker?.querySelector('.btn-primary') as HTMLButtonElement;
    expect(confirmBtn).not.toBeNull();
    await fireEvent.click(confirmBtn);

    // 验证模态框已关闭
    await new Promise(r => setTimeout(r, 20));
    expect(container.querySelector('.dir-picker-box')).toBeNull();

    // 验证工作目录输入框成功回填路径
    const dirInput = container.querySelector('#run-job-dir') as HTMLInputElement;
    expect(dirInput.value).toBe('C:/');
  });
});

