import { ACCENT_COLORS, ANNOTATION_COLORS, GRID_PRESET_CELLS, PERCENT_SCALE, TIME_FORMAT, TIMELINE_LAYOUT, TIMING, WAVEFORM } from './constants.js';

export { ACCENT_COLORS };

export function hasId(list, id) {
  return (list || []).some((value) => String(value) === String(id));
}

export function removeIds(list, ids) {
  const deny = new Set((ids || []).map(String));
  return (list || []).filter((value) => !deny.has(String(value)));
}

export function addIds(list, ids) {
  const out = [...(list || [])];
  for (const id of ids || []) if (!hasId(out, id)) out.push(id);
  return out;
}

export function normalizeTrackIds(ids, validIds = null) {
  const valid = validIds ? new Set(validIds.map(String)) : null;
  const out = [];
  for (const id of ids || []) {
    if (valid && !valid.has(String(id))) continue;
    if (!hasId(out, id)) out.push(id);
  }
  return out;
}

export function moveItem(list, id, delta) {
  const arr = [...list];
  const idx = arr.indexOf(id);
  if (idx < 0) return arr;
  const next = Math.max(0, Math.min(arr.length - 1, idx + delta));
  if (next === idx) return arr;
  arr.splice(idx, 1);
  arr.splice(next, 0, id);
  return arr;
}

export function maxCells(preset) {
  return GRID_PRESET_CELLS[preset] || GRID_PRESET_CELLS['3x2'];
}

export function format(ms) {
  const date = new Date(Math.max(0, Number(ms) || 0));
  return date.toISOString().substring(TIME_FORMAT.isoTimeStart, TIME_FORMAT.isoTimeEnd);
}

export function inferPerspective(path) {
  const parts = path.split('/').filter(Boolean);
  return parts.length > 1 ? parts[parts.length - 2] : 'Default';
}

export function parentPrefix(prefix) {
  const parts = prefix.split('/').filter(Boolean);
  parts.pop();
  return parts.length ? `${parts.join('/')}/` : '';
}

export function streamDetails(stream) {
  return stream.kind === 'video'
    ? `${stream.codec || 'video'} ${stream.width || '?'}×${stream.height || '?'}`
    : `${stream.codec || 'audio'} ${stream.channels || '?'}ch`.trim();
}

export function perspectiveKey(item) {
  return item?.perspectiveName || item?.perspective || 'Default';
}

export function timelineEnd(clips, playhead, regions = []) {
  const mediaEnd = clips.reduce((max, clip) => Math.max(max, clip.wallclockStartMs + Math.max(clip.durationMs || 0, TIMING.minimumClipDurationMs)), 0);
  const regionEnd = regions.reduce((max, region) => Math.max(max, region.region_end_ms || 0), 0);
  return Math.max(
    TIMING.timelineMinimumDurationMs,
    mediaEnd + TIMING.timelinePaddingMs,
    regionEnd + TIMING.timelinePaddingMs,
    playhead + TIMING.timelinePaddingMs
  );
}

export function makeTicks(end) {
  const step = end > TIMING.longTickThresholdMs
    ? TIMING.longTickStepMs
    : end > TIMING.mediumTickThresholdMs
      ? TIMING.mediumTickStepMs
      : TIMING.shortTickStepMs;
  const ticks = [];
  for (let value = 0; value <= end; value += step) ticks.push(value);
  return ticks;
}


