// web/src/test/logterminal.test.ts - 日志流式抽屉与任务视图交互集成测试
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import LogTerminal from '../lib/components/LogTerminal.svelte';
import JobsView from '../lib/components/JobsView.svelte';
import type { JobInfo, NodeInfo } from '../lib/types';

describe('LogTerminal real-time log streaming and retention', () => {
  class MockWebSocket {
    static instances: MockWebSocket[] = [];
    url: string;
    onopen: (() => void) | null = null;
    onmessage: ((e: { data: string }) => void) | null = null;
    onclose: (() => void) | null = null;
    onerror: ((e: any) => void) | null = null;
    readyState = 0; // CONNECTING

    constructor(url: string) {
      this.url = url;
      MockWebSocket.instances.push(this);
      setTimeout(() => {
        this.readyState = 1; // OPEN
        if (this.onopen) this.onopen();
      }, 0);
    }

    close() {
      this.readyState = 3; // CLOSED
      if (this.onclose) this.onclose();
    }
  }

  beforeEach(() => {
    MockWebSocket.instances = [];
    (globalThis as any).WebSocket = MockWebSocket;
  });

  it('should connect to websocket stream and display incoming text with ANSI colors', async () => {
    const { container, getByText } = render(LogTerminal, {
      props: {
        open: true,
        jobId: 'job-stream-001',
        node: 'node-1',
        status: 'RUNNING',
      },
    });

    expect(getByText('job-stream-001')).toBeTruthy();
    expect(getByText('node-1')).toBeTruthy();
    expect(getByText('运行中')).toBeTruthy();

    // 等待 WebSocket 模拟开启
    await new Promise(r => setTimeout(r, 10));
    const ws = MockWebSocket.instances[0];
    expect(ws).toBeDefined();
    expect(ws.url).toContain('/api/ui/jobs/stream?job_id=job-stream-001');
    expect(ws.url).toContain('node=node-1');

    // 模拟服务端推流（包含 ANSI 颜色代码）
    ws.onmessage?.({ data: '\u001b[32m[OK] Job initialized successfully\u001b[0m\n' });
    await new Promise(r => setTimeout(r, 10));

    const terminalBody = container.querySelector('.terminal-body');
    expect(terminalBody?.innerHTML).toContain('[OK] Job initialized successfully');
    expect(terminalBody?.innerHTML).toContain('color: #4ade80'); // 绿色 ANSI 转换
  });

  it('should fallback to REST /api/ui/jobs/logs when websocket closes with empty output', async () => {
    const mockFetch = vi.fn().mockResolvedValue({
      ok: true,
      text: () => Promise.resolve('Fallback historical log content from disk\n'),
    });
    (globalThis as any).fetch = mockFetch;

    const { container } = render(LogTerminal, {
      props: {
        open: true,
        jobId: 'job-disk-004',
        node: 'node-A',
        status: 'COMPLETED',
      },
    });

    await new Promise(r => setTimeout(r, 10));
    const ws = MockWebSocket.instances[0];
    expect(ws).toBeDefined();

    // WebSocket 立即关闭且未发送任何消息
    ws.close();
    await new Promise(r => setTimeout(r, 20));

    expect(mockFetch).toHaveBeenCalled();
    const calledUrl = mockFetch.mock.calls[0][0];
    expect(calledUrl).toContain('/api/ui/jobs/logs?job_id=job-disk-004');
    expect(calledUrl).toContain('node=node-A');

    const terminalBody = container.querySelector('.terminal-body');
    expect(terminalBody?.textContent).toContain('Fallback historical log content from disk');
  });

  it('should retain logs when websocket stream closes on job completion (边界测试：任务结束日志不丢失)', async () => {
    const { container } = render(LogTerminal, {
      props: {
        open: true,
        jobId: 'job-finished-002',
        status: 'COMPLETED',
      },
    });

    await new Promise(r => setTimeout(r, 10));
    const ws = MockWebSocket.instances[0];
    expect(ws).toBeDefined();

    // 写入日志
    ws.onmessage?.({ data: 'Execution completed with exit code 0\n' });
    await new Promise(r => setTimeout(r, 10));

    // 服务端正常关闭 WebSocket (代表传输完毕或已退出的历史日志查阅完毕)
    ws.close();
    await new Promise(r => setTimeout(r, 10));

    // 关键边界断言：连接断开或传输完毕后，终端区域的日志必须完整保留，绝不能被清空
    const terminalBody = container.querySelector('.terminal-body');
    expect(terminalBody?.textContent).toContain('Execution completed with exit code 0');
    expect(container.querySelector('.footer-hint')?.textContent).toContain('传输完毕');
  });

  it('should support manual clear and reload', async () => {
    const { container } = render(LogTerminal, {
      props: {
        open: true,
        jobId: 'job-test-003',
        status: 'RUNNING',
      },
    });

    await new Promise(r => setTimeout(r, 10));
    const ws = MockWebSocket.instances[0];
    ws.onmessage?.({ data: 'First line of output\n' });
    await new Promise(r => setTimeout(r, 10));

    const terminalBody = container.querySelector('.terminal-body');
    expect(terminalBody?.textContent).toContain('First line of output');

    // 测试清屏
    const clearBtn = container.querySelector('button[title="清屏"]') as HTMLElement;
    expect(clearBtn).not.toBeNull();
    await fireEvent.click(clearBtn);

    expect(container.querySelector('.empty-hint')?.textContent).toContain('暂无日志输出');

    // 测试重新载入
    const reloadBtn = container.querySelector('button[title="重新载入历史日志"]') as HTMLElement;
    expect(reloadBtn).not.toBeNull();
    await fireEvent.click(reloadBtn);

    // 验证发起了新的 WebSocket 连接
    expect(MockWebSocket.instances.length).toBe(2);
  });
});

