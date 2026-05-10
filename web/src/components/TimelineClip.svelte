<script>
  import { clipStatus, statusText, format, waveBars } from '../lib/timeline.js';

  let {
    clip,
    summary = false,
    selected = false,
    dragging = false,
    linkedMoveEnabled = false,
    owner = false,
    blockStyle,
    ondragstart,
    ondelete
  } = $props();
</script>

{#if dragging}
  <div class="clip-block {clip.kind} clip-ghost {summary ? 'clip-summary' : ''}" style={blockStyle?.(clip, true)}>
    {#if !summary}<span class="clip-title">{clip.displayName}</span>{/if}
  </div>
{/if}

<button
  data-clip-id={clip.clipId}
  class="clip-block {clip.kind} {clipStatus(clip)} {summary ? 'clip-summary' : ''}"
  class:clip-selected={selected}
  class:clip-dragging={dragging}
  class:clip-linked={!summary && clip.linkGroupId && linkedMoveEnabled}
  title={summary ? `${clip.displayName} · ${clip.kind}` : (clip.displayName || clip.trackName)}
  style={blockStyle?.(clip)}
  onpointerdown={(event) => ondragstart?.(event, clip)}
>
  {#if summary}
    {#if clip.kind === 'audio'}
      <span class="waveform">{#each waveBars(clip) as height}<i style="height:{height}%"></i>{/each}</span>
    {:else}
      <span class="video-stripe"></span>
    {/if}
  {:else}
    <span class="clip-title">{clip.displayName || clip.trackName}</span>
    {#if clipStatus(clip) !== 'success'}<span class="clip-badge {clipStatus(clip)}">{statusText(clip)}</span>{/if}
    {#if clip.linkGroupId}<span class="clip-link-badge">🔗</span>{/if}
    {#if clip.kind === 'audio'}
      <span class="waveform">{#each waveBars(clip) as height}<i style="height:{height}%"></i>{/each}</span>
    {:else}
      <span class="video-stripe"></span>
    {/if}
    <span class="clip-timecode">{format(clip.wallclockStartMs)} · {format(clip.durationMs)}</span>
    {#if owner}<span class="clip-del" role="button" tabindex="0" onpointerdown={(event) => event.stopPropagation()} onclick={(event) => { event.stopPropagation(); ondelete?.(clip); }}>×</span>{/if}
  {/if}
</button>