export function reconcileOrdering({
  clips = [],
  perspectiveOrder = [],
  trackOrder = [],
  visibleTrackIds = [],
  activeAudioIds = [],
  collapsedPerspectiveIds = [],
  hiddenPerspectiveIds = [],
  rememberedPerspectiveViewIds = {},
  rememberedPerspectiveAudioIds = {}
} = {}) {
  const perspectiveKeys = [...new Set(clips.map((clip) => perspectiveKey(clip)))].sort((a, b) => a.localeCompare(b));
  const nextPerspectiveOrder = [
    ...perspectiveOrder.filter((id) => perspectiveKeys.includes(id)),
    ...perspectiveKeys.filter((id) => !perspectiveOrder.includes(id))
  ];

  const trackIds = [...new Set(clips.map((clip) => clip.trackId))];
  const trackById = new Map(clips.map((clip) => [clip.trackId, clip]));
  const existingTrackOrder = trackOrder.filter((id) => trackIds.includes(id));
  const knownTracks = new Set(existingTrackOrder);
  const fallbackTrackOrder = [...trackIds].sort((a, b) => {
    const ca = trackById.get(a);
    const cb = trackById.get(b);
    const pa = nextPerspectiveOrder.indexOf(perspectiveKey(ca));
    const pb = nextPerspectiveOrder.indexOf(perspectiveKey(cb));
    if (pa !== pb) return pa - pb;
    if (ca?.kind !== cb?.kind) return ca?.kind === 'video' ? -1 : 1;
    return String(ca?.trackName || '').localeCompare(String(cb?.trackName || ''));
  });

  let nextVisibleTrackIds = normalizeTrackIds(visibleTrackIds, trackIds);
  let nextActiveAudioIds = normalizeTrackIds(activeAudioIds, trackIds);

  for (const id of trackIds) {
    const clip = trackById.get(id);
    // Only auto-enable tracks that are genuinely new. Tracks explicitly disabled
    // by the user are already in trackOrder but not in the enabled lists.
    if (!knownTracks.has(id) && clip?.kind === 'video' && !hasId(nextVisibleTrackIds, id)) {
      nextVisibleTrackIds = addIds(nextVisibleTrackIds, [id]);
    }
    if (!knownTracks.has(id) && clip?.kind === 'audio' && !hasId(nextActiveAudioIds, id)) {
      nextActiveAudioIds = addIds(nextActiveAudioIds, [id]);
    }
  }

  return {
    perspectiveOrder: nextPerspectiveOrder,
    trackOrder: [...existingTrackOrder, ...fallbackTrackOrder.filter((id) => !existingTrackOrder.includes(id))],
    visibleTrackIds: nextVisibleTrackIds,
    activeAudioIds: nextActiveAudioIds,
    collapsedPerspectiveIds: collapsedPerspectiveIds.filter((id) => perspectiveKeys.includes(id)),
    hiddenPerspectiveIds: hiddenPerspectiveIds.filter((id) => perspectiveKeys.includes(id)),
    rememberedPerspectiveViewIds: pruneTrackMemory(rememberedPerspectiveViewIds, trackIds, perspectiveKeys),
    rememberedPerspectiveAudioIds: pruneTrackMemory(rememberedPerspectiveAudioIds, trackIds, perspectiveKeys)
  };
}

function pruneTrackMemory(memory, validTrackIds, validPerspectiveIds) {
  const validTracks = new Set(validTrackIds.map(String));
  const validPerspectives = new Set(validPerspectiveIds.map(String));
  const next = {};

  for (const [perspectiveId, ids] of Object.entries(memory || {})) {
    if (!validPerspectives.has(String(perspectiveId))) continue;
    const filtered = [...new Set(Array.isArray(ids) ? ids : [])].filter((id) => validTracks.has(String(id)));
    if (filtered.length) next[perspectiveId] = filtered;
  }

  return next;
}

export function buildPerspectiveGroups(
  clips,
  orderedPerspectives = [],
  orderedTracks = [],
  collapsedIds = [],
  hiddenIds = [],
  enabledVideoTrackIds = [],
  enabledAudioTrackIds = []
) {
  const groups = new Map();
  for (const clip of clips) {
    const id = perspectiveKey(clip);
    if (!groups.has(id)) groups.set(id, { id, perspectiveName: id, tracks: [] });
    const group = groups.get(id);
    let track = group.tracks.find((item) => item.id === clip.trackId);
    if (!track) {
      track = { id: clip.trackId, trackName: clip.trackName, kind: clip.kind, clips: [], perspectiveName: id };
      group.tracks.push(track);
    }
    track.clips.push(clip);
  }

  const perspectiveIds = [...orderedPerspectives.filter((id) => groups.has(id)), ...[...groups.keys()].filter((id) => !orderedPerspectives.includes(id))];
  return perspectiveIds.map((id) => {
    const group = groups.get(id);
    const tracks = [...group.tracks].sort((a, b) => {
      const ia = orderedTracks.indexOf(a.id);
      const ib = orderedTracks.indexOf(b.id);
      const orderA = ia < 0 ? TIMELINE_LAYOUT.unknownTrackSortIndex : ia;
      const orderB = ib < 0 ? TIMELINE_LAYOUT.unknownTrackSortIndex : ib;
      return orderA - orderB || String(a.trackName).localeCompare(String(b.trackName));
    });
    const videoTracks = tracks.filter((track) => track.kind === 'video');
    const audioTracks = tracks.filter((track) => track.kind === 'audio');
    return {
      ...group,
      tracks,
      videoTracks,
      audioTracks,
      collapsed: collapsedIds.includes(id),
      hidden: hiddenIds.includes(id),
      viewEnabled: videoTracks.some((track) => hasId(enabledVideoTrackIds, track.id)) && !hiddenIds.includes(id),
      audioEnabled: audioTracks.some((track) => hasId(enabledAudioTrackIds, track.id))
    };
  });
}

