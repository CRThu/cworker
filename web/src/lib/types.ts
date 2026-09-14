// web/src/lib/types.ts - 与 cworker Go protocol 严格对齐的强类型定义

export type NodeStatus = 'ONLINE' | 'OFFLINE';
export type JobStatus = 'RUNNING' | 'COMPLETED' | 'FAILED' | 'STOPPED';

export interface HostMetrics {
  cpu_percent: number;
  mem_total_mb: number;
  mem_free_mb: number;
}

export interface NodeInfo {
  name: string;
  address: string;
  status: NodeStatus;
  active_jobs: number;
  metrics?: HostMetrics;
}

export interface KnownNode {
  name: string;
  target: string;
  token?: string;
}

export interface JobMetrics {
  cpu_percent: number;
  memory_mb: number;
}

export interface JobInfo {
  id: string;
  name: string;
  node: string;
  command: string;
  dir?: string;
  status: JobStatus;
  pid: number;
  start_time: string;
  end_time?: string;
  exit_code?: number;
  metrics?: JobMetrics;
}

export interface LocalCardInfo {
  name: string;
  ip: string;
  port: number;
  token: string;
}

export interface OverviewData {
  version?: string;
  build_date?: string;
  nodes_count: number;
  online_count: number;
  active_jobs: number;
  avg_cpu: number;
  total_free_mem_mb: number;
  total_mem_mb?: number;
  nodes: NodeInfo[];
  local_card: LocalCardInfo;
}

export interface FileItem {
  name: string;
  size: number;
  is_dir: boolean;
  mod_time: string;
}

export interface TransferRequest {
  src_node: string;
  src_path: string;
  dst_node: string;
  dst_path: string;
  recursive: boolean;
  concurrency: number;
}

export interface TransferProgress {
  active: boolean;
  statusText: string;
  percent: number;
  finished: boolean;
  error?: string;
}

export type ThemeMode = 'system' | 'dark' | 'light';
