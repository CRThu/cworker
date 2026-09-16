// web/src/test/components.test.ts - Svelte 关键组件集成测试
import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import KillConfirmModal from '../lib/components/KillConfirmModal.svelte';
import Sparkline from '../lib/components/Sparkline.svelte';
import NodesView from '../lib/components/NodesView.svelte';
import AddNodeModal from '../lib/components/AddNodeModal.svelte';
import CleanJobsModal from '../lib/components/CleanJobsModal.svelte';
import RunJobModal from '../lib/components/RunJobModal.svelte';
import MyCardModal from '../lib/components/MyCardModal.svelte';
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

  it('should format used and total memory in usedG / totalG format (e.g. 16.0G / 32.0G)', () => {
    const { container } = render(NodesView, {
      props: {
        nodes: mockNodes,
        overview: {
          nodes_count: 2,
          online_count: 1,
          active_jobs: 2,
          avg_cpu: 15.0,
          total_free_mem_mb: 16384,
          total_mem_mb: 32768,
          nodes: mockNodes,
          local_card: { name: 'local', ip: '127.0.0.1', port: 19000, token: '' },
        },
      },
    });

    const statCards = container.querySelectorAll('.stat-card');
    const memCard = Array.from(statCards).find(c => c.querySelector('.stat-label')?.textContent?.trim() === '内存');
    expect(memCard).toBeDefined();
    // mockNodes has 1 ONLINE node with mem_total_mb: 32768, mem_free_mb: 16384 -> used 16384MB = 16.0G / 32.0G
    expect(memCard?.querySelector('.stat-value')?.textContent?.trim()).toBe('16.0G / 32.0G');
    expect(memCard?.querySelector('.stat-sub')?.textContent?.trim()).toBe('占用 / 总量');
  });

  it('should format CPU stat card in used% / total% format (e.g. 400% / 2500%) when cores are available', () => {
    const multiCoreNodes: any[] = [
      {
        name: 'NODE-16C',
        address: '100.93.237.16:19000',
        status: 'ONLINE',
        active_jobs: 1,
        metrics: { cpu_percent: 15.0, cpu_cores: 16, mem_total_mb: 32768, mem_free_mb: 16384 },
      },
      {
        name: 'NODE-9C',
        address: '100.93.237.17:19000',
        status: 'ONLINE',
        active_jobs: 1,
        metrics: { cpu_percent: 17.78, cpu_cores: 9, mem_total_mb: 16384, mem_free_mb: 8192 },
      },
    ];

    const { container } = render(NodesView, {
      props: {
        nodes: multiCoreNodes,
        overview: {
          nodes_count: 2,
          online_count: 2,
          active_jobs: 2,
          avg_cpu: 16.0,
          total_cpu_cores: 25,
          total_used_cpu_percent: 400.0,
          total_free_mem_mb: 24576,
          total_mem_mb: 49152,
          nodes: multiCoreNodes,
          local_card: { name: 'local', ip: '127.0.0.1', port: 19000, token: '' },
        },
      },
    });

    const statCards = container.querySelectorAll('.stat-card');
    const cpuCard = Array.from(statCards).find(c => c.querySelector('.stat-label')?.textContent?.trim() === 'CPU');
    expect(cpuCard).toBeDefined();
    expect(cpuCard?.querySelector('.stat-value')?.textContent?.trim()).toBe('400% / 2500%');
    expect(cpuCard?.querySelector('.stat-sub')?.textContent?.trim()).toBe('负载 / 总量');
  });

  it('should fall back to single percentage and 在线均值 on CPU card when cores are not provided', () => {
    const legacyNodes: any[] = [
      {
        name: 'NODE-LEGACY',
        address: '100.93.237.16:19000',
        status: 'ONLINE',
        active_jobs: 0,
        metrics: { cpu_percent: 15.0, mem_total_mb: 8192, mem_free_mb: 4096 },
      },
    ];

    const { container } = render(NodesView, {
      props: {
        nodes: legacyNodes,
        overview: {
          nodes_count: 1,
          online_count: 1,
          active_jobs: 0,
          avg_cpu: 15.0,
          total_free_mem_mb: 4096,
          total_mem_mb: 8192,
          nodes: legacyNodes,
          local_card: { name: 'local', ip: '127.0.0.1', port: 19000, token: '' },
        },
      },
    });

    const statCards = container.querySelectorAll('.stat-card');
    const cpuCard = Array.from(statCards).find(c => c.querySelector('.stat-label')?.textContent?.trim() === 'CPU');
    expect(cpuCard).toBeDefined();
    expect(cpuCard?.querySelector('.stat-value')?.textContent?.trim()).toBe('15.0%');
    expect(cpuCard?.querySelector('.stat-sub')?.textContent?.trim()).toBe('在线均值');
  });

  it('should render friendly empty state message when nodes list is empty', () => {
    const { container } = render(NodesView, {
      props: {
        nodes: [],
        knownNodes: [],
      },
    });

    const emptyCell = container.querySelector('.empty-cell');
    expect(emptyCell).not.toBeNull();
    expect(emptyCell?.textContent).toContain('暂无节点。点击右上角 "+ 添加节点" 添加。');
  });

  it('should render CPU as used% / total% when cpu_cores is available', () => {
    const { container } = render(NodesView, {
      props: {
        nodes: [
          {
            name: 'NODE-16C',
            address: '100.93.237.16:19000',
            status: 'ONLINE',
            active_jobs: 1,
            metrics: { cpu_percent: 20.0, cpu_cores: 16, mem_total_mb: 32768, mem_free_mb: 16384 },
          },
        ],
        knownNodes: [],
      },
    });

    const cpuCell = container.querySelector('tbody tr td:nth-child(4)');
    expect(cpuCell?.textContent).toContain('320% / 1600%');
  });

  it('should safely fall back to single percentage when cpu_cores is not provided (legacy worker)', () => {
    const { container } = render(NodesView, {
      props: {
        nodes: [
          {
            name: 'NODE-LEGACY',
            address: '100.93.237.16:19000',
            status: 'ONLINE',
            active_jobs: 0,
            metrics: { cpu_percent: 10.6, mem_total_mb: 8192, mem_free_mb: 4096 },
          },
        ],
        knownNodes: [],
      },
    });

    const cpuCell = container.querySelector('tbody tr td:nth-child(4)');
    expect(cpuCell?.textContent).toContain('10.6%');
    expect(cpuCell?.textContent).not.toContain('/');
  });

  it('should render dash for offline nodes without NaN or 0%/0%', () => {
    const { container } = render(NodesView, {
      props: {
        nodes: [
          {
            name: 'NODE-DOWN',
            address: '100.93.237.200:19000',
            status: 'OFFLINE',
            active_jobs: 0,
          },
        ],
        knownNodes: [],
      },
    });

    const cpuCell = container.querySelector('tbody tr td:nth-child(4)');
    const memCell = container.querySelector('tbody tr td:nth-child(5)');
    expect(cpuCell?.textContent?.trim()).toBe('-');
    expect(memCell?.textContent?.trim()).toBe('-');
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

  it('should dispatch submit event with trimmed values and close event on cancel', async () => {
    let submitted: any = null;
    let closed = false;
    const { container } = render(AddNodeModal, {
      props: {
        open: true,
        onSubmit: (p: any) => { submitted = p; },
        onClose: () => { closed = true; },
      },
    });

    const nameInput = container.querySelector('#add-node-name') as HTMLInputElement;
    const targetInput = container.querySelector('#add-node-target') as HTMLInputElement;
    const tokenInput = container.querySelector('#add-node-token') as HTMLInputElement;
    const form = container.querySelector('form') as HTMLFormElement;

    // 填写输入
    await fireEvent.input(nameInput, { target: { value: '  new-node  ' } });
    await fireEvent.input(targetInput, { target: { value: '  192.168.1.88:19000  ' } });
    await fireEvent.input(tokenInput, { target: { value: '  secret-token  ' } });

    await fireEvent.submit(form);

    expect(submitted).toEqual({
      name: 'new-node',
      target: '192.168.1.88:19000',
      token: 'secret-token',
    });

    // 点击取消按钮
    const cancelBtn = container.querySelector('.modal-footer .btn-secondary') as HTMLButtonElement;
    await fireEvent.click(cancelBtn);
    expect(closed).toBe(true);
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

  it('should dispatch submit event with selected nodes, days, and cleanAll options', async () => {
    let submittedPayload: any = null;
    const { container } = render(CleanJobsModal, {
      props: {
        open: true,
        nodes: [
          { name: 'worker-1', address: '127.0.0.1:19000', status: 'ONLINE', active_jobs: 0 },
          { name: 'worker-2', address: '127.0.0.1:19001', status: 'ONLINE', active_jobs: 1 },
        ],
        onSubmit: (payload: any) => {
          submittedPayload = payload;
        },
      },
    });

    const submitBtn = container.querySelector('button[type="submit"]') as HTMLButtonElement;
    expect(submitBtn).not.toBeNull();
    await fireEvent.click(submitBtn);

    // 默认：全部节点、days = 7、all = false
    expect(submittedPayload).toEqual({
      nodes: undefined,
      node: '',
      all: false,
      days: 7,
    });

    // 切换为清理全部已结束任务
    const radios = container.querySelectorAll('input[type="radio"]') as NodeListOf<HTMLInputElement>;
    expect(radios.length).toBe(2);
    await fireEvent.click(radios[1]);
    await fireEvent.click(submitBtn);

    expect(submittedPayload).toEqual({
      nodes: undefined,
      node: '',
      all: true,
      days: 0,
    });
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

  it('should randomize name and dispatch submit event on command submit', async () => {
    let submitted: any = null;
    let closed = false;
    const { container } = render(RunJobModal, {
      props: {
        open: true,
        nodes: [{ name: 'worker-1', address: '127.0.0.1:19000', status: 'ONLINE', active_jobs: 0 }],
        onSubmit: (p: any) => { submitted = p; },
        onClose: () => { closed = true; },
      },
    });

    // 点击随机生成按钮
    const randBtn = container.querySelector('.btn-text-sm') as HTMLButtonElement;
    await fireEvent.click(randBtn);

    // 输入命令
    const cmdInput = container.querySelector('#run-job-cmd') as HTMLInputElement;
    await fireEvent.input(cmdInput, { target: { value: 'python test.py' } });

    const form = container.querySelector('form') as HTMLFormElement;
    await fireEvent.submit(form);

    expect(submitted).not.toBeNull();
    expect(submitted.node).toBe('worker-1');
    expect(submitted.command).toBe('python test.py');

    // 点击取消
    const cancelBtn = container.querySelector('.modal-footer .btn-secondary') as HTMLButtonElement;
    await fireEvent.click(cancelBtn);
    expect(closed).toBe(true);
  });

  it('should support multi-node selection and dispatch multiple nodes in payload', async () => {
    let submitted: any = null;
    const { container, getByText } = render(RunJobModal, {
      props: {
        open: true,
        nodes: [
          { name: 'worker-1', address: '127.0.0.1:19000', status: 'ONLINE', active_jobs: 0 },
          { name: 'worker-2', address: '127.0.0.1:19001', status: 'ONLINE', active_jobs: 0 },
        ],
        onSubmit: (p: any) => { submitted = p; },
      },
    });

    // 验证 MultiSelect 组件已挂载
    const multiSelect = container.querySelector('.multiselect-container');
    expect(multiSelect).not.toBeNull();

    // 点击下拉展开
    const trigger = container.querySelector('.multiselect-trigger') as HTMLButtonElement;
    await fireEvent.click(trigger);

    // 点击 "全选"
    const selectAllBtn = getByText('全选');
    await fireEvent.click(selectAllBtn);

    // 输入命令
    const cmdInput = container.querySelector('#run-job-cmd') as HTMLInputElement;
    await fireEvent.input(cmdInput, { target: { value: 'pytest test/' } });

    // 提交
    const form = container.querySelector('form') as HTMLFormElement;
    await fireEvent.submit(form);

    expect(submitted).not.toBeNull();
    expect(submitted.nodes).toEqual(['worker-1', 'worker-2']);
    expect(submitted.command).toBe('pytest test/');
  });

  it('should disable submit button when no node is selected or command is empty', async () => {
    const { container, getByText } = render(RunJobModal, {
      props: {
        open: true,
        nodes: [
          { name: 'worker-1', address: '127.0.0.1:19000', status: 'ONLINE', active_jobs: 0 },
        ],
      },
    });

    const submitBtn = container.querySelector('button[type="submit"]') as HTMLButtonElement;
    // 命令为空时禁用
    expect(submitBtn.disabled).toBe(true);

    // 输入命令
    const cmdInput = container.querySelector('#run-job-cmd') as HTMLInputElement;
    await fireEvent.input(cmdInput, { target: { value: 'dir' } });
    expect(submitBtn.disabled).toBe(false);

    // 展开下拉并清空选中节点
    const trigger = container.querySelector('.multiselect-trigger') as HTMLButtonElement;
    await fireEvent.click(trigger);
    const clearBtn = getByText('清空');
    await fireEvent.click(clearBtn);

    // 节点为空时禁用
    expect(submitBtn.disabled).toBe(true);
  });

  it('should close RunJobModal on Escape key press', async () => {
    let closed = false;
    render(RunJobModal, {
      props: {
        open: true,
        nodes: [{ name: 'worker-1', address: '127.0.0.1:19000', status: 'ONLINE', active_jobs: 0 }],
        onClose: () => { closed = true; },
      },
    });

    await fireEvent.keyDown(window, { key: 'Escape' });
    expect(closed).toBe(true);
  });
});

describe('MyCardModal identity and pairing command', () => {
  it('should render hostname, default port, token, and pairing command', () => {
    const { container, getByText } = render(MyCardModal, {
      props: {
        open: true,
        card: {
          name: 'DESKTOP-4090',
          ip: '127.0.0.1',
          port: 19000,
          token: 'token-identity-xyz-1234567890',
        },
      },
    });

    expect(getByText('DESKTOP-4090')).toBeTruthy();
    expect(getByText('19000')).toBeTruthy();

    // 验证一键配对命令格式
    const cmdInput = container.querySelector('#my-card-cmd') as HTMLInputElement;
    expect(cmdInput).not.toBeNull();
    expect(cmdInput.value).toBe('cw node add DESKTOP-4090 --token token-identity-xyz-1234567890');
  });

  it('should fallback gracefully when token is empty', () => {
    const { container, getByText } = render(MyCardModal, {
      props: {
        open: true,
        card: {
          name: 'LOCAL-NODE',
          ip: '127.0.0.1',
          port: 19000,
          token: '',
        },
      },
    });

    expect(getByText('LOCAL-NODE')).toBeTruthy();
    const cmdInput = container.querySelector('#my-card-cmd') as HTMLInputElement;
    expect(cmdInput.value).toBe('cw node add LOCAL-NODE');
  });

  it('should copy token and pairing cmd to clipboard and close modal on cancel', async () => {
    let closed = false;
    const { container } = render(MyCardModal, {
      props: {
        open: true,
        card: {
          name: 'LOCAL-NODE',
          ip: '127.0.0.1',
          port: 19000,
          token: 'ed25519-token-secret-12345678901234567890',
        },
        onClose: () => { closed = true; },
      },
    });

    const writeTextMock = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText: writeTextMock } });
    (window as any).isSecureContext = true;

    // 点击复制 Token
    const copyTokenBtn = Array.from(container.querySelectorAll('button')).find(b => b.textContent?.includes('复制 Token')) as HTMLButtonElement;
    expect(copyTokenBtn).toBeDefined();
    await fireEvent.click(copyTokenBtn);
    expect(writeTextMock).toHaveBeenCalledWith('ed25519-token-secret-12345678901234567890');
    expect(copyTokenBtn.textContent).toContain('已复制');

    // 点击复制命令
    const copyCmdBtn = Array.from(container.querySelectorAll('button')).find(b => b.textContent?.includes('复制命令')) as HTMLButtonElement;
    expect(copyCmdBtn).toBeDefined();
    await fireEvent.click(copyCmdBtn);
    expect(writeTextMock).toHaveBeenCalledWith('cw node add LOCAL-NODE --token ed25519-token-secret-12345678901234567890');
    expect(copyCmdBtn.textContent).toContain('已复制');

    // 点击关闭
    const closeBtn = container.querySelector('.modal-footer .btn-secondary') as HTMLButtonElement;
    await fireEvent.click(closeBtn);
    expect(closed).toBe(true);
  });
});