export function flattenTimelineRows(groups) {
  const rows = [];
  for (const group of groups) {
    rows.push({ type: 'perspective', id: group.id, perspectiveName: group.perspectiveName, group });
    if (group.collapsed) {
      const clips = group.tracks.flatMap((track) => track.clips).sort((a, b) => a.wallclockStartMs - b.wallclockStartMs);
      rows.push({ type: 'collapsed', id: `${group.id}:collapsed`, perspectiveName: group.perspectiveName, clips, group });
      continue;
    }
    for (const track of group.tracks) rows.push({ type: 'track', ...track, group });
  }
  return rows;
}

export function buildMonitorCells(groups, enabledTrackIds, hiddenPerspectives, wallclockMs) {
  const cells = [];
  for (const group of groups) {
    if (hiddenPerspectives.includes(group.id)) continue;
    for (const track of group.videoTracks) {
      if (!hasId(enabledTrackIds, track.id)) continue;
      const activeClip = track.clips.find((clip) => wallclockMs >= clip.wallclockStartMs && wallclockMs <= clip.wallclockStartMs + clip.durationMs) || null;
      cells.push({ trackId: track.id, trackName: track.trackName, perspectiveName: group.perspectiveName, activeClip });
    }
  }
  return cells;
}

export function waveBars(clip) {
  const seed = Number(clip.clipId || 1);
  return Array.from(
    { length: WAVEFORM.barCount },
    (_, index) => WAVEFORM.minHeight + ((seed * (index + WAVEFORM.seedOffset) * WAVEFORM.seedMultiplier) % WAVEFORM.heightRange)
  );
}

export function countStatuses(clips) {
  const counts = { total: clips.length, ready: 0, queued: 0, processing: 0, failed: 0 };
  for (const clip of clips) {
    const status = clipStatus(clip);
    if (status === 'success') counts.ready += 1;
    else if (status === 'processing') counts.processing += 1;
    else if (status === 'failed') counts.failed += 1;
    else counts.queued += 1;
  }
  return counts;
}

export function clipStatus(clip) {
  return String(clip.ingestStatus || (clip.hlsURL ? 'SUCCESS' : 'PENDING')).toLowerCase();
}

export function statusText(clip) {
  const status = clipStatus(clip);
  return status === 'success' ? '' : status === 'processing' ? 'encoding' : status === 'failed' ? 'failed' : 'queued';
}

export function jobProgress(job) {
  return Math.max(0, Math.min(1, Number(job.progress_pct || 0)));
}

export function jobProgressPct(job) {
  return `${Math.round(jobProgress(job) * PERCENT_SCALE)}%`;
}

export function jobStage(job) {
  const state = String(job.state || 'job');
  return job.stage || (state === 'PROCESSING' ? 'working' : state.toLowerCase());
}

export function jobClipLabel(job) {
  return job.clip_name || `clip ${job.clip_id}`;
}

export function jobStats(job) {
  const stats = [];
  if (job.ffmpeg_fps) stats.push(`${job.ffmpeg_fps} fps`);
  if (job.ffmpeg_bitrate) stats.push(job.ffmpeg_bitrate);
  if (job.ffmpeg_speed) stats.push(`${job.ffmpeg_speed}×`);
  return stats.join(' · ');
}

export function annotationAuthor(item) {
  return item?.author_username || 'unknown';
}

export function colorForUsername(username, fallback, me, members = [], presenceUsers = []) {
  if (!username) return fallback;
  if (me?.username === username && me.color) return me.color;
  const member = members.find((item) => item.username === username);
  if (member?.color) return member.color;
  const present = presenceUsers.find((item) => item.username === username);
  if (present?.color) return present.color;
  return fallback;
}

export function annotationColor(item, fallback, me, members = [], presenceUsers = []) {
  return item?.author_color || item?.authorColor || colorForUsername(annotationAuthor(item), fallback, me, members, presenceUsers);
}

export function markerColor(marker, me, members = [], presenceUsers = []) {
  return annotationColor(marker, ANNOTATION_COLORS.markerDefault, me, members, presenceUsers);
}

export function regionColor(region, me, members = [], presenceUsers = []) {
  return annotationColor(region, ANNOTATION_COLORS.regionDefault, me, members, presenceUsers);
}

export function withAlpha(color, alpha) {
  return /^#[0-9a-fA-F]{6}$/.test(color) ? `${color}${alpha}` : color;
}
