// web/src/test/api.test.ts - api.ts 统一交互契约客户端全量单元测试
import { describe, it, expect, vi, beforeEach } from 'vitest';
import {
  fetchOverview,
  fetchNodes,
  addNode,
  removeNode,
  fetchJobs,
  runJob,
  killJob,
  cleanJobs,
  listDir,
  getRoots,
  makeDir,
  removePath,
  transfer,
} from '../lib/api';

describe('API Client Unit Tests', () => {
  let mockFetch: any;

  beforeEach(() => {
    mockFetch = vi.fn();
    (globalThis as any).fetch = mockFetch;
  });

  it('fetchOverview should parse JSON on 200 OK and throw on error', async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ version: '1.5.2', nodes_count: 3 }),
    });

    const ov = await fetchOverview();
    expect(ov.version).toBe('1.5.2');
    expect(mockFetch).toHaveBeenCalledWith('/api/ui/overview');

    mockFetch.mockResolvedValueOnce({
      ok: false,
      status: 500,
      text: () => Promise.resolve('internal error'),
    });
    await expect(fetchOverview()).rejects.toThrow('internal error');
  });

  it('fetchNodes should call /api/ui/nodes', async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ nodes: [{ name: 'node-1' }], known: [] }),
    });

    const res = await fetchNodes();
    expect(res.nodes.length).toBe(1);
    expect(mockFetch).toHaveBeenCalledWith('/api/ui/nodes');
  });

  it('addNode should send POST /api/ui/nodes and handle failure', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true });

    await addNode({ name: 'test-node', target: '127.0.0.1:19000', token: 'tok' });
    expect(mockFetch).toHaveBeenCalledWith('/api/ui/nodes', expect.objectContaining({
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: 'test-node', target: '127.0.0.1:19000', token: 'tok' }),
    }));

    mockFetch.mockResolvedValueOnce({
      ok: false,
      text: () => Promise.resolve('node already exists'),
    });
    await expect(addNode({ name: 'dup', target: '1.1.1.1' })).rejects.toThrow('node already exists');
  });

  it('removeNode should send DELETE /api/ui/nodes?name=...', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true });

    await removeNode('my node');
    expect(mockFetch).toHaveBeenCalledWith('/api/ui/nodes?name=my%20node', expect.objectContaining({
      method: 'DELETE',
    }));

    mockFetch.mockResolvedValueOnce({
      ok: false,
      text: () => Promise.resolve('node not found'),
    });
    await expect(removeNode('missing')).rejects.toThrow('node not found');
  });

  it('fetchJobs should format query string with optional node and status', async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve([{ id: 'job-1' }]),
    });

    const jobs = await fetchJobs('worker-1', 'RUNNING');
    expect(jobs.length).toBe(1);
    expect(mockFetch).toHaveBeenCalledWith('/api/ui/jobs?node=worker-1&status=RUNNING');
  });

  it('runJob should send POST /api/ui/jobs/run', async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ id: 'job-run-1' }),
    });

    const res = await runJob({ node: 'worker-1', command: 'echo 1' });
    expect(res.id).toBe('job-run-1');
  });

  it('killJob should send POST /api/ui/jobs/kill', async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ id: 'job-1', status: 'STOPPED' }),
    });

    const res = await killJob('job-1', 'worker-1');
    expect(res.status).toBe('STOPPED');
    expect(mockFetch).toHaveBeenCalledWith('/api/ui/jobs/kill', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ job_id: 'job-1', node: 'worker-1' }),
    }));
  });

  it('cleanJobs should send POST /api/ui/jobs/clean', async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ 'node-1': { cleaned_count: 5 } }),
    });

    const res = await cleanJobs({ node: 'node-1', days: 7, all: false });
    expect(res['node-1'].cleaned_count).toBe(5);
  });

  it('listDir and getRoots should format query strings properly', async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve([{ name: 'test.txt', is_dir: false }]),
    });
    const files = await listDir('node-1', 'D:/work');
    expect(files.length).toBe(1);
    expect(mockFetch).toHaveBeenCalledWith('/api/ui/fs/ls?node=node-1&path=D%3A%2Fwork');

    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve(['C:/', 'D:/']),
    });
    const roots = await getRoots('node-1');
    expect(roots).toEqual(['C:/', 'D:/']);
    expect(mockFetch).toHaveBeenCalledWith('/api/ui/fs/roots?node=node-1');
  });

  it('makeDir and removePath should send POST and handle errors', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true });
    await makeDir('node-1', 'D:/newdir');
    expect(mockFetch).toHaveBeenCalledWith('/api/ui/fs/mkdir', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ node: 'node-1', path: 'D:/newdir' }),
    }));

    mockFetch.mockResolvedValueOnce({
      ok: false,
      text: () => Promise.resolve('cannot create dir'),
    });
    await expect(makeDir('node-1', 'invalid')).rejects.toThrow('cannot create dir');

    mockFetch.mockResolvedValueOnce({ ok: true });
    await removePath('node-1', 'D:/newdir', true);
    expect(mockFetch).toHaveBeenCalledWith('/api/ui/fs/rm', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ node: 'node-1', path: 'D:/newdir', recursive: true }),
    }));
  });

  it('transfer without progress callback should send non-streaming request', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true });

    await transfer({
      src_node: '',
      src_path: 'C:/a.txt',
      dst_node: 'worker-1',
      dst_path: 'D:/a.txt',
      recursive: false,
      concurrency: 8,
    });

    expect(mockFetch).toHaveBeenCalledWith('/api/ui/fs/transfer', expect.objectContaining({
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
    }));
  });

  it('transfer with streaming should parse NDJSON frames and handle errors defensively', async () => {
    // 模拟服务端下发的 NDJSON 流式数据包 (包含 progress、不合法行、以及 done)
    const ndjsonContent = [
      '{"type":"progress","percent":30,"total_bytes":1000,"transferred_bytes":300}',
      'INVALID_JSON_LINE_THAT_SHOULD_NOT_CRASH',
      '{"type":"progress","percent":80,"total_bytes":1000,"transferred_bytes":800}',
      '{"type":"done","percent":100}',
    ].join('\n') + '\n';

    const encoder = new TextEncoder();
    const stream = new ReadableStream({
      start(controller) {
        controller.enqueue(encoder.encode(ndjsonContent));
        controller.close();
      },
    });

    mockFetch.mockResolvedValueOnce({
      ok: true,
      body: stream,
    });

    const receivedFrames: any[] = [];
    await transfer(
      {
        src_node: '',
        src_path: 'C:/test.txt',
        dst_node: 'worker-1',
        dst_path: 'D:/test.txt',
        recursive: false,
        concurrency: 8,
      },
      (frame) => {
        receivedFrames.push(frame);
      }
    );

    expect(receivedFrames.length).toBe(3);
    expect(receivedFrames[0].percent).toBe(30);
    expect(receivedFrames[1].percent).toBe(80);
    expect(receivedFrames[2].type).toBe('done');
  });

  it('transfer with streaming error frame should reject with error message', async () => {
    const ndjsonContent = '{"type":"error","error":"disk full"}\n';
    const encoder = new TextEncoder();
    const stream = new ReadableStream({
      start(controller) {
        controller.enqueue(encoder.encode(ndjsonContent));
        controller.close();
      },
    });

    mockFetch.mockResolvedValueOnce({
      ok: true,
      body: stream,
    });

    await expect(
      transfer(
        {
          src_node: '',
          src_path: 'C:/test.txt',
          dst_node: 'worker-1',
          dst_path: 'D:/test.txt',
          recursive: false,
          concurrency: 8,
        },
        () => {}
      )
    ).rejects.toThrow('disk full');
  });
});
