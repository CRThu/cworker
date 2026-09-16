// web/src/test/app.test.ts - App 顶层容器状态流转与任务启动集成测试
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, fireEvent, waitFor } from '@testing-library/svelte';
import App from '../App.svelte';
import * as api from '../lib/api';
import type { NodeInfo } from '../lib/types';

vi.mock('../lib/api', () => ({
  fetchOverview: vi.fn(),
  fetchNodes: vi.fn(),
  fetchJobs: vi.fn(),
  addNode: vi.fn(),
  removeNode: vi.fn(),
  runJob: vi.fn(),
  dispatchJob: vi.fn(),
  killJob: vi.fn(),
  cleanJobs: vi.fn(),
  getRoots: vi.fn().mockResolvedValue(['C:/']),
  listDir: vi.fn().mockResolvedValue([]),
}));

describe('App component task dispatch integration', () => {
  const mockNodes: NodeInfo[] = [
    { name: 'worker-1', address: '127.0.0.1:19000', status: 'ONLINE', active_jobs: 0 },
    { name: 'worker-2', address: '127.0.0.1:19001', status: 'ONLINE', active_jobs: 0 },
  ];

  beforeEach(() => {
    vi.clearAllMocks();
    window.matchMedia = vi.fn().mockImplementation((query) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
    vi.mocked(api.fetchOverview).mockResolvedValue({
      version: '1.4.0',
      hostname: 'test-host',
      nodes_count: 2,
      online_count: 2,
      active_jobs: 0,
      avg_cpu: 10,
      total_mem_mb: 16000,
      total_free_mem_mb: 8000,
      total_cpu_cores: 8,
      nodes: mockNodes,
    } as any);
    vi.mocked(api.fetchNodes).mockResolvedValue({
      nodes: mockNodes,
      known: mockNodes.map(n => ({ name: n.name, target: n.address, token: 'tok' })),
    });
    vi.mocked(api.fetchJobs).mockResolvedValue([]);
  });

  it('should render App and handle multi-node dispatchJob with success toast', async () => {
    vi.mocked(api.dispatchJob).mockResolvedValue({
      succeeded: [
        { id: 'job-1', node: 'worker-1', pid: 101, command: 'pytest', status: 'RUNNING', start_time: '' } as any,
        { id: 'job-2', node: 'worker-2', pid: 102, command: 'pytest', status: 'RUNNING', start_time: '' } as any,
      ],
      failed: [],
    });

    const { container, getByText } = render(App);

    // 切换到任务视图
    const jobsTabBtn = getByText('任务');
    await fireEvent.click(jobsTabBtn);

    // 点击新建任务
    const runBtn = getByText('新建任务');
    await fireEvent.click(runBtn);

    // 弹窗打开
    expect(container.querySelector('.modal-box')).not.toBeNull();

    // 输入命令
    const cmdInput = container.querySelector('#run-job-cmd') as HTMLTextAreaElement;
    await fireEvent.input(cmdInput, { target: { value: 'pytest' } });

    // 提交启动
    const form = container.querySelector('form') as HTMLFormElement;
    await fireEvent.submit(form);

    // 验证 dispatchJob 被正确调用
    expect(api.dispatchJob).toHaveBeenCalled();

    // 等待 toast 显示
    await waitFor(() => {
      const toast = container.querySelector('.toast.success');
      expect(toast).not.toBeNull();
      expect(toast?.textContent).toContain('已成功在 2 个节点启动任务');
    });
  });

  it('should display error toast when dispatchJob has failed nodes', async () => {
    vi.mocked(api.dispatchJob).mockResolvedValue({
      succeeded: [
        { id: 'job-1', node: 'worker-1', pid: 101, command: 'pytest', status: 'RUNNING', start_time: '' } as any,
      ],
      failed: [
        { node: 'worker-2', error: 'node offline' },
      ],
    });

    const { container, getByText } = render(App);

    // 切换到任务视图并打开新建任务弹窗
    await fireEvent.click(getByText('任务'));
    await fireEvent.click(getByText('新建任务'));

    const cmdInput = container.querySelector('#run-job-cmd') as HTMLTextAreaElement;
    await fireEvent.input(cmdInput, { target: { value: 'pytest' } });

    const form = container.querySelector('form') as HTMLFormElement;
    await fireEvent.submit(form);

    await waitFor(() => {
      const errorToast = container.querySelector('.toast.error');
      expect(errorToast).not.toBeNull();
      expect(errorToast?.textContent).toContain('部分节点启动失败 (1): worker-2: node offline');
    });
  });
});
