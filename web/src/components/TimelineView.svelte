<script>
  import TimelineClip from './TimelineClip.svelte';
  import { format, hasId } from '../lib/timeline.js';
  import { ZOOM } from '../lib/constants.js';
  import { stepZoomLevel } from '../lib/ui.js';

  let {
    playing,
    snapEnabled,
    linkedMoveEnabled,
    showIngestPanel,
    pendingJobCount,
    failedJobCount,
    zoomLevel = $bindable(),
    trackRows,
    tickMarks,
    markers,
    regions,
    wallclockMs,
    timelineWidthPx,
    marqueeState,
    owner,
    visibleTrackIds,
    activeAudioIds,
    blockStyle,
    markerStyle,
    regionStyle,
    marqueeStyle,
    isClipSelected,
    isClipDragging,
    annotationAuthor,
    registerTimelineNodes,
    ontoggleplay,
    onaddmarker,
    onaddregion,
    ontogglesnap,
    ontogglelink,
    ontogglejobs,
    onmoveorder,
    ontogglecollapse,
    onrename,
    ontoggleperspectiveview,
    ontoggleperspectiveaudio,
    ontoggletrack,
    onplayheaddrag,
    onlanepointerdown,
    onclipdrag,
    onannotationopen,
    ondeleteclip
  } = $props();

  let viewport = $state(null);
  let canvas = $state(null);

  $effect(() => {
    registerTimelineNodes?.(viewport, canvas);
    return () => registerTimelineNodes?.(null, null);
  });

  function nextZoomLevel(direction) {
    return stepZoomLevel(zoomLevel, direction);
  }
</script>

