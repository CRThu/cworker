<!-- web/src/lib/components/MultiSelect.svelte - 多选节点下拉组件 (支持全选与全不选) -->
<script lang="ts">
  import { onMount } from 'svelte';

  export let options: string[] = [];
  export let selected: string[] = [];
  export let placeholder: string = '全部节点 (全集群)';
  export let allSelectedText: string = '';

  let open = false;
  let container: HTMLDivElement;

  $: isAllSelected = options.length > 0 && selected.length === options.length;
  $: isNoneSelected = selected.length === 0;

  function toggleOpen() {
    open = !open;
  }

  function toggleOption(opt: string) {
    if (selected.includes(opt)) {
      selected = selected.filter(s => s !== opt);
    } else {
      selected = [...selected, opt];
    }
  }

  function selectAll() {
    selected = [...options];
  }

  function deselectAll() {
    selected = [];
  }

  function handleClickOutside(event: MouseEvent) {
    if (container && !container.contains(event.target as Node)) {
      open = false;
    }
  }

  onMount(() => {
    document.addEventListener('click', handleClickOutside);
    return () => {
      document.removeEventListener('click', handleClickOutside);
    };
  });
</script>

<div class="multiselect-container" bind:this={container}>
  <button class="multiselect-trigger select" type="button" on:click={toggleOpen}>
    <span class="trigger-label">
      {#if isNoneSelected}
        {placeholder}
      {:else if isAllSelected}
        {allSelectedText || (placeholder.includes('全部节点') ? placeholder : `全部节点 (${selected.length} 台)`)}
      {:else}
        已选 {selected.length} 个节点: {selected.join(', ')}
      {/if}
    </span>
    <span class="arrow">{open ? '▲' : '▼'}</span>
  </button>

  {#if open}
    <div class="multiselect-dropdown">
      <div class="dropdown-actions">
        <button type="button" class="btn-text" on:click={selectAll} disabled={isAllSelected}>
          全选
        </button>
        <span class="divider">|</span>
        <button type="button" class="btn-text" on:click={deselectAll} disabled={isNoneSelected}>
          清空
        </button>
      </div>
      <div class="options-list">
        {#each options as opt}
          <label class="option-item">
            <input
              type="checkbox"
              checked={selected.includes(opt)}
              on:change={() => toggleOption(opt)}
            />
            <span class="option-name">{opt}</span>
          </label>
        {/each}
      </div>
    </div>
  {/if}
</div>

<style>
  .multiselect-container {
    position: relative;
    display: inline-block;
    min-width: 200px;
  }
  .multiselect-trigger {
    width: 100%;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    cursor: pointer;
    text-align: left;
    user-select: none;
  }
  .trigger-label {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 13px;
  }
  .arrow {
    font-size: 10px;
    color: var(--text-dim);
  }
  .multiselect-dropdown {
    position: absolute;
    top: calc(100% + 4px);
    left: 0;
    min-width: 220px;
    width: 100%;
    background: var(--bg-surface);
    border: 1px solid var(--border);
    border-radius: 6px;
    box-shadow: var(--shadow-md);
    z-index: 50;
    padding: 6px 0;
  }
  .dropdown-actions {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 4px 12px 6px;
    border-bottom: 1px solid var(--border-subtle);
    font-size: 12px;
  }
  .btn-text {
    background: none;
    border: none;
    color: var(--primary);
    cursor: pointer;
    font-size: 12px;
    padding: 0;
  }
  .btn-text:disabled {
    color: var(--text-dim);
    cursor: not-allowed;
  }
  .divider {
    color: var(--border);
  }
  .options-list {
    max-height: 200px;
    overflow-y: auto;
    padding: 4px 0;
  }
  .option-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 12px;
    cursor: pointer;
    font-size: 13px;
    color: var(--text-main);
  }
  .option-item:hover {
    background: var(--bg-hover);
  }
  .option-name {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
</style>
