// web/src/test/format.test.ts - 格式化与随机命名工具单元测试
import { describe, it, expect } from 'vitest';
import { formatBytes, formatUptime, generateJobName } from '../lib/utils/format';

describe('formatBytes', () => {
  it('should format 0 bytes correctly', () => {
    expect(formatBytes(0)).toBe('0 B');
    expect(formatBytes(-1)).toBe('0 B');
  });

  it('should format KB, MB, GB accurately', () => {
    expect(formatBytes(500)).toBe('500 B');
    expect(formatBytes(1024)).toBe('1 KB');
    expect(formatBytes(1024 * 1024)).toBe('1 MB');
    expect(formatBytes(1024 * 1024 * 1024)).toBe('1 GB');
    expect(formatBytes(2.5 * 1024 * 1024)).toBe('2.5 MB');
  });
});

describe('formatUptime', () => {
  it('should handle empty or missing startTime', () => {
    expect(formatUptime('')).toBe('-');
  });

  it('should format seconds, minutes, and hours', () => {
    const now = new Date();
    const tenSecAgo = new Date(now.getTime() - 10 * 1000).toISOString();
    const twoMinAgo = new Date(now.getTime() - 130 * 1000).toISOString();
    const twoHoursAgo = new Date(now.getTime() - 7300 * 1000).toISOString();

    expect(formatUptime(tenSecAgo, now.toISOString())).toBe('10s');
    expect(formatUptime(twoMinAgo, now.toISOString())).toBe('2m 10s');
    expect(formatUptime(twoHoursAgo, now.toISOString())).toBe('2h 1m 40s');
  });
});

describe('generateJobName', () => {
  it('should generate memorable names in adjective-noun-number format', () => {
    const name = generateJobName();
    expect(name).toMatch(/^[a-z]+-[a-z]+-\d{3}$/);
  });

  it('should generate distinct random names across calls', () => {
    const set = new Set();
    for (let i = 0; i < 20; i++) {
      set.add(generateJobName());
    }
    expect(set.size).toBeGreaterThanOrEqual(18);
  });
});