describe('JobsView filtering, status pills and action dispatches', () => {
  const mockJobs: JobInfo[] = [
    {
      id: 'job-101',
      name: 'alpha-worker-run',
      node: 'node-A',
      command: 'python train.py',
      status: 'RUNNING',
      pid: 1001,
      start_time: new Date(Date.now() - 30000).toISOString(),
    },
    {
      id: 'job-102',
      name: 'beta-eval-job',
      node: 'node-B',
      command: 'pytest test/',
      status: 'COMPLETED',
      pid: 1002,
      start_time: new Date(Date.now() - 60000).toISOString(),
      end_time: new Date(Date.now() - 10000).toISOString(),
    },
    {
      id: 'job-103',
      name: 'gamma-crash-job',
      node: 'node-A',
      command: 'bad_cmd --err',
      status: 'FAILED',
      pid: 1003,
      start_time: new Date(Date.now() - 90000).toISOString(),
      end_time: new Date(Date.now() - 85000).toISOString(),
    },
    {
      id: 'job-104',
      name: 'delta-reboot-stopped',
      node: 'node-C',
      command: 'ping 127.0.0.1 -n 50',
      status: 'STOPPED',
      exit_code: -1,
      pid: 1004,
      start_time: new Date(Date.now() - 120000).toISOString(),
      end_time: new Date(Date.now() - 110000).toISOString(),
    },
  ];

  const mockNodes: NodeInfo[] = [
    { name: 'node-A', address: '10.0.0.1:19000', status: 'ONLINE', active_jobs: 1 },
    { name: 'node-B', address: '10.0.0.2:19000', status: 'ONLINE', active_jobs: 0 },
    { name: 'node-C', address: '10.0.0.3:19000', status: 'ONLINE', active_jobs: 0 },
  ];

  it('should filter jobs by status pills including STOPPED', async () => {
    const { container, getByText } = render(JobsView, {
      props: { jobs: mockJobs, nodes: mockNodes },
    });

    // 默认展示全部 4 个任务
    let rows = container.querySelectorAll('tbody tr');
    expect(rows.length).toBe(4);

    // 点击 "运行中" Pill
    const statusPills = container.querySelectorAll('.status-pills .pill-btn');
    const runningPill = statusPills[1];
    await fireEvent.click(runningPill);
    rows = container.querySelectorAll('tbody tr');
    expect(rows.length).toBe(1);
    expect(rows[0].textContent).toContain('job-101');

    // 点击 "已完成" Pill
    const completedPill = statusPills[2];
    await fireEvent.click(completedPill);
    rows = container.querySelectorAll('tbody tr');
    expect(rows.length).toBe(1);
    expect(rows[0].textContent).toContain('job-102');

    // 点击 "已终止" Pill
    const stoppedPill = statusPills[4]; // 全部, 运行中, 已完成, 失败, 已终止
    await fireEvent.click(stoppedPill);
    rows = container.querySelectorAll('tbody tr');
    expect(rows.length).toBe(1);
    expect(rows[0].textContent).toContain('job-104');
    expect(rows[0].textContent).toContain('已终止');

    // 点击 "全部" Pill 还原
    const allPill = statusPills[0];
    await fireEvent.click(allPill);
    rows = container.querySelectorAll('tbody tr');
    expect(rows.length).toBe(4);
  });

  it('should filter jobs in 0ms with instant search query', async () => {
    const { container } = render(JobsView, {
      props: { jobs: mockJobs, nodes: mockNodes },
    });

    const searchInput = container.querySelector('.search-input') as HTMLInputElement;
    expect(searchInput).not.toBeNull();

    // 搜索任务名称关键字
    await fireEvent.input(searchInput, { target: { value: 'gamma' } });
    let rows = container.querySelectorAll('tbody tr');
    expect(rows.length).toBe(1);
    expect(rows[0].textContent).toContain('job-103');

    // 搜索节点关键字
    await fireEvent.input(searchInput, { target: { value: 'node-B' } });
    rows = container.querySelectorAll('tbody tr');
    expect(rows.length).toBe(1);
    expect(rows[0].textContent).toContain('job-102');

    // 清空搜索
    await fireEvent.input(searchInput, { target: { value: '' } });
    rows = container.querySelectorAll('tbody tr');
    expect(rows.length).toBe(4);
  });

  it('should dispatch openRun, openClean, onViewLogs, and onKillJob correctly', async () => {
    let runOpened = false;
    let cleanOpened = false;
    let viewedJob: any = null;
    let killedJob: any = null;

    const { container } = render(JobsView, {
      props: {
        jobs: mockJobs,
        nodes: mockNodes,
        onOpenRun: () => { runOpened = true; },
        onOpenClean: () => { cleanOpened = true; },
        onViewLogs: (job: JobInfo) => { viewedJob = job; },
        onKillJob: (job: JobInfo) => { killedJob = job; },
      },
    });

    // 1. 点击操作栏 "派发任务" 按钮
    const runBtn = Array.from(container.querySelectorAll('.action-group button')).find(b => b.textContent?.includes('派发任务')) as HTMLElement;
    expect(runBtn).toBeDefined();
    await fireEvent.click(runBtn);
    expect(runOpened).toBe(true);

    // 2. 点击操作栏 "清理" 按钮
    const cleanBtn = Array.from(container.querySelectorAll('.action-group button')).find(b => b.textContent?.includes('清理')) as HTMLElement;
    expect(cleanBtn).toBeDefined();
    await fireEvent.click(cleanBtn);
    expect(cleanOpened).toBe(true);

    // 3. 点击表格中第 4 行（STOPPED 任务 job-104）的 "日志" 按钮
    const rows = container.querySelectorAll('tbody tr');
    const stoppedRow = rows[3];
    const logBtn = stoppedRow.querySelector('.btn-secondary') as HTMLElement;
    expect(logBtn).not.toBeNull();
    await fireEvent.click(logBtn);
    expect(viewedJob).toBeDefined();
    expect(viewedJob?.id).toBe('job-104');

    // 验证 STOPPED 任务行中没有 "终止" 按钮
    const stoppedKillBtn = stoppedRow.querySelector('.btn-danger');
    expect(stoppedKillBtn).toBeNull();

    // 4. 点击表格中第 1 行（RUNNING 任务 job-101）的 "终止" 按钮
    const runningRow = rows[0];
    const killBtn = runningRow.querySelector('.btn-danger') as HTMLElement;
    expect(killBtn).not.toBeNull();
    await fireEvent.click(killBtn);
    expect(killedJob).toBeDefined();
    expect(killedJob?.id).toBe('job-101');
  });
});