<section class="timeline-section">
  <div class="timeline-toolbar">
    <div class="tbar-l">
      <button class="tbar-play-btn {playing ? 'tbar-pause' : 'tbar-play'}" onclick={ontoggleplay}>{playing ? 'Pause' : 'Play'}</button>
      <button class="tbar-tool-btn" onclick={onaddmarker} title="Marker (M)">
        <svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" aria-hidden="true" role="img" style="color: rgb(228, 171, 0);" width="10" height="10" viewBox="0 0 24 24"><path fill="currentColor" d="M22 10.1c.1-.5-.3-1.1-.8-1.1l-5.7-.8L12.9 3c-.1-.2-.2-.3-.4-.4c-.5-.3-1.1-.1-1.4.4L8.6 8.2L2.9 9q-.45 0-.6.3c-.4.4-.4 1 0 1.4l4.1 4l-1 5.7c0 .2 0 .4.1.6c.3.5.9.7 1.4.4l5.1-2.7l5.1 2.7c.1.1.3.1.5.1h.2c.5-.1.9-.6.8-1.2l-1-5.7l4.1-4c.2-.1.3-.3.3-.5"></path></svg>Marker
      </button>
      <button class="tbar-tool-btn" onclick={onaddregion} title="Region (R)">
        <svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" aria-hidden="true" role="img" style="color: rgb(128, 82, 246);" width="10" height="10" viewBox="0 0 24 24"><path fill="currentColor" fill-rule="evenodd" d="M3 6a3 3 0 0 0-3 3v7a3 3 0 0 0 3 3h18a3 3 0 0 0 3-3V9a3 3 0 0 0-3-3zm6 2H7v5a1 1 0 1 1-2 0V8H3a1 1 0 0 0-1 1v7a1 1 0 0 0 1 1h18a1 1 0 0 0 1-1V9a1 1 0 0 0-1-1h-2v3a1 1 0 1 1-2 0V8h-2v5a1 1 0 1 1-2 0V8h-2v3a1 1 0 1 1-2 0z" clip-rule="evenodd"></path></svg>Region
      </button>
      <button class="tbar-tool-btn {snapEnabled ? 'tbar-active' : ''}" onclick={ontogglesnap} title="Snap clip edges to playhead, markers, regions, and other clip edges">Snap</button>
      <button class="tbar-tool-btn {linkedMoveEnabled ? 'tbar-active' : ''}" onclick={ontogglelink} title="Move linked video/audio clips together">Link</button>
      <div class="tbar-sep"></div>
      <button class="tbar-tool-btn {showIngestPanel ? 'tbar-active' : ''}" onclick={() => ontogglejobs?.()}>
        Jobs {pendingJobCount > 0 ? `(${pendingJobCount})` : ''}{failedJobCount > 0 ? ` ⚠${failedJobCount}` : ''}
      </button>
    </div>
    <div class="tbar-r">
      <div class="zoom-controls">
        <button class="tbar-tool-btn" onclick={() => zoomLevel = nextZoomLevel(-1)} title="Zoom out (−)">−</button>
        <span class="zoom-label" title="Click to reset" onclick={() => zoomLevel = ZOOM.default} role="button" tabindex="0" onkeydown={(event) => event.key === 'Enter' && (zoomLevel = ZOOM.default)}>{Math.round(zoomLevel * ZOOM.percentScale)}%</span>
        <button class="tbar-tool-btn" onclick={() => zoomLevel = nextZoomLevel(1)} title="Zoom in (+)">+</button>
      </div>
      <span class="tbar-hint">Shift-drag = marquee · Delete = remove selection</span>
    </div>
  </div>

  <div class="timeline-body">
    <div class="label-rail">
      <div class="label-corner"></div>
      {#each trackRows as row}
        {#if row.type === 'perspective'}
          <div class="label-persp-row">
            <div class="persp-controls">
              <div class="persp-order">
                <button class="mini-btn" onclick={(event) => { event.stopPropagation(); onmoveorder?.('perspective', row.id, -1); }}>▲</button>
                <button class="mini-btn" onclick={(event) => { event.stopPropagation(); onmoveorder?.('perspective', row.id, 1); }}>▼</button>
              </div>
              <button class="collapse-btn" onclick={() => ontogglecollapse?.(row.id)}>{row.group.collapsed ? '▸' : '▾'}</button>
              <button class="persp-name-btn" title="Dbl-click to rename" ondblclick={() => onrename?.('perspective', row.id, row.perspectiveName)}>{row.perspectiveName}</button>
              <div class="persp-toggles">
                <button class="toggle-btn {row.group.viewEnabled ? 'tog-v-on' : ''}" aria-pressed={row.group.viewEnabled} title="Show/hide in grid" onclick={() => ontoggleperspectiveview?.(row.group)}>V</button>
                <button class="toggle-btn {row.group.audioEnabled ? 'tog-a-on' : ''}" aria-pressed={row.group.audioEnabled} title="Enable/disable audio" onclick={() => ontoggleperspectiveaudio?.(row.group)}>A</button>
              </div>
            </div>
          </div>
        {:else if row.type === 'collapsed'}
          <div class="label-track-row label-collapsed">
            <div class="track-head-inner">
              <span class="track-label-name">{row.perspectiveName}</span>
              <span class="track-label-sub muted">collapsed</span>
            </div>
          </div>
        {:else}
          <div class="label-track-row label-{row.kind}">
            <div class="track-head-inner">
              <div class="track-order-btns">
                <button class="mini-btn" onclick={(event) => { event.stopPropagation(); onmoveorder?.('track', row.id, -1, row.perspectiveName); }}>▲</button>
                <button class="mini-btn" onclick={(event) => { event.stopPropagation(); onmoveorder?.('track', row.id, 1, row.perspectiveName); }}>▼</button>
              </div>
              <div class="track-names">
                <span class="track-label-name">{row.trackName}</span>
                <span class="track-label-sub muted">{row.perspectiveName}</span>
              </div>
              {#if row.kind === 'video'}
                <button class="toggle-btn {hasId(visibleTrackIds, row.id) ? 'tog-v-on' : ''}" onclick={() => ontoggletrack?.('video', row.id)} title="Show in grid">V</button>
              {:else}
                <button class="toggle-btn {hasId(activeAudioIds, row.id) ? 'tog-a-on' : ''}" onclick={() => ontoggletrack?.('audio', row.id)} title="Enable audio">A</button>
              {/if}
            </div>
          </div>
        {/if}
      {/each}
    </div>

    <div class="lane-scroll" bind:this={viewport}>
      <div class="lane-canvas" bind:this={canvas} style="width:{timelineWidthPx}px">
        {#if marqueeState}<div class="marquee-box" style={marqueeStyle?.(marqueeState)}></div>{/if}
        <div class="ruler" onpointerdown={onplayheaddrag}>
          {#each tickMarks as tick}
            <div class="tick" style="left:{blockStyle.msToPx(tick)}px"><span>{format(tick)}</span></div>
          {/each}
          {#each markers as marker}
            <button class="marker-pin" style={markerStyle?.(marker)} title="{marker.label} – {annotationAuthor?.(marker)}" onpointerdown={(event) => onannotationopen?.('marker', marker, event)}>◆</button>
          {/each}
          {#each regions as region}
            <button class="region-band" style={regionStyle?.(region)} title="{region.label} – {annotationAuthor?.(region)}" onpointerdown={(event) => onannotationopen?.('region', region, event)}></button>
          {/each}
        </div>
        <div class="playhead" style="left:{blockStyle.msToPx(wallclockMs)}px" onpointerdown={onplayheaddrag}><span></span></div>

        {#each trackRows as row}
          {#if row.type === 'perspective'}
            <div class="lane-persp-row">
              <div class="persp-lane-inner">
                <span class="persp-lane-meta muted">{row.group.videoTracks.length}V · {row.group.audioTracks.length}A</span>
              </div>
            </div>
          {:else if row.type === 'collapsed'}
            <div class="lane-track-row lane-collapsed">
              <div class="clip-lane" onpointerdown={onlanepointerdown}>
                {#each row.clips as clip (clip.clipId)}
                  <TimelineClip
                    {clip}
                    summary
                    selected={isClipSelected?.(clip)}
                    dragging={isClipDragging?.(clip)}
                    {linkedMoveEnabled}
                    {owner}
                    blockStyle={blockStyle.clip}
                    ondragstart={onclipdrag}
                    ondelete={ondeleteclip}
                  />
                {/each}
              </div>
            </div>
          {:else}
            <div class="lane-track-row lane-{row.kind}">
              <div class="clip-lane" onpointerdown={onlanepointerdown}>
                {#each row.clips as clip (clip.clipId)}
                  <TimelineClip
                    {clip}
                    selected={isClipSelected?.(clip)}
                    dragging={isClipDragging?.(clip)}
                    {linkedMoveEnabled}
                    {owner}
                    blockStyle={blockStyle.clip}
                    ondragstart={onclipdrag}
                    ondelete={ondeleteclip}
                  />
                {/each}
              </div>
            </div>
          {/if}
        {:else}
          <div class="empty-lane">Add footage from the left panel. Prepared review media is generated automatically.</div>
        {/each}
      </div>
    </div>
  </div>
</section>
