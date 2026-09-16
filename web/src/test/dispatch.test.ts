// web/src/test/dispatch.test.ts - 多节点任务调度并发下发与请求断言单元测试
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { dispatchJob } from '../lib/api';

describe('dispatchJob multi-node dispatch and request verification', () => {
  let mockFetch: any;

  beforeEach(() => {
    mockFetch = vi.fn();
    (globalThis as any).fetch = mockFetch;
  });

  it('should dispatch multiple independent HTTP requests matching all target nodes', async () => {
    // 模拟服务端正常响应每个节点的任务生成
    mockFetch.mockImplementation(async (_url: string, init: any) => {
      const body = JSON.parse(init.body);
      return {
        ok: true,
        json: () =>
          Promise.resolve({
            id: `job-${body.node}-101`,
            node: body.node,
            name: body.name,
            command: body.command,
            status: 'RUNNING',
            pid: 1234,
            start_time: new Date().toISOString(),
          }),
      };
    });

    const targetNodes = ['DESKTOP-4090', 'LAB-SERVER-01', 'TEST-BOX'];
    const result = await dispatchJob({
      nodes: targetNodes,
      name: 'batch-eval',
      command: 'python run_benchmark.py',
      dir: 'D:/workspace',
    });

    // 关键断言 1：验证底层是否精准下发了 3 次独立的 HTTP 请求
    expect(mockFetch).toHaveBeenCalledTimes(3);

    // 关键断言 2：逐一核对每次调用的 URL 和请求体参数
    for (let i = 0; i < targetNodes.length; i++) {
      const [url, init] = mockFetch.mock.calls[i];
      expect(url).toBe('/api/ui/jobs/run');
      expect(init.method).toBe('POST');
      const reqBody = JSON.parse(init.body);
      expect(reqBody.node).toBe(targetNodes[i]);
      expect(reqBody.name).toBe('batch-eval');
      expect(reqBody.command).toBe('python run_benchmark.py');
      expect(reqBody.dir).toBe('D:/workspace');
    }

    // 关键断言 3：验证返回结果中的 3 个任务信息
    expect(result.succeeded.length).toBe(3);
    expect(result.failed.length).toBe(0);
    expect(result.succeeded.map(s => s.node)).toEqual(targetNodes);
    expect(result.succeeded.map(s => s.id)).toEqual([
      'job-DESKTOP-4090-101',
      'job-LAB-SERVER-01-101',
      'job-TEST-BOX-101',
    ]);
  });

  it('should isolate errors when some nodes succeed and others fail (部分失败容错断言)', async () => {
    // 模拟 worker-1 成功，worker-2 失败 (500 Internal Error)
    mockFetch.mockImplementation(async (_url: string, init: any) => {
      const body = JSON.parse(init.body);
      if (body.node === 'worker-1') {
        return {
          ok: true,
          json: () =>
            Promise.resolve({
              id: 'job-w1-ok',
              node: 'worker-1',
              command: body.command,
              status: 'RUNNING',
              pid: 2001,
            }),
        };
      } else {
        return {
          ok: false,
          status: 500,
          text: () => Promise.resolve('connection refused: node offline'),
        };
      }
    });

    const result = await dispatchJob({
      nodes: ['worker-1', 'worker-2'],
      command: 'nvidia-smi',
    });

    // 两个节点的请求都必须下发
    expect(mockFetch).toHaveBeenCalledTimes(2);

    // worker-1 应进入 succeeded，worker-2 应进入 failed
    expect(result.succeeded.length).toBe(1);
    expect(result.succeeded[0].id).toBe('job-w1-ok');
    expect(result.succeeded[0].node).toBe('worker-1');

    expect(result.failed.length).toBe(1);
    expect(result.failed[0].node).toBe('worker-2');
    expect(result.failed[0].error).toContain('connection refused: node offline');
  });

  it('should issue only 1 request when dispatching with single node fallback', async () => {
    mockFetch.mockResolvedValue({
      ok: true,
      json: () =>
        Promise.resolve({
          id: 'job-single-001',
          node: 'worker-single',
          command: 'echo single',
          status: 'RUNNING',
          pid: 3001,
        }),
    });

    const result = await dispatchJob({
      node: 'worker-single',
      command: 'echo single',
    });

    expect(mockFetch).toHaveBeenCalledTimes(1);
    const [, init] = mockFetch.mock.calls[0];
    const reqBody = JSON.parse(init.body);
    expect(reqBody.node).toBe('worker-single');
    expect(result.succeeded.length).toBe(1);
    expect(result.succeeded[0].id).toBe('job-single-001');
  });
});
