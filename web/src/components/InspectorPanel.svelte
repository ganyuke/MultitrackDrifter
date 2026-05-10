<script>
  import { getAppActions } from '../lib/app-actions.js';
  import { clipStatus, format, annotationAuthor, markerColor as markerColorFor, regionColor as regionColorFor, hasId } from '../lib/timeline.js';
  import { AUDIO, PERCENT_SCALE, TIMING } from '../lib/constants.js';

  const app = getAppActions();

  let {
    activeInspectorTab = $bindable(),
    current,
    selectedClip,
    selectedClips,
    audioClips,
    activeAudioIds,
    volumes,
    softNudges,
    wallclockMs,
    linkedMoveEnabled,
    markers,
    regions,
    me,
    members,
    presenceUsers
  } = $props();

  const tabs = [['clip','Clip'],['mixer','Mix'],['markers','Mkr'],['regions','Rgn'],['export','Export']];
</script>

<aside class="right-panel">
  <div class="inspector-tabs">
    {#each tabs as [tab, label]}
      <button class="ins-tab {activeInspectorTab === tab ? 'ins-tab-active' : ''}" onclick={() => activeInspectorTab = tab}>{label}</button>
    {/each}
  </div>

  <div class="inspector-content">
    {#if activeInspectorTab === 'clip'}
      {#if selectedClip}
        <div class="ins-section">
          <div class="clip-inspect-title">
            <span class="clip-kind-badge {selectedClip.kind}">{selectedClip.kind}</span>
            <span class="clip-inspect-name">{selectedClip.displayName}</span>
          </div>
          <dl class="detail-list">
            <dt>Selected</dt><dd>{selectedClips.length} clip{selectedClips.length === 1 ? '' : 's'}</dd>
            <dt>Track</dt><dd>{selectedClip.perspectiveName} / {selectedClip.trackName}</dd>
            <dt>Stream</dt><dd>#{selectedClip.streamIndex}</dd>
            <dt>Start</dt><dd>{format(selectedClip.wallclockStartMs)}</dd>
            <dt>Duration</dt><dd>{format(selectedClip.durationMs)}</dd>
            <dt>A/V link</dt><dd>{selectedClip.linkGroupId ? 'Linked' : 'Detached'}</dd>
            <dt>Status</dt>
            <dd class="status-dd">
              <span class="inline-status {clipStatus(selectedClip)}">{clipStatus(selectedClip)}</span>
              {#if clipStatus(selectedClip) === 'failed'}
                <button class="btn-sm btn-warn-sm" onclick={() => app.retryClipIngest?.(selectedClip.clipId)}>Retry</button>
              {/if}
            </dd>
          </dl>
        </div>

        <div class="ins-section">
          <p class="ins-section-label">Timeline align {app.isProjectOwner ? '' : '(owner only)'}</p>
          {#if selectedClips.length > 1}
            <p class="muted">Nudges and drags move all selected clips together.</p>
          {:else if selectedClip.linkGroupId && linkedMoveEnabled}
            <p class="muted">Linked A/V is enabled, so matching video/audio clips move together.</p>
          {/if}
          <div class="nudge-grid">
            <button class="nudge-btn nudge-wide" onclick={() => app.moveClipTo?.(selectedClip, wallclockMs)} disabled={!app.isProjectOwner} title="Move selected clips so they all start at the current playhead time">Align to Playhead</button>
            <button class="nudge-btn" onclick={() => app.moveClip?.(selectedClip, -TIMING.largePlayheadJogMs)} disabled={!app.isProjectOwner}>−1s</button>
            <button class="nudge-btn" onclick={() => app.moveClip?.(selectedClip, -TIMING.smallPlayheadJogMs)} disabled={!app.isProjectOwner}>−100ms</button>
            <button class="nudge-btn" onclick={() => app.moveClip?.(selectedClip, TIMING.smallPlayheadJogMs)} disabled={!app.isProjectOwner}>+100ms</button>
            <button class="nudge-btn" onclick={() => app.moveClip?.(selectedClip, TIMING.largePlayheadJogMs)} disabled={!app.isProjectOwner}>+1s</button>
          </div>
        </div>

        <div class="ins-section">
          <p class="ins-section-label">Soft A/V nudge (local only, not saved to server)</p>
          <div class="soft-nudge-row">
            <button class="nudge-btn" onclick={() => app.setSoftNudge?.(selectedClip.clipId, (softNudges[selectedClip.clipId] || 0) - TIMING.softNudgeStepMs)}>−50ms</button>
            <span class="soft-val">{softNudges[selectedClip.clipId] || 0}ms</span>
            <button class="nudge-btn" onclick={() => app.setSoftNudge?.(selectedClip.clipId, (softNudges[selectedClip.clipId] || 0) + TIMING.softNudgeStepMs)}>+50ms</button>
            <button class="btn-sm-ghost" onclick={() => app.setSoftNudge?.(selectedClip.clipId, 0)}>Reset</button>
          </div>
        </div>

        <div class="ins-section ins-actions">
          <button class="action-btn" onclick={app.renameSelectedClip} disabled={!app.isProjectOwner || selectedClips.length !== 1}>Rename…</button>
          <button class="action-btn" onclick={app.detachSelectedClips} disabled={!app.isProjectOwner || !selectedClips.some((clip) => clip.linkGroupId)}>Detach A/V</button>
          <button class="action-btn action-danger" onclick={app.deleteSelectedClips} disabled={!app.isProjectOwner}>Delete {selectedClips.length > 1 ? 'selection' : 'clip'}</button>
        </div>
      {:else}
        <p class="muted padded">Select a clip in the timeline.</p>
      {/if}

    {:else if activeInspectorTab === 'mixer'}
      <div class="ins-section">
        <p class="ins-section-label">Audio mixer</p>
        {#each audioClips as clip}
          <div class="mixer-entry">
            <label class="mixer-check-row">
              <input type="checkbox" checked={hasId(activeAudioIds, clip.trackId)} onchange={() => app.toggleTrack?.('audio', clip.trackId)} />
              <span class="mixer-name" title="{clip.perspectiveName} / {clip.trackName}">{clip.perspectiveName} / {clip.trackName}</span>
            </label>
            <div class="mixer-vol-row">
              <input type="range" min={AUDIO.minVolume} max={AUDIO.maxVolume} step={AUDIO.volumeStep} value={volumes[clip.clipId] ?? AUDIO.defaultVolume} oninput={(event) => app.setVolume?.(clip, event.currentTarget.value)} />
              <span class="vol-pct">{Math.round((volumes[clip.clipId] ?? AUDIO.defaultVolume) * PERCENT_SCALE)}%</span>
            </div>
          </div>
        {:else}
          <p class="muted">No audio tracks prepared.</p>
        {/each}
      </div>

    {:else if activeInspectorTab === 'markers'}
      <div class="ann-toolbar">
        <button class="tbar-tool-btn" onclick={app.addMarker} disabled={!app.canAnnotateProject}>+ Marker @ {format(wallclockMs)}</button>
      </div>
      {#if !app.canAnnotateProject}<p class="muted padded">You can view this project, but only editors can add markers.</p>{/if}
      {#each markers as marker}
        <div class="ann-row">
          <span class="ann-dot" style="background:{markerColorFor(marker, me, members, presenceUsers)}"></span>
          <button class="ann-time-btn" onclick={(event) => app.seekToAnnotation?.('marker', marker, event)}>{format(marker.marker_ts_ms)}</button>
          <div class="ann-text">
            <span class="ann-label">{marker.label || '—'}</span>
            <span class="ann-author muted">{annotationAuthor(marker)}</span>
          </div>
          {#if app.canEditAnnotation?.(marker)}
            <button class="topbar-icon-btn" onclick={() => app.deleteMarker?.(marker.id)}>×</button>
          {/if}
        </div>
      {:else}
        <p class="muted padded">Press M to add a marker at the playhead.</p>
      {/each}

    {:else if activeInspectorTab === 'regions'}
      <div class="ann-toolbar">
        <button class="tbar-tool-btn" onclick={app.addRegion} disabled={!app.canAnnotateProject}>+ Region @ {format(wallclockMs)}</button>
      </div>
      {#if !app.canAnnotateProject}<p class="muted padded">You can view this project, but only editors can add regions.</p>{/if}
      {#each regions as region}
        <div class="ann-row">
          <span class="ann-dot" style="background:{regionColorFor(region, me, members, presenceUsers)};border-radius:2px"></span>
          <button class="ann-time-btn" onclick={(event) => app.seekToAnnotation?.('region', region, event)}>{format(region.region_start_ms)}</button>
          <div class="ann-text">
            <span class="ann-label">{region.label || '—'}</span>
            <span class="ann-author muted">{format(region.region_end_ms - region.region_start_ms)} · {annotationAuthor(region)}</span>
          </div>
          {#if app.canEditAnnotation?.(region)}
            <button class="topbar-icon-btn" onclick={() => app.deleteRegion?.(region.id)}>×</button>
          {/if}
        </div>
      {:else}
        <p class="muted padded">Press R to add a region at the playhead.</p>
      {/each}

    {:else if activeInspectorTab === 'export'}
      <div class="ins-section">
        <p class="ins-section-label">Download annotations</p>
        <div class="export-links">
          <a class="export-link" href="/api/projects/{current.id}/export.csv" download>CSV</a>
          <a class="export-link" href="/api/projects/{current.id}/export.md" download>Markdown</a>
          <a class="export-link" href="/api/projects/{current.id}/export.json" download>JSON</a>
          <a class="export-link" href="/api/projects/{current.id}/export.edl" download>EDL</a>
        </div>
      </div>
    {/if}
  </div>
</aside>
