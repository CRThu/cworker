// web/src/test/files.test.ts - 双栏文件管理器算法与交互单元测试
import { describe, it, expect } from 'vitest';
import type { FileItem } from '../lib/types';

// 复制待测算法
function sortFiles(items: FileItem[]): FileItem[] {
  return [...items].sort((a, b) => {
    if (a.is_dir && !b.is_dir) return -1;
    if (!a.is_dir && b.is_dir) return 1;
    return a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' });
  });
}

function resolveUpPath(currentPath: string): string {
  const trimmed = currentPath.replace(/[\\/]+$/, '');
  const lastSlash = Math.max(trimmed.lastIndexOf('/'), trimmed.lastIndexOf('\\'));
  if (lastSlash > 0) {
    let next = trimmed.slice(0, lastSlash);
    if (/^[A-Za-z]:$/.test(next)) next += '/';
    return next;
  } else if (lastSlash === 0) {
    return '/';
  } else if (/^[A-Za-z]:\/?$/.test(trimmed)) {
    return trimmed.endsWith('/') ? trimmed : trimmed + '/';
  } else {
    return 'C:/';
  }
}

function resolveDrillPath(basePath: string, dirName: string): string {
  const clean = basePath.replace(/[\\/]+$/, '');
  return clean ? `${clean}/${dirName}` : dirName;
}

describe('File Explorer sorting and navigation logic', () => {
  it('should sort directories strictly before files, and sort alphabetically within groups', () => {
    const rawFiles: FileItem[] = [
      { name: 'zebra.txt', size: 100, is_dir: false, mod_time: '' },
      { name: 'build', size: 0, is_dir: true, mod_time: '' },
      { name: 'apple.txt', size: 200, is_dir: false, mod_time: '' },
      { name: 'assets', size: 0, is_dir: true, mod_time: '' },
      { name: '01_doc.pdf', size: 50, is_dir: false, mod_time: '' },
    ];

    const sorted = sortFiles(rawFiles);

    // 目录必须排在最前
    expect(sorted[0].name).toBe('assets');
    expect(sorted[0].is_dir).toBe(true);
    expect(sorted[1].name).toBe('build');
    expect(sorted[1].is_dir).toBe(true);

    // 文件排在目录后面，且按字母/数字自然排序
    expect(sorted[2].name).toBe('01_doc.pdf');
    expect(sorted[2].is_dir).toBe(false);
    expect(sorted[3].name).toBe('apple.txt');
    expect(sorted[3].is_dir).toBe(false);
    expect(sorted[4].name).toBe('zebra.txt');
    expect(sorted[4].is_dir).toBe(false);
  });

  it('should correctly resolve parent directory paths on Windows drive systems', () => {
    expect(resolveUpPath('C:/Users/test/workspace')).toBe('C:/Users/test');
    expect(resolveUpPath('C:/Users/test')).toBe('C:/Users');
    expect(resolveUpPath('C:/Users')).toBe('C:/');
    expect(resolveUpPath('C:/')).toBe('C:/');
    expect(resolveUpPath('D:\\projects\\cworker')).toBe('D:/projects'.replace('/', '\\') /* or normalized */);
  });

  it('should correctly resolve directory drill paths', () => {
    expect(resolveDrillPath('C:/', 'data')).toBe('C:/data');
    expect(resolveDrillPath('C:/data', 'subfolder')).toBe('C:/data/subfolder');
    expect(resolveDrillPath('D:/workspace', 'dataset')).toBe('D:/workspace/dataset');
  });
});
