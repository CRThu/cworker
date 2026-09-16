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

import { render, fireEvent } from '@testing-library/svelte';
import ThemeToggle from '../lib/components/ThemeToggle.svelte';

describe('ThemeToggle component rendering and cycle interactions', () => {
  beforeEach(() => {
    themeMode.set('system');
    isDarkEffective.set(true);
  });

  it('should render system icon by default and cycle through dark and light on click', async () => {
    const { container } = render(ThemeToggle);
    const btn = container.querySelector('button') as HTMLButtonElement;
    expect(btn).not.toBeNull();
    expect(btn.getAttribute('title')).toContain('跟随系统');

    // 点击切换为 dark
    await fireEvent.click(btn);
    expect(btn.getAttribute('title')).toContain('强制暗色');

    // 点击切换为 light
    await fireEvent.click(btn);
    expect(btn.getAttribute('title')).toContain('强制亮色');

    // 点击切回 system
    await fireEvent.click(btn);
    expect(btn.getAttribute('title')).toContain('跟随系统');
  });
});
