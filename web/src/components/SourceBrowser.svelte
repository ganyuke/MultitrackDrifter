<script>
  import { format, parentPrefix, streamDetails } from '../lib/timeline.js';

  let {
    sourcePrefix,
    sources,
    importProbe,
    importPerspective = $bindable(),
    importStreams = $bindable(),
    importError,
    wallclockMs,
    error,
    browseSources,
    inspectSource,
    addSelectedStreams,
    clearImport,
    clearError
  } = $props();

  let selectedStreamCount = $derived((importStreams || []).filter((stream) => stream.selected).length);
</script>

<aside class="left-panel">
  <div class="panel-head"><span class="panel-label">Sources</span>
    {#if sourcePrefix}<button class="topbar-icon-btn" onclick={() => browseSources?.(parentPrefix(sourcePrefix))} title="Up">↑</button>{/if}
  </div>
  {#if sourcePrefix}
    <div class="breadcrumb">
      <button class="crumb" onclick={() => browseSources?.('')}>root</button>
      {#each sourcePrefix.split('/').filter(Boolean) as part, index}
        <span class="crumb-sep">/</span>
        <button class="crumb" onclick={() => browseSources?.(sourcePrefix.split('/').filter(Boolean).slice(0, index + 1).join('/') + '/')}>{part}</button>
      {/each}
    </div>
  {/if}
  <div class="source-file-list">
    {#each sources as item}
      {#if item.isPrefix}
        <button class="src-row src-folder" title={item.ref.path} onclick={() => browseSources?.(item.ref.path)}>
          <span class="src-icon-folder">📁</span>
          <span class="src-name">{item.ref.path.split('/').filter(Boolean).pop()}/</span>
        </button>
      {:else}
        <button class="src-row src-file" title={item.ref.path} onclick={() => inspectSource?.(item.ref.path)}>
          <span class="src-icon-file">🎬</span>
          <span class="src-name">{item.name}</span>
        </button>
      {/if}
    {:else}
      <p class="muted padded">No files.</p>
    {/each}
  </div>

  {#if importProbe}
    <div class="import-sheet">
      <div class="import-head">
        <div class="import-head-text">
          <span class="import-fname">{importProbe.name}</span>
          <span class="import-size muted">{(importProbe.sizeBytes / 1e6).toFixed(1)} MB</span>
        </div>
        <button class="topbar-icon-btn" onclick={clearImport}>×</button>
      </div>
      <label class="dense-label">Perspective<input bind:value={importPerspective} /></label>
      <div class="stream-entries">
        {#each importStreams as stream}
          <div class="stream-entry {stream.selected ? '' : 'stream-off'}">
            <label class="stream-check-row">
              <input type="checkbox" bind:checked={stream.selected} />
              <span class="stream-kind-tag {stream.kind}">{stream.kind.charAt(0).toUpperCase()}</span>
              <span class="stream-idx muted">#{stream.index}</span>
              <span class="stream-details muted">{streamDetails(stream)}</span>
            </label>
            {#if stream.selected}
              <input class="stream-input" bind:value={stream.track} placeholder="Track" />
              <input class="stream-input" bind:value={stream.displayName} placeholder="Clip name" />
            {/if}
          </div>
        {/each}
      </div>
      <button class="btn-primary full" onclick={addSelectedStreams}>
        Add {selectedStreamCount} stream{selectedStreamCount !== 1 ? 's' : ''} @ {format(wallclockMs)}
      </button>
      {#if importError}<p class="error">{importError}</p>{/if}
    </div>
  {/if}

  {#if error}
    <div class="error-bar">
      <span>{error}</span>
      <button class="topbar-icon-btn" onclick={clearError}>×</button>
    </div>
  {/if}
</aside>
