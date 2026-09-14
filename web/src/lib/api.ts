// web/src/lib/api.ts - 统一后端交互契约客户端
import type { OverviewData, NodeInfo, KnownNode, JobInfo, FileItem, TransferRequest } from './types';

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

export async function killJob(jobId: string): Promise<JobInfo> {
  const res = await fetch('/api/ui/jobs/kill', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ job_id: jobId }),
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

export async function transfer(req: TransferRequest): Promise<void> {
  const res = await fetch('/api/ui/fs/transfer', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  });
  if (!res.ok) {
    throw new Error(await res.text());
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

