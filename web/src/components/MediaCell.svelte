<script>
  import { attachHLS, fitVideoToCellAttachment } from '../playback.js';
  import { clipStatus, statusText } from '../lib/timeline.js';

  let { cell, playbackEnabled = true, registerNode, onselect } = $props();
  let video = $state(null);
  let attachedNode = null;
  let attachedUrl = '';
  let detachHls = () => {};

  function resetMediaElement(node) {
    if (!node) return;
    try { node.pause(); } catch (_) {}
    try { node.removeAttribute('src'); node.load(); } catch (_) {}
  }

  function detachCurrent() {
    detachHls();
    resetMediaElement(attachedNode);
    detachHls = () => {};
    attachedNode = null;
    attachedUrl = '';
  }

  function attachCurrent(node, url) {
    if (attachedNode === node && attachedUrl === url) return;
    detachCurrent();
    if (!node || !url) return;
    node.muted = true;
    detachHls = attachHLS(node, url);
    attachedNode = node;
    attachedUrl = url;
  }

  $effect(() => {
    const node = video;
    const trackId = cell.trackId;
    if (!node || !registerNode) return;
    registerNode(trackId, node);
    return () => registerNode(trackId, null);
  });

  $effect(() => {
    const node = video;
    const clip = cell.activeClip;
    const url = playbackEnabled ? (clip?.hlsURL || '') : '';
    attachCurrent(node, url);
    if (!node) return;
    node.dataset.activeClipId = url && clip ? String(clip.clipId) : '';
    if (!url) node.pause();
  });

  $effect(() => detachCurrent);
</script>

<div class="monitor-cell">
  <video muted playsinline preload="auto" bind:this={video} {@attach fitVideoToCellAttachment}></video>
  {#if cell.activeClip}
    {#if !cell.activeClip.hlsURL}
      <div class="cell-overlay status-overlay {clipStatus(cell.activeClip)}">{statusText(cell.activeClip)}</div>
    {/if}
    <button class="cell-label-btn" title="{cell.perspectiveName} / {cell.trackName}" onclick={() => onselect?.(cell.activeClip)}>
      <span class="cell-persp">{cell.perspectiveName}</span>
      <span class="cell-sep">/</span>
      <span class="cell-track">{cell.trackName}</span>
    </button>
  {:else}
    <div class="cell-overlay gap-overlay">Gap — {cell.perspectiveName}</div>
    <button class="cell-label-btn" title="{cell.perspectiveName} / {cell.trackName}">
      <span class="cell-persp">{cell.perspectiveName}</span>
      <span class="cell-sep">/</span>
      <span class="cell-track">{cell.trackName}</span>
    </button>
  {/if}
</div>
