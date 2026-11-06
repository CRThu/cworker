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
    onclose: ((event?: any) => void) | null = null;
    onerror: ((e?: any) => void) | null = null;
    readyState = 0; // CONNECTING

    constructor(url: string) {
      this.url = url;
      MockWebSocket.instances.push(this);
      setTimeout(() => {
        this.readyState = 1; // OPEN
        if (this.onopen) this.onopen();
      }, 0);
    }

    close(code = 1000) {
      this.readyState = 3; // CLOSED
      if (this.onclose) this.onclose({ code });
    }
  }

  beforeEach(() => {
    MockWebSocket.instances = [];
    (globalThis as any).WebSocket = MockWebSocket;
    (globalThis as any).fetch = vi.fn().mockResolvedValue({
      ok: true,
      text: () => Promise.resolve(''),
    });
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

  it('should display "连接已断开" when websocket closes while job is still RUNNING', async () => {
    const { container } = render(LogTerminal, {
      props: {
        open: true,
        jobId: 'job-running-disc',
        status: 'RUNNING',
      },
    });

    await new Promise(r => setTimeout(r, 10));
    const ws = MockWebSocket.instances[0];
    expect(ws).toBeDefined();

    ws.onmessage?.({ data: 'Some running log output\n' });
    await new Promise(r => setTimeout(r, 10));

    ws.close();
    await new Promise(r => setTimeout(r, 10));

    expect(container.querySelector('.footer-hint')?.textContent).toContain('连接已断开');
    expect(container.querySelector('.terminal-body')?.textContent).toContain('Some running log output');
  });

  it('should display error message when fallback REST /api/ui/jobs/logs fails with 500', async () => {
    const mockFetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 500,
      statusText: 'Internal Server Error',
      text: () => Promise.resolve('node DESKTOP-4090 not found in known nodes ledger'),
    });
    (globalThis as any).fetch = mockFetch;

    const { container } = render(LogTerminal, {
      props: {
        open: true,
        jobId: 'job-fallback-err',
        node: 'DESKTOP-4090',
        status: 'RUNNING',
      },
    });

    await new Promise(r => setTimeout(r, 10));
    const ws = MockWebSocket.instances[0];
    expect(ws).toBeDefined();

    // WebSocket 立即关闭且无内容，触发 fallback
    ws.close();
    await new Promise(r => setTimeout(r, 20));

    expect(mockFetch).toHaveBeenCalled();
    const terminalBody = container.querySelector('.terminal-body');
    expect(terminalBody?.textContent).toContain('加载失败: node DESKTOP-4090 not found in known nodes ledger');
    expect(terminalBody?.querySelector('.text-danger')).toBeTruthy();
    expect(container.querySelector('.footer-hint')?.textContent).toContain('加载失败');
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

  it('should trigger close on Escape key press when open', async () => {
    let closed = false;
    render(LogTerminal, {
      props: {
        open: true,
        jobId: 'job-test-esc',
        status: 'RUNNING',
        onClose: () => {
          closed = true;
        },
      },
    });

    await fireEvent.keyDown(window, { key: 'Escape' });
    expect(closed).toBe(true);
  });

  it('should not trigger close on Escape key press when closed', async () => {
    let closed = false;
    render(LogTerminal, {
      props: {
        open: false,
        jobId: 'job-test-esc-closed',
        status: 'RUNNING',
        onClose: () => {
          closed = true;
        },
      },
    });

    await fireEvent.keyDown(window, { key: 'Escape' });
    expect(closed).toBe(false);
  });

  it('should automatically reconnect with backoff when websocket stream is interrupted on RUNNING job', async () => {
    vi.useFakeTimers();
    try {
      const { container } = render(LogTerminal, {
        props: {
          open: true,
          jobId: 'job-recon-1',
          status: 'RUNNING',
        },
      });

      await vi.advanceTimersByTimeAsync(10);
      expect(MockWebSocket.instances.length).toBe(1);
      const ws1 = MockWebSocket.instances[0];

      // 接收初始日志
      ws1.onmessage?.({ data: 'Initial streaming output line 1\n' });
      await vi.advanceTimersByTimeAsync(10);
      expect(container.querySelector('.terminal-body')?.textContent).toContain('Initial streaming output line 1');

      // 模拟网络闪断/异常断开 (code 1006)
      ws1.readyState = 3;
      ws1.onclose?.({ code: 1006 } as any);
      await vi.advanceTimersByTimeAsync(10);

      // 验证断开状态提示与平滑留存 (不闪烁白屏，日志依然留存)
      expect(container.querySelector('.footer-hint')?.textContent).toContain('连接已断开');
      expect(container.querySelector('.footer-hint')?.textContent).toContain('秒后自动重试');
      expect(container.querySelector('.terminal-body')?.textContent).toContain('Initial streaming output line 1');

      // 前进 1050ms 触发第一次退避重连
      await vi.advanceTimersByTimeAsync(1050);
      expect(MockWebSocket.instances.length).toBe(2);
      const ws2 = MockWebSocket.instances[1];

      // 等待 ws2 建立连接
      await vi.advanceTimersByTimeAsync(10);
      expect(container.querySelector('.footer-hint')?.textContent).toBe('实时传输中');

      // ws2 推送最新日志
      ws2.onmessage?.({ data: 'Reconnected updated output line 2\n' });
      await vi.advanceTimersByTimeAsync(10);
      expect(container.querySelector('.terminal-body')?.textContent).toContain('Reconnected updated output line 2');
    } finally {
      vi.useRealTimers();
    }
  });

  it('should immediately trigger reconnect on browser window online event without waiting for timer', async () => {
    const { container } = render(LogTerminal, {
      props: {
        open: true,
        jobId: 'job-recon-online',
        status: 'RUNNING',
      },
    });

    await new Promise(r => setTimeout(r, 10));
    expect(MockWebSocket.instances.length).toBe(1);
    const ws1 = MockWebSocket.instances[0];

    // 发送初始日志
    ws1.onmessage?.({ data: 'Initial line for online test\n' });
    await new Promise(r => setTimeout(r, 10));

    // 模拟连接异常断开
    ws1.readyState = 3;
    ws1.onclose?.({ code: 1006 } as any);
    await new Promise(r => setTimeout(r, 10));

    expect(container.querySelector('.footer-hint')?.textContent).toContain('连接已断开');

    // 模拟浏览器恢复网络触发 online 事件
    window.dispatchEvent(new Event('online'));
    await new Promise(r => setTimeout(r, 20));

    // 验证无需等待数秒定时器，立即发起第 2 次重连
    expect(MockWebSocket.instances.length).toBe(2);
    expect(container.querySelector('.footer-hint')?.textContent).toBe('实时传输中');
  });

  it('should cancel reconnect timer and stop retrying when drawer is closed', async () => {
    vi.useFakeTimers();
    try {
      const { container } = render(LogTerminal, {
        props: {
          open: true,
          jobId: 'job-recon-cancel',
          status: 'RUNNING',
        },
      });

      await vi.advanceTimersByTimeAsync(10);
      expect(MockWebSocket.instances.length).toBe(1);
      const ws1 = MockWebSocket.instances[0];
      ws1.onmessage?.({ data: 'Running output\n' });
      await vi.advanceTimersByTimeAsync(10);

      // 异常断开
      ws1.readyState = 3;
      ws1.onclose?.({ code: 1006 } as any);
      await vi.advanceTimersByTimeAsync(10);
      expect(container.querySelector('.footer-hint')?.textContent).toContain('自动重试');

      // 关闭抽屉 (点击右上角关闭按钮)
      const closeBtn = container.querySelector('.close-btn') as HTMLElement;
      expect(closeBtn).not.toBeNull();
      await fireEvent.click(closeBtn);
      await vi.advanceTimersByTimeAsync(10);

      // 即使推进 5 秒，也不会发起任何新的重连
      await vi.advanceTimersByTimeAsync(5000);
      expect(MockWebSocket.instances.length).toBe(1);
    } finally {
      vi.useRealTimers();
    }
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

    // 1. 点击操作栏 "新建任务" 按钮
    const runBtn = Array.from(container.querySelectorAll('.action-group button')).find(b => b.textContent?.includes('新建任务')) as HTMLElement;
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

  it('should render table with fixed-table class and calibrated column widths for anti-jitter', () => {
    const { container } = render(JobsView, {
      props: { jobs: mockJobs, nodes: mockNodes },
    });

    const table = container.querySelector('table.data-table.fixed-table');
    expect(table).not.toBeNull();

    const ths = container.querySelectorAll('thead th');
    expect(ths.length).toBe(9);
    expect(ths[0].getAttribute('style')).toContain('width: 120px'); // ID
    expect(ths[4].getAttribute('style')).toContain('width: 95px');  // CPU
    expect(ths[5].getAttribute('style')).toContain('width: 85px');  // 内存
    expect(ths[6].getAttribute('style')).toContain('width: 95px');  // 时长
    expect(ths[8].getAttribute('style')).toContain('width: 140px'); // 操作
  });

  it('should render high-load metrics with tabular-nums and guarantee stable slot 1 position for log button', () => {
    const highLoadJobs: JobInfo[] = [
      {
        id: 'job-heavy-1',
        name: 'cuda-training',
        node: 'node-A',
        command: 'python train_distributed.py --world_size 8',
        status: 'RUNNING',
        pid: 2001,
        start_time: new Date(Date.now() - 7200000).toISOString(), // 2 小时前
        metrics: {
          cpu_percent: 2800.0,
          memory_mb: 14900,
        },
      },
      {
        id: 'job-done-1',
        name: 'quick-eval',
        node: 'node-A',
        command: 'echo done',
        status: 'COMPLETED',
        pid: 2002,
        start_time: new Date(Date.now() - 3600000).toISOString(),
        end_time: new Date(Date.now() - 3590000).toISOString(),
      },
    ];

    const { container } = render(JobsView, {
      props: { jobs: highLoadJobs, nodes: mockNodes },
    });

    const rows = container.querySelectorAll('tbody tr');
    expect(rows.length).toBe(2);

    // 1. 验证高负荷 CPU (2800.0%) 和内存 (14900M) 正确渲染且具备 tabular-nums 防抖
    const runningRow = rows[0];
    const cpuCell = runningRow.querySelector('td:nth-child(5)');
    expect(cpuCell?.textContent?.trim()).toBe('2800.0%');
    expect(cpuCell?.classList.contains('tabular-nums')).toBe(true);

    const memCell = runningRow.querySelector('td:nth-child(6)');
    expect(memCell?.textContent?.trim()).toBe('14900M');
    expect(memCell?.classList.contains('tabular-nums')).toBe(true);

    // 2. 验证已完成任务指标展示为 '-'
    const doneRow = rows[1];
    expect(doneRow.querySelector('td:nth-child(5)')?.textContent?.trim()).toBe('-');
    expect(doneRow.querySelector('td:nth-child(6)')?.textContent?.trim()).toBe('-');

    // 3. 验证操作列槽位布局稳定性 (Slot 1 绝对位置守恒，彻底消除日志按钮瞬移)
    const runningActions = runningRow.querySelector('.row-actions');
    expect(runningActions).not.toBeNull();
    const runningBtns = runningActions?.querySelectorAll('button');
    expect(runningBtns?.length).toBe(2);
    // 槽位 1 永远是 [日志] 按钮
    expect(runningBtns?.[0].textContent?.trim()).toBe('日志');
    // 槽位 2 为 [终止] 按钮
    expect(runningBtns?.[1].textContent?.trim()).toBe('终止');

    const doneActions = doneRow.querySelector('.row-actions');
    expect(doneActions).not.toBeNull();
    const doneBtns = doneActions?.querySelectorAll('button');
    expect(doneBtns?.length).toBe(1);
    // 槽位 1 同样是 [日志] 按钮（绝对位置与 running 行严格垂直对齐）
    expect(doneBtns?.[0].textContent?.trim()).toBe('日志');
  });
});

