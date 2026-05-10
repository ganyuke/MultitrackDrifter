<script>
  import MediaCell from './MediaCell.svelte';

  let {
    gridPreset,
    monitorCells,
    started,
    playbackEnabled,
    registerNode,
    ongridpreset,
    onstart,
    onselect
  } = $props();
</script>

<section class="monitor-section">
  <div class="monitor-head">
    <span class="panel-label">Program monitor</span>
    <div class="grid-btns">
      {#each ['1x1','1x2','2x2','2x3'] as preset}
        <button class="grid-btn {gridPreset === preset ? 'grid-active' : ''}" onclick={() => ongridpreset?.(preset)}>{preset}</button>
      {/each}
    </div>
    {#if !started}
      <button class="start-review-btn" onclick={onstart}>▶ Start / unlock audio</button>
    {/if}
  </div>
  <div class="video-grid preset-{gridPreset}">
    {#each monitorCells as cell (cell.trackId)}
      <MediaCell {cell} {playbackEnabled} {registerNode} {onselect} />
    {:else}
      <div class="empty-monitor">Enable a video track in the timeline header.</div>
    {/each}
  </div>
</section>
