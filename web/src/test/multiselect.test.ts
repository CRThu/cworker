// web/src/test/multiselect.test.ts - MultiSelect 节点多选下拉组件单元测试
import { describe, it, expect } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import MultiSelect from '../lib/components/MultiSelect.svelte';

describe('MultiSelect component', () => {
  const options = ['node-alpha', 'node-beta', 'node-gamma'];

  it('should render default placeholder when no options are selected', () => {
    const { getByText } = render(MultiSelect, {
      props: {
        options,
        selected: [],
        placeholder: '全部节点 (全集群)',
      },
    });

    expect(getByText('全部节点 (全集群)')).toBeTruthy();
  });

  it('should show selected count and names when specific items are selected', () => {
    const { getByText } = render(MultiSelect, {
      props: {
        options,
        selected: ['node-alpha', 'node-beta'],
      },
    });

    expect(getByText(/已选 2 个节点: node-alpha, node-beta/)).toBeTruthy();
  });

  it('should open dropdown and show all options when clicked', async () => {
    const { container, getByText } = render(MultiSelect, {
      props: {
        options,
        selected: [],
      },
    });

    const trigger = container.querySelector('.multiselect-trigger');
    expect(trigger).not.toBeNull();
    await fireEvent.click(trigger!);

    expect(container.querySelector('.multiselect-dropdown')).not.toBeNull();
    expect(getByText('node-alpha')).toBeTruthy();
    expect(getByText('node-beta')).toBeTruthy();
    expect(getByText('node-gamma')).toBeTruthy();
    expect(getByText('全选')).toBeTruthy();
    expect(getByText('清空')).toBeTruthy();
  });

  it('should render allSelectedText when all options are selected', () => {
    const { getByText } = render(MultiSelect, {
      props: {
        options,
        selected: ['node-alpha', 'node-beta', 'node-gamma'],
        placeholder: '请选择目标节点',
        allSelectedText: '全部节点 (全选)',
      },
    });

    expect(getByText('全部节点 (全选)')).toBeTruthy();
  });

  it('should render count fallback when all options selected and custom placeholder without allSelectedText', () => {
    const { getByText } = render(MultiSelect, {
      props: {
        options,
        selected: ['node-alpha', 'node-beta', 'node-gamma'],
        placeholder: '请选择目标节点',
      },
    });

    expect(getByText('全部节点 (3 台)')).toBeTruthy();
  });
});
