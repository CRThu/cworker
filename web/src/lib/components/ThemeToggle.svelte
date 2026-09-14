<!-- web/src/lib/components/ThemeToggle.svelte - 黑白主题切换矢量按钮 -->
<script lang="ts">
  import { Sun, Moon, Monitor } from 'lucide-svelte';
  import { themeMode, isDarkEffective, cycleTheme } from '../stores/theme';

  $: modeLabel = $themeMode === 'system'
    ? `跟随系统 (${$isDarkEffective ? '暗色' : '亮色'})`
    : ($themeMode === 'dark' ? '强制暗色' : '强制亮色');
</script>

<button
  class="btn-icon theme-toggle-btn"
  type="button"
  title="主题模式: {modeLabel} (点击切换)"
  on:click={cycleTheme}
  aria-label="切换主题"
>
  {#if $themeMode === 'system'}
    <Monitor size={16} />
  {:else if $themeMode === 'dark'}
    <Moon size={16} />
  {:else}
    <Sun size={16} />
  {/if}
</button>

<style>
  .theme-toggle-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 32px;
    height: 32px;
    border-radius: 6px;
    color: var(--text-muted);
    transition: all 0.15s ease;
  }
  .theme-toggle-btn:hover {
    color: var(--primary);
    background-color: var(--bg-hover);
  }
</style>
