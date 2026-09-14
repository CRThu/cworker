// web/src/lib/utils/format.ts - 格式化与高可用工具函数

export function formatBytes(bytes: number): string {
  if (!bytes || bytes <= 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  if (i >= sizes.length) return (bytes / Math.pow(k, sizes.length - 1)).toFixed(1) + ' ' + sizes[sizes.length - 1];
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
}

export function formatUptime(startTimeStr: string, endTimeStr?: string): string {
  if (!startTimeStr) return '-';
  const start = new Date(startTimeStr).getTime();
  const end = endTimeStr ? new Date(endTimeStr).getTime() : Date.now();
  const diffSec = Math.max(0, Math.floor((end - start) / 1000));

  const h = Math.floor(diffSec / 3600);
  const m = Math.floor((diffSec % 3600) / 60);
  const s = diffSec % 60;

  if (h > 0) return `${h}h ${m}m ${s}s`;
  if (m > 0) return `${m}m ${s}s`;
  return `${s}s`;
}

// Docker 风格高可读性随机名称生成器
const ADJECTIVES = [
  'swift', 'brave', 'sharp', 'calm', 'rapid', 'silent', 'bold', 'vivid',
  'bright', 'keen', 'agile', 'sturdy', 'noble', 'crisp', 'warm', 'cool'
];

const NOUNS = [
  'falcon', 'badger', 'otter', 'crane', 'lynx', 'tiger', 'eagle', 'cedar',
  'stream', 'beacon', 'canyon', 'forest', 'harbor', 'summit', 'orbit', 'matrix'
];

export function generateJobName(): string {
  const adj = ADJECTIVES[Math.floor(Math.random() * ADJECTIVES.length)];
  const noun = NOUNS[Math.floor(Math.random() * NOUNS.length)];
  const suffix = Math.floor(100 + Math.random() * 900);
  return `${adj}-${noun}-${suffix}`;
}

export async function copyToClipboard(text: string): Promise<boolean> {
  if (!text) return false;
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return true;
    }
    const input = document.createElement('textarea');
    input.value = text;
    input.style.position = 'fixed';
    input.style.opacity = '0';
    document.body.appendChild(input);
    input.select();
    const ok = document.execCommand('copy');
    document.body.removeChild(input);
    return ok;
  } catch {
    return false;
  }
}
