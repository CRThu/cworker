// web/src/lib/api.ts - 统一后端交互契约客户端
import type { OverviewData, NodeInfo, KnownNode, JobInfo, FileItem, TransferRequest, TransferStreamFrame } from './types';

async function handleResponse<T>(res: Response): Promise<T> {
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || `HTTP Error ${res.status}`);
  }
  return res.json() as Promise<T>;
}

export async function fetchOverview(): Promise<OverviewData> {
  const res = await fetch('/api/ui/overview');
  return handleResponse<OverviewData>(res);
}

export async function fetchNodes(): Promise<{ nodes: NodeInfo[]; known: KnownNode[] }> {
  const res = await fetch('/api/ui/nodes');
  return handleResponse<{ nodes: NodeInfo[]; known: KnownNode[] }>(res);
}

export async function addNode(data: { name: string; target: string; token?: string }): Promise<void> {
  const res = await fetch('/api/ui/nodes', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(data),
  });
  if (!res.ok) {
    throw new Error(await res.text());
  }
}

export async function removeNode(name: string): Promise<void> {
  const res = await fetch(`/api/ui/nodes?name=${encodeURIComponent(name)}`, {
    method: 'DELETE',
  });
  if (!res.ok) {
    throw new Error(await res.text());
  }
}

export async function fetchJobs(node?: string, status?: string): Promise<JobInfo[]> {
  const query = new URLSearchParams();
  if (node) query.set('node', node);
  if (status) query.set('status', status);
  const res = await fetch(`/api/ui/jobs?${query.toString()}`);
  return handleResponse<JobInfo[]>(res);
}

export async function runJob(data: {
  node: string;
  name?: string;
  dir?: string;
  command: string;
  token?: string;
}): Promise<JobInfo> {
  const res = await fetch('/api/ui/jobs/run', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(data),
  });
  return handleResponse<JobInfo>(res);
}

export interface DispatchResult {
  succeeded: JobInfo[];
  failed: { node: string; error: string }[];
}

// dispatchJob 支持单节点或多节点并发启动任务，收集成功与失败详情，确保错误隔离
export async function dispatchJob(data: {
  nodes?: string[];
  node?: string;
  name?: string;
  dir?: string;
  command: string;
  token?: string;
}): Promise<DispatchResult> {
  const targetNodes = data.nodes && data.nodes.length > 0
    ? data.nodes
    : [data.node || ''];

  const results = await Promise.allSettled(
    targetNodes.map(node =>
      runJob({
        node,
        name: data.name,
        dir: data.dir,
        command: data.command,
        token: data.token,
      })
    )
  );

  const succeeded: JobInfo[] = [];
  const failed: { node: string; error: string }[] = [];

  results.forEach((res, idx) => {
    const nodeName = targetNodes[idx] || 'local';
    if (res.status === 'fulfilled') {
      succeeded.push(res.value);
    } else {
      failed.push({
        node: nodeName,
        error: res.reason?.message || String(res.reason),
      });
    }
  });

  return { succeeded, failed };
}

export async function killJob(jobId: string, node?: string): Promise<JobInfo> {
  const res = await fetch('/api/ui/jobs/kill', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ job_id: jobId, node }),
  });
  return handleResponse<JobInfo>(res);
}

export async function cleanJobs(data: { node?: string; days: number; all: boolean }): Promise<any> {
  const res = await fetch('/api/ui/jobs/clean', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(data),
  });
  return handleResponse<any>(res);
}

export async function listDir(node: string, path: string): Promise<FileItem[]> {
  const query = new URLSearchParams();
  if (node) query.set('node', node);
  query.set('path', path || '.');
  const res = await fetch(`/api/ui/fs/ls?${query.toString()}`);
  return handleResponse<FileItem[]>(res);
}

export async function transfer(
  req: TransferRequest,
  onProgress?: (frame: TransferStreamFrame) => void
): Promise<void> {
  const url = onProgress ? '/api/ui/fs/transfer?stream=true' : '/api/ui/fs/transfer';
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  if (onProgress) {
    headers['Accept'] = 'application/x-ndjson';
  }

  const res = await fetch(url, {
    method: 'POST',
    headers,
    body: JSON.stringify(req),
  });

  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || `HTTP Error ${res.status}`);
  }

  if (!onProgress || !res.body) {
    return;
  }

  const reader = res.body.getReader();
  const decoder = new TextDecoder('utf-8');
  let buffer = '';

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split('\n');
    buffer = lines.pop() || '';

    for (const line of lines) {
      const trimmed = line.trim();
      if (!trimmed) continue;
      let frame: TransferStreamFrame;
      try {
        frame = JSON.parse(trimmed);
      } catch (err) {
        console.warn('Failed to parse transfer stream frame:', trimmed, err);
        continue;
      }
      if (frame.type === 'error') {
        throw new Error(frame.error || '传输失败');
      }
      onProgress(frame);
    }
  }

  const trailing = buffer.trim();
  if (trailing) {
    let frame: TransferStreamFrame;
    try {
      frame = JSON.parse(trailing);
    } catch (err) {
      console.warn('Failed to parse trailing transfer stream frame:', trailing, err);
      return;
    }
    if (frame.type === 'error') {
      throw new Error(frame.error || '传输失败');
    }
    onProgress(frame);
  }
}

export async function getRoots(node?: string): Promise<string[]> {
  const query = new URLSearchParams();
  if (node) query.set('node', node);
  const res = await fetch(`/api/ui/fs/roots?${query.toString()}`);
  return handleResponse<string[]>(res);
}

export async function makeDir(node: string, path: string): Promise<void> {
  const res = await fetch('/api/ui/fs/mkdir', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ node, path }),
  });
  if (!res.ok) {
    throw new Error(await res.text());
  }
}

export async function removePath(node: string, path: string, recursive: boolean = false): Promise<void> {
  const res = await fetch('/api/ui/fs/rm', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ node, path, recursive }),
  });
  if (!res.ok) {
    throw new Error(await res.text());
  }
}

