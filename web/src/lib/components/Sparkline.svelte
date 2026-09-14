<!-- web/src/lib/components/Sparkline.svelte - 类似任务管理器的平滑折线走势图 -->
<script lang="ts">
  export let values: number[] = [];
  export let max: number = 100;
  export let color: string = '#f97316';
  export let width: number = 120;
  export let height: number = 28;

  // 补齐至少 2 个点以渲染线段
  $: safeValues = values && values.length > 0 ? values : [0, 0];
  $: effectiveMax = Math.max(max, ...safeValues, 1);

  $: points = safeValues.map((val, idx) => {
    const x = safeValues.length > 1 ? (idx / (safeValues.length - 1)) * width : width / 2;
    const y = height - (Math.min(val, effectiveMax) / effectiveMax) * (height - 4) - 2;
    return `${x.toFixed(1)},${y.toFixed(1)}`;
  });

  $: polylinePoints = points.join(' ');
  $: polygonPoints = safeValues.length > 1
    ? `0,${height} ${polylinePoints} ${width},${height}`
    : `0,${height} ${width},${height}`;

  // 唯一渐变 ID
  $: gradientId = `spark-grad-${Math.random().toString(36).slice(2, 8)}`;
</script>

<div class="sparkline-wrapper" style="width: {width}px; height: {height}px;">
  <svg {width} {height} viewBox="0 0 {width} {height}" class="sparkline-svg">
    <defs>
      <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
        <stop offset="0%" stop-color={color} stop-opacity="0.35" />
        <stop offset="100%" stop-color={color} stop-opacity="0.0" />
      </linearGradient>
    </defs>
    <polygon points={polygonPoints} fill="url(#{gradientId})" />
    <polyline
      points={polylinePoints}
      fill="none"
      stroke={color}
      stroke-width="1.5"
      stroke-linecap="round"
      stroke-linejoin="round"
    />
  </svg>
</div>

<style>
  .sparkline-wrapper {
    display: inline-flex;
    align-items: center;
    overflow: hidden;
    vertical-align: middle;
  }
  .sparkline-svg {
    display: block;
  }
</style>
