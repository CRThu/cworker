// web/src/test/ansi.test.ts - ANSI 转译单元测试
import { describe, it, expect } from 'vitest';
import { ansiToHtml, escapeHtml } from '../lib/utils/ansi';

describe('ansiToHtml', () => {
  it('should escape HTML characters safely', () => {
    const raw = '<script>alert("xss")</script> & "test"';
    const escaped = escapeHtml(raw);
    expect(escaped).not.toContain('<script>');
    expect(escaped).toContain('&lt;script&gt;');
    expect(escaped).toContain('&amp;');
    expect(escaped).toContain('&quot;');
  });

  it('should handle plain text with zero escape codes', () => {
    const raw = 'Hello World from cworker!';
    expect(ansiToHtml(raw)).toBe('Hello World from cworker!');
  });

  it('should convert standard 16-color ANSI codes to styled spans', () => {
    // \x1b[32m is green, \x1b[0m is reset
    const raw = '\x1b[32m[OK] Job dispatched\x1b[0m normal';
    const html = ansiToHtml(raw);
    expect(html).toContain('style="color: #4ade80;"');
    expect(html).toContain('[OK] Job dispatched');
    expect(html).toContain('normal');
  });

  it('should convert bold styles', () => {
    const raw = '\x1b[1mBold Text\x1b[0m';
    const html = ansiToHtml(raw);
    expect(html).toContain('font-weight: bold;');
    expect(html).toContain('Bold Text');
  });

  it('should handle multiple consecutive color changes and reset', () => {
    const raw = '\x1b[31mRed\x1b[34mBlue\x1b[0mPlain';
    const html = ansiToHtml(raw);
    expect(html).toContain('color: #f87171;');
    expect(html).toContain('color: #60a5fa;');
    expect(html).toContain('Plain');
  });

  it('should handle empty or null string gracefully', () => {
    expect(ansiToHtml('')).toBe('');
    expect(ansiToHtml(null as any)).toBe('');
  });
});
