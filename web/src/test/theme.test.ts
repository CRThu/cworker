// web/src/test/theme.test.ts - 黑白主题状态机与系统偏好单元测试
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { themeMode, cycleTheme, initTheme, isDarkEffective } from '../lib/stores/theme';

describe('theme store', () => {
  beforeEach(() => {
    localStorage.clear();
    document.documentElement.removeAttribute('data-theme');
    themeMode.set('system');

    // 模拟 matchMedia
    window.matchMedia = vi.fn().mockImplementation((query) => ({
      matches: query === '(prefers-color-scheme: dark)',
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
  });

  it('should initialize theme and update DOM attribute', () => {
    initTheme();
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark');
  });

  it('should cycle theme modes in order: system -> dark -> light -> system', () => {
    let current: string = '';
    themeMode.subscribe(v => current = v)();

    expect(current).toBe('system');

    cycleTheme();
    expect(localStorage.getItem('cworker-theme')).toBe('dark');

    cycleTheme();
    expect(localStorage.getItem('cworker-theme')).toBe('light');

    cycleTheme();
    expect(localStorage.getItem('cworker-theme')).toBe('system');
  });

  it('should correctly set light theme attribute on document', () => {
    initTheme();
    themeMode.set('light');
    expect(document.documentElement.getAttribute('data-theme')).toBe('light');
    let isDark = true;
    isDarkEffective.subscribe(v => isDark = v)();
    expect(isDark).toBe(false);
  });
});
