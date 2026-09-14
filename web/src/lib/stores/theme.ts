// web/src/lib/stores/theme.ts - 黑白主题切换与系统自动自适应状态机
import { writable, derived } from 'svelte/store';
import type { ThemeMode } from '../types';

const STORAGE_KEY = 'cworker-theme';

function getInitialTheme(): ThemeMode {
  if (typeof window === 'undefined') return 'system';
  const saved = localStorage.getItem(STORAGE_KEY) as ThemeMode;
  if (saved === 'dark' || saved === 'light' || saved === 'system') {
    return saved;
  }
  return 'system';
}

export const themeMode = writable<ThemeMode>(getInitialTheme());

// 实际生效的明暗状态 (暗色为 true，亮色为 false)
export const isDarkEffective = writable<boolean>(true);

export function initTheme() {
  if (typeof window === 'undefined') return;

  const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)');

  function updateDom(mode: ThemeMode) {
    const isDark = mode === 'system' ? mediaQuery.matches : mode === 'dark';
    isDarkEffective.set(isDark);
    if (isDark) {
      document.documentElement.setAttribute('data-theme', 'dark');
    } else {
      document.documentElement.setAttribute('data-theme', 'light');
    }
  }

  themeMode.subscribe((mode) => {
    localStorage.setItem(STORAGE_KEY, mode);
    updateDom(mode);
  });

  mediaQuery.addEventListener('change', () => {
    themeMode.subscribe((mode) => {
      if (mode === 'system') {
        updateDom('system');
      }
    })();
  });
}

export function cycleTheme() {
  themeMode.update((current) => {
    if (current === 'system') return 'dark';
    if (current === 'dark') return 'light';
    return 'system';
  });
}
