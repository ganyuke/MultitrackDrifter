<script>
  import { onMount, tick } from 'svelte';
  import { api, postJSON, patchJSON, del } from './api.js';
  import { attachHLS, clipLocalSeconds, fitVideoToCell, loadPrefs, savePrefs } from './playback.js';

  let me = null;
  let username = 'alice';
  let password = 'dev';
  let error = '';
  let importError = '';
  let errorTimer = null;
  let projects = [];
  let current = null;
  let manifest = { clips: [] };
  let markers = [];
  let regions = [];
  let sources = [];
  let sourcePrefix = '';
  let importProbe = null;
  let importPerspective = '';
  let importStreams = [];
  let newProjectName = 'Local review';
  let wallclockMs = 0;
  let playing = false;
  let started = false;
  let gridPreset = '2x2';
  let visibleTrackIds = [];
  let activeAudioIds = [];
  let softNudges = {};
  let volumes = {};
  let selectedClipId = null;
  let timelineViewport;
  let timelineCanvas;
  let dragState = null;
  let ws;
  const TRACK_HEADER_PX = 196;
  let mediaRefs = new Map();
  let cleanups = new Map();
  let attachedUrls = new Map();
  let attachedNodes = new Map();
  let trackMediaRefs = new Map();
  let trackCleanups = new Map();
  let trackAttachedUrls = new Map();
  let trackAttachedNodes = new Map();
  let annotationEditor = null;
  let annotationSaved = '';
  let playbackToken = 0;
  let showProjectPicker = false;
  let perspectiveOrder = [];
  let trackOrder = [];
  let collapsedPerspectiveIds = [];
  let hiddenPerspectiveIds = [];
  let rememberedPerspectiveViewIds = {};
  let rememberedPerspectiveAudioIds = {};
  let previousTargetSignature = '';

  $: allClips = manifest.clips || [];
  $: videoClips = allClips.filter(c => c.kind === 'video');
  $: audioClips = allClips.filter(c => c.kind === 'audio');
  $: perspectiveGroups = buildPerspectiveGroups(allClips, perspectiveOrder, trackOrder, collapsedPerspectiveIds, hiddenPerspectiveIds);
  $: trackRows = flattenTimelineRows(perspectiveGroups);
  $: monitorCells = buildMonitorCells(perspectiveGroups, visibleTrackIds, hiddenPerspectiveIds, wallclockMs).slice(0, maxCells(gridPreset));
  $: visibleVideos = monitorCells.map(cell => cell.activeClip).filter(Boolean);
  $: selectedClip = allClips.find(c => c.clipId === selectedClipId) || null;
  $: timelineEndMs = timelineEnd(allClips, wallclockMs);
  $: timelineLaneWidthPx = Math.max(900, Math.ceil(timelineEndMs / 45));
  $: timelineWidthPx = timelineLaneWidthPx;
  $: tickMarks = makeTicks(timelineEndMs);
  $: activeAudioClips = audioClips.filter(c => activeAudioIds.includes(c.trackId));
  $: statusCounts = countStatuses(allClips);

  onMount(() => {
    window.addEventListener('keydown', handleKeyDown);
    const timer = setInterval(syncPlayingMedia, 250);
    const poller = setInterval(() => {
      if (current && (statusCounts.queued > 0 || statusCounts.processing > 0)) refreshProject();
    }, 2000);
    boot();
    return () => {
      window.removeEventListener('keydown', handleKeyDown);
      clearInterval(timer);
      clearInterval(poller);
    };
  });

  async function boot() {
    try { me = await api('/api/me'); await loadProjects(); }
    catch (_) { me = null; }
  }

  async function login() {
    try { error = ''; me = await postJSON('/api/login', { username, password }); await loadProjects(); }
    catch (e) { setError(e.message, 0); }
  }

  async function logout() {
    await postJSON('/api/logout', {});
    me = null; current = null; projects = []; disconnectMedia();
  }

  async function loadProjects() { projects = await api('/api/projects'); }

  async function createProject() {
    const p = await postJSON('/api/projects', { name: newProjectName, description: 'Local source/local HLS POC' });
    await loadProjects();
    await openProject(p.id);
  }

  async function openProject(id) {
    current = await api(`/api/projects/${id}`);
    manifest = await api(`/api/projects/${id}/playback-manifest`);
    reconcileOrdering();
    markers = await api(`/api/projects/${id}/markers`);
    regions = await api(`/api/projects/${id}/regions`);
    const prefs = loadPrefs(id);
    gridPreset = prefs.gridPreset || '2x2';
    perspectiveOrder = prefs.perspectiveOrder || [];
    trackOrder = prefs.trackOrder || [];
    collapsedPerspectiveIds = prefs.collapsedPerspectiveIds || [];
    hiddenPerspectiveIds = prefs.hiddenPerspectiveIds || [];
    rememberedPerspectiveViewIds = prefs.rememberedPerspectiveViewIds || {};
    rememberedPerspectiveAudioIds = prefs.rememberedPerspectiveAudioIds || {};
    visibleTrackIds = prefs.visibleTrackIds || [...new Set(videoClips.map(c => c.trackId))];
    activeAudioIds = prefs.activeAudioIds || [...new Set(audioClips.map(c => c.trackId))];
    reconcileOrdering();
    softNudges = prefs.softNudges || {};
    volumes = prefs.volumes || {};
    selectedClipId = prefs.selectedClipId || null;
    connectWS(id);
    await browseSources('');
    await tick();
    attachAll();
  }

  async function refreshProject() {
    if (!current) return;
    const id = current.id;
    current = await api(`/api/projects/${id}`);
    manifest = await api(`/api/projects/${id}/playback-manifest`);
    reconcileOrdering();
    markers = await api(`/api/projects/${id}/markers`);
    regions = await api(`/api/projects/${id}/regions`);
    await tick();
    attachAll();
  }

  function connectWS(id) {
    if (ws) ws.close();
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    ws = new WebSocket(`${proto}//${location.host}/ws/projects/${id}`);
    ws.onmessage = async (ev) => {
      const msg = JSON.parse(ev.data);
      if (msg.type?.startsWith('marker.') || msg.type?.startsWith('region.') || msg.type?.startsWith('clip.')) await refreshProject();
    };
  }

  async function browseSources(prefix) {
    if (!current) return;
    sourcePrefix = prefix;
    sources = await api(`/api/projects/${current.id}/sources?prefix=${encodeURIComponent(prefix)}&delimiter=/`);
  }

  function parentPrefix(prefix) {
    const parts = prefix.split('/').filter(Boolean);
    parts.pop();
    return parts.length ? `${parts.join('/')}/` : '';
  }

  async function inspectSource(path) {
    importError = '';
    error = '';
    importProbe = await api(`/api/projects/${current.id}/sources/probe?path=${encodeURIComponent(path)}`);
    importPerspective = inferPerspective(path);
    importStreams = (importProbe.streams || []).map((stream) => ({
      ...stream,
      selected: true,
      track: `${importPerspective} / ${stream.label || (stream.kind === 'audio' ? 'Audio' : 'Video')}`,
      displayName: `${path.split('/').pop()} - ${stream.label || stream.kind}`
    }));
  }

  async function addSelectedStreams() {
    const streams = importStreams.filter(s => s.selected).map(s => ({
      streamIndex: s.index,
      kind: s.kind,
      track: s.track.trim(),
      displayName: s.displayName.trim(),
      label: s.label
    }));
    if (!streams.length) { importError = 'Select at least one video or audio stream.'; return; }
    await postJSON(`/api/projects/${current.id}/assets`, { sourcePath: importProbe.sourcePath, perspective: importPerspective, wallclockStartMs: wallclockMs, streams });
    importProbe = null;
    importStreams = [];
    importError = '';
    await refreshProject();
    setTimeout(refreshProject, 1200);
  }

  async function ingest() {
    await postJSON(`/api/projects/${current.id}/ingest`, {});
    setTimeout(refreshProject, 1200);
  }

  async function addMarker() { await postJSON(`/api/projects/${current.id}/markers`, { tsMs: wallclockMs, label: 'Funny moment', note: '' }); }
  async function addRegion() { await postJSON(`/api/projects/${current.id}/regions`, { startMs: wallclockMs, endMs: wallclockMs + 5000, label: 'Clip candidate', note: '' }); }
  async function deleteMarker(id) {
    const marker = markers.find(m => String(m.id) === String(id));
    if (marker && !canEditAnnotation(marker)) { annotationSaved = readOnlyAnnotationMessage(); return; }
    await del(`/api/projects/${current.id}/markers/${id}`);
  }

  async function moveClipTo(clip, startMs) {
    if (!isProjectOwner()) { setError(projectOwnerMessage()); return; }
    await patchJSON(`/api/projects/${current.id}/clips/${clip.clipId}`, { wallclockStartMs: Math.max(0, Math.round(startMs)) });
  }

  async function moveClip(clip, delta) { await moveClipTo(clip, clip.wallclockStartMs + delta); }

  async function renameSelectedClip() {
    if (!selectedClip) return;
    if (!isProjectOwner()) { setError(projectOwnerMessage()); return; }
    const name = prompt('Clip name', selectedClip.displayName || '');
    if (name === null) return;
    await patchJSON(`/api/projects/${current.id}/clips/${selectedClip.clipId}`, { displayName: name });
    await refreshProject();
  }

  async function deleteClip(clip = selectedClip) {
    if (!clip) return;
    if (!isProjectOwner()) { setError(projectOwnerMessage()); return; }
    const label = clip.displayName || 'clip';
    if (!confirm(`Remove ${label} from this project timeline? Source files and generated review media stay on disk.`)) return;
    await del(`/api/projects/${current.id}/clips/${clip.clipId || clip.id}`);
    if (selectedClipId === (clip.clipId || clip.id)) selectedClipId = null;
    await refreshProject();
  }

  function persistPrefs() {
    if (!current) return;
    savePrefs(current.id, { gridPreset, visibleTrackIds, activeAudioIds, softNudges, volumes, selectedClipId, perspectiveOrder, trackOrder, collapsedPerspectiveIds, hiddenPerspectiveIds, rememberedPerspectiveViewIds, rememberedPerspectiveAudioIds });
  }

  function disconnectMedia() {
    for (const cleanup of cleanups.values()) cleanup();
    for (const cleanup of trackCleanups.values()) cleanup();
    cleanups.clear();
    attachedUrls.clear();
    attachedNodes.clear();
    mediaRefs.clear();
    trackCleanups.clear();
    trackAttachedUrls.clear();
    trackAttachedNodes.clear();
    trackMediaRefs.clear();
  }

  function cleanupClipMedia(clipId) {
    const cleanup = cleanups.get(clipId);
    if (cleanup) cleanup();
    cleanups.delete(clipId);
    attachedUrls.delete(clipId);
    attachedNodes.delete(clipId);
  }

  function cleanupTrackMedia(trackId) {
    const cleanup = trackCleanups.get(trackId);
    if (cleanup) cleanup();
    trackCleanups.delete(trackId);
    trackAttachedUrls.delete(trackId);
    trackAttachedNodes.delete(trackId);
  }

  function setMedia(node, key) {
    if (node) {
      mediaRefs.set(key, node);
      attachedUrls.delete(key);
      attachedNodes.delete(key);
    }
    return {
      update: (nextKey) => {
        if (nextKey === key) return;
        mediaRefs.delete(key);
        cleanupClipMedia(key);
        key = nextKey;
        mediaRefs.set(key, node);
      },
      destroy: () => {
        mediaRefs.delete(key);
        cleanupClipMedia(key);
      }
    };
  }

  function setTrackMedia(node, trackId) {
    if (node) {
      trackMediaRefs.set(trackId, node);
      trackAttachedUrls.delete(trackId);
      trackAttachedNodes.delete(trackId);
    }
    return {
      update: (nextTrackId) => {
        if (nextTrackId === trackId) return;
        trackMediaRefs.delete(trackId);
        cleanupTrackMedia(trackId);
        trackId = nextTrackId;
        trackMediaRefs.set(trackId, node);
      },
      destroy: () => {
        trackMediaRefs.delete(trackId);
        cleanupTrackMedia(trackId);
      }
    };
  }

  function attachAll() {
    attachAudioClips();
    attachVideoCells();
  }

  function attachAudioClips() {
    const liveAudio = new Set(audioClips.map(c => c.clipId));
    for (const [clipId, cleanup] of cleanups.entries()) {
      if (!liveAudio.has(clipId)) {
        cleanup();
        cleanups.delete(clipId);
        attachedUrls.delete(clipId);
        attachedNodes.delete(clipId);
      }
    }
    for (const clip of audioClips) {
      const node = mediaRefs.get(clip.clipId);
      if (!node || !clip.hlsURL) continue;
      node.muted = !activeAudioIds.includes(clip.trackId);
      node.volume = Number(volumes[clip.clipId] ?? 0.85);
      if (attachedUrls.get(clip.clipId) === clip.hlsURL && attachedNodes.get(clip.clipId) === node) continue;
      const existing = cleanups.get(clip.clipId);
      if (existing) existing();
      cleanups.set(clip.clipId, attachHLS(node, clip.hlsURL));
      attachedUrls.set(clip.clipId, clip.hlsURL);
      attachedNodes.set(clip.clipId, node);
    }
  }

  function attachVideoCells() {
    const liveTracks = new Set(monitorCells.map(c => c.trackId));
    for (const [trackId, cleanup] of trackCleanups.entries()) {
      if (!liveTracks.has(trackId)) {
        cleanup();
        trackCleanups.delete(trackId);
        trackAttachedUrls.delete(trackId);
        trackAttachedNodes.delete(trackId);
      }
    }
    for (const cell of monitorCells) {
      const node = trackMediaRefs.get(cell.trackId);
      if (!node) continue;
      const clip = cell.activeClip;
      if (!clip || !clip.hlsURL || !isClipPlaybackEnabled(clip)) {
        node.pause();
        node.dataset.activeClipId = '';
        continue;
      }
      node.muted = true;
      node.dataset.activeClipId = String(clip.clipId);
      if (trackAttachedUrls.get(cell.trackId) === clip.hlsURL && trackAttachedNodes.get(cell.trackId) === node) continue;
      const existing = trackCleanups.get(cell.trackId);
      if (existing) existing();
      resetMediaElement(node);
      trackCleanups.set(cell.trackId, attachHLS(node, clip.hlsURL));
      trackAttachedUrls.set(cell.trackId, clip.hlsURL);
      trackAttachedNodes.set(cell.trackId, node);
    }
  }

  function mediaNodeForClip(clip) {
    return clip.kind === 'video' ? trackMediaRefs.get(clip.trackId) : mediaRefs.get(clip.clipId);
  }

  function resetMediaElement(node) {
    if (!node) return;
    try { node.pause(); } catch (_) {}
    try { node.removeAttribute('src'); node.load(); } catch (_) {}
  }

  function setError(msg, ms = 5000) {
    error = msg;
    if (errorTimer) clearTimeout(errorTimer);
    if (msg && ms > 0) errorTimer = setTimeout(() => { error = ''; errorTimer = null; }, ms);
  }

  function seekNode(node, seconds) {
    if (!node || !Number.isFinite(seconds)) return;
    const apply = () => {
      try { node.currentTime = Math.max(0, seconds); } catch (_) {}
    };
    if (node.readyState > 0) apply();
    else node.addEventListener('loadedmetadata', apply, { once: true });
  }

  function isPlaybackTargetNow(clip) {
    return playbackTargets().some(target => target.clipId === clip.clipId);
  }

  function queueReadyPlay(node, clip, token) {
    const retry = () => {
      if (!playing || token !== playbackToken || !isClipPlaybackEnabled(clip) || !isPlaybackTargetNow(clip)) return;
      const local = clipLocalSeconds(wallclockMs, clip, softNudges[clip.clipId] || 0);
      if (Number.isFinite(local)) seekNode(node, local);
      node.play().catch(() => {});
    };
    node.addEventListener('loadedmetadata', retry, { once: true });
    node.addEventListener('canplay', retry, { once: true });
    node.addEventListener('playing', retry, { once: true });
  }

  async function safePlay(node, clip, token = playbackToken) {
    if (!node || !clip || !isClipPlaybackEnabled(clip)) return;
    if (node.readyState < 2) queueReadyPlay(node, clip, token);
    try {
      await node.play();
    } catch (e) {
      const message = e?.message || '';
      if (e?.name === 'AbortError' || /abort|interrupted|removed|detached/i.test(message)) return;
      queueReadyPlay(node, clip, token);
      if (token === playbackToken && isClipPlaybackEnabled(clip) && node.readyState > 0) setError(`Playback blocked for ${clip.displayName || clip.trackName}: ${message}`);
    }
  }

  async function startSession() {
    started = true;
    await tick();
    attachAll();
    seekAll();
    playing = true;
    await playActiveMedia();
  }

  function seekAll() {
    previousTargetSignature = '';
    attachAll();
    const targets = playbackTargets();
    const targetIds = new Set(targets.map(c => c.clipId));
    pauseInactiveMedia();
    for (const clip of targets) {
      const node = mediaNodeForClip(clip);
      if (!node || !clip.hlsURL) continue;
      const local = clipLocalSeconds(wallclockMs, clip, softNudges[clip.clipId] || 0);
      if (Number.isFinite(local)) seekNode(node, local);
      if (playing) safePlay(node, clip);
    }
    for (const clip of allClips) {
      if (targetIds.has(clip.clipId)) continue;
      const node = mediaNodeForClip(clip);
      if (node && clip.kind === 'audio') node.pause();
    }
  }

  async function togglePlay() {
    if (!started) {
      await startSession();
      return;
    }
    playbackToken += 1;
    playing = !playing;
    if (playing) await playActiveMedia();
    else pauseAllMedia();
  }

  async function playActiveMedia() {
    const token = playbackToken;
    await tick();
    if (token !== playbackToken) return;
    attachAll();
    pauseInactiveMedia();
    for (const clip of playbackTargets()) {
      if (token !== playbackToken || !isClipPlaybackEnabled(clip)) continue;
      const node = mediaNodeForClip(clip);
      if (!node || !clip.hlsURL) continue;
      if (clip.kind === 'audio') node.muted = false;
      const local = clipLocalSeconds(wallclockMs, clip, softNudges[clip.clipId] || 0);
      if (local >= 0 && local * 1000 <= clip.durationMs && Math.abs(node.currentTime - local) > 0.45) seekNode(node, local);
      await safePlay(node, clip, token);
      if (token !== playbackToken || !isClipPlaybackEnabled(clip)) node.pause();
    }
  }

  function pauseAllMedia() {
    playbackToken += 1;
    for (const node of mediaRefs.values()) node.pause();
    for (const node of trackMediaRefs.values()) node.pause();
  }

  function pauseInactiveMedia() {
    const targets = playbackTargets();
    const targetAudioIds = new Set(targets.filter(c => c.kind === 'audio').map(c => c.clipId));
    const targetVideoTrackIds = new Set(targets.filter(c => c.kind === 'video').map(c => c.trackId));
    for (const clip of audioClips) {
      if (targetAudioIds.has(clip.clipId)) continue;
      const node = mediaRefs.get(clip.clipId);
      if (!node) continue;
      node.pause();
      node.muted = true;
    }
    for (const [trackId, node] of trackMediaRefs.entries()) {
      if (!targetVideoTrackIds.has(trackId)) node.pause();
    }
  }

  function playbackTargets() {
    return [...visibleVideos, ...activeAudioClips].filter(clip => clip.hlsURL && isClipPlaybackEnabled(clip) && wallclockMs >= clip.wallclockStartMs && wallclockMs <= clip.wallclockStartMs + clip.durationMs);
  }

  function isClipPlaybackEnabled(clip) {
    if (clip.kind === 'video') return visibleTrackIds.includes(clip.trackId) && !hiddenPerspectiveIds.includes(perspectiveKey(clip));
    return activeAudioIds.includes(clip.trackId);
  }

  async function toggleTrack(listName, id) {
    playbackToken += 1;
    const list = listName === 'video' ? visibleTrackIds : activeAudioIds;
    const disabling = list.includes(id);
    const next = disabling ? list.filter(v => v !== id) : [...list, id];
    if (listName === 'video') visibleTrackIds = next;
    else activeAudioIds = next;
    if (disabling) disableTrackMedia(id);
    persistPrefs();
    await tick();
    attachAll();
    seekAll();
    if (playing) await playActiveMedia();
  }

  function disableTrackMedia(trackId) {
    for (const clip of allClips.filter(c => c.trackId === trackId)) disableClipMedia(clip.clipId);
  }

  function disableClipMedia(clipId) {
    const clip = allClips.find(c => c.clipId === clipId);
    const node = clip ? mediaNodeForClip(clip) : mediaRefs.get(clipId);
    if (!node) return;
    node.pause();
    if (!clip || clip.kind === 'audio') node.muted = true;
  }

  function setVolume(clip, value) {
    volumes = { ...volumes, [clip.clipId]: Number(value) };
    const node = mediaRefs.get(clip.clipId);
    if (node) node.volume = Number(value);
    persistPrefs();
  }

  function startPlayheadDrag(event) {
    if (!timelineCanvas || !timelineViewport) return;
    event.preventDefault();
    event.currentTarget?.setPointerCapture?.(event.pointerId);
    document.body.classList.add('dragging-timeline');
    setWallclockFromClient(event.clientX);
    seekAll();
    const move = (ev) => { ev.preventDefault(); setWallclockFromClient(ev.clientX); seekAll(); };
    const up = () => {
      document.body.classList.remove('dragging-timeline');
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', up);
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', up);
  }

  function startClipDrag(event, clip) {
    selectedClipId = clip.clipId;
    persistPrefs();
    if (!isProjectOwner()) {
      setError(projectOwnerMessage());
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    event.currentTarget?.setPointerCapture?.(event.pointerId);
    document.body.classList.add('dragging-timeline');
    dragState = {
      clipId: clip.clipId,
      originalStartMs: clip.wallclockStartMs,
      previewStartMs: clip.wallclockStartMs,
      pointerStartMs: clientToTimelineMs(event.clientX)
    };
    const move = (ev) => {
      const delta = clientToTimelineMs(ev.clientX) - dragState.pointerStartMs;
      dragState = { ...dragState, previewStartMs: Math.max(0, snapMs(dragState.originalStartMs + delta, 50)) };
      wallclockMs = dragState.previewStartMs;
    };
    const up = async () => {
      document.body.classList.remove('dragging-timeline');
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', up);
      const finished = dragState;
      dragState = null;
      if (finished && Math.abs(finished.previewStartMs - finished.originalStartMs) >= 1) {
        await moveClipTo(clip, finished.previewStartMs);
      }
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', up);
  }

  function setWallclockFromClient(clientX) { wallclockMs = snapMs(clientToTimelineMs(clientX), 25); }

  function clientToTimelineMs(clientX) {
    if (!timelineViewport) return wallclockMs;
    const rect = timelineViewport.getBoundingClientRect();
    const x = Math.max(0, clientX - rect.left + timelineViewport.scrollLeft);
    return Math.min(timelineEndMs, Math.round((x / timelineLaneWidthPx) * timelineEndMs));
  }

  function snapMs(ms, step) { return Math.round(ms / step) * step; }
  function msToLanePx(ms) { return Math.round((Math.max(0, ms) / timelineEndMs) * timelineLaneWidthPx); }
  function msToPx(ms) { return msToLanePx(ms); }
  function maxCells(preset) { return preset === '1x1' ? 1 : preset === '1x2' ? 2 : preset === '2x2' ? 4 : 6; }
  function format(ms) { const d = new Date(Math.max(0, ms)); return d.toISOString().substring(11, 23); }
  function inferPerspective(path) { const parts = path.split('/').filter(Boolean); return parts.length > 1 ? parts[parts.length - 2] : 'Default'; }

  function clipBlockStyle(clip, ghost = false) {
    const isDragged = dragState?.clipId === clip.clipId;
    const start = isDragged && !ghost ? dragState.previewStartMs : clip.wallclockStartMs;
    return `left:${msToLanePx(start)}px;width:${Math.max(36, msToLanePx(clip.durationMs || 1000))}px;`;
  }

  function markerStyle(marker) { return `left:${msToPx(marker.marker_ts_ms)}px;color:${markerColor(marker)};`; }
  function regionStyle(region) {
    const color = regionColor(region);
    return `left:${msToPx(region.region_start_ms)}px;width:${Math.max(10, msToLanePx(region.region_end_ms - region.region_start_ms))}px;background:${withAlpha(color, '55')};border-color:${withAlpha(color, 'cc')};`;
  }
  function markerColor(marker) { return marker.author_color || marker.authorColor || marker.color || me?.color || '#f6c85f'; }
  function regionColor(region) { return region.author_color || region.authorColor || region.color || me?.color || '#8f70ff'; }
  function withAlpha(color, alpha) {
    if (/^#[0-9a-fA-F]{6}$/.test(color)) return `${color}${alpha}`;
    return color;
  }

  function isProjectOwner() { return !!(current && me && current.ownerUsername === me.username); }
  function annotationAuthor(item) { return item?.author_username || item?.authorUsername || item?.author || 'unknown'; }
  function canEditAnnotation(item) { return !!(item && me && (annotationAuthor(item) === me.username || isProjectOwner())); }
  function annotationEditorCanEdit() { return !!(annotationEditor && me && (annotationEditor.author === me.username || isProjectOwner())); }
  function readOnlyAnnotationMessage() { return 'Read-only: only the author or project owner can edit.'; }
  function projectOwnerMessage() { return 'Project owner required to align or manage clips.'; }

  function perspectiveKey(item) { return item.perspectiveName || item.perspective || 'Default'; }

  function reconcileOrdering() {
    const pKeys = [...new Set(allClips.map(c => perspectiveKey(c)))].sort((a, b) => a.localeCompare(b));
    perspectiveOrder = [...perspectiveOrder.filter(p => pKeys.includes(p)), ...pKeys.filter(p => !perspectiveOrder.includes(p))];
    const tKeys = [...new Set(allClips.map(c => c.trackId))];
    const trackById = new Map(allClips.map(c => [c.trackId, c]));
    const existingTrackOrder = trackOrder.filter(t => tKeys.includes(t));
    const knownTrackIds = new Set(existingTrackOrder);
    const fallbackOrder = [...tKeys].sort((a, b) => {
      const ca = trackById.get(a); const cb = trackById.get(b);
      const pa = perspectiveOrder.indexOf(perspectiveKey(ca));
      const pb = perspectiveOrder.indexOf(perspectiveKey(cb));
      if (pa !== pb) return pa - pb;
      if (ca.kind !== cb.kind) return ca.kind === 'video' ? -1 : 1;
      return String(ca.trackName || '').localeCompare(String(cb.trackName || ''));
    });
    trackOrder = [...existingTrackOrder, ...fallbackOrder.filter(t => !existingTrackOrder.includes(t))];
    visibleTrackIds = visibleTrackIds.filter(id => tKeys.includes(id));
    activeAudioIds = activeAudioIds.filter(id => tKeys.includes(id));
    for (const id of tKeys) {
      const clip = trackById.get(id);
      const isNewTrack = !knownTrackIds.has(id);
      if (isNewTrack && clip?.kind === 'video' && !visibleTrackIds.includes(id)) visibleTrackIds = [...visibleTrackIds, id];
      if (isNewTrack && clip?.kind === 'audio' && !activeAudioIds.includes(id)) activeAudioIds = [...activeAudioIds, id];
    }
    collapsedPerspectiveIds = collapsedPerspectiveIds.filter(p => pKeys.includes(p));
    hiddenPerspectiveIds = hiddenPerspectiveIds.filter(p => pKeys.includes(p));
    rememberedPerspectiveViewIds = pruneTrackMemory(rememberedPerspectiveViewIds, tKeys, pKeys);
    rememberedPerspectiveAudioIds = pruneTrackMemory(rememberedPerspectiveAudioIds, tKeys, pKeys);
  }

  function pruneTrackMemory(memory, validTrackIds, validPerspectiveIds) {
    const validTracks = new Set(validTrackIds);
    const validPerspectives = new Set(validPerspectiveIds);
    const next = {};
    for (const [perspectiveId, ids] of Object.entries(memory || {})) {
      if (!validPerspectives.has(perspectiveId)) continue;
      const filtered = [...new Set(Array.isArray(ids) ? ids : [])].filter(id => validTracks.has(id));
      if (filtered.length) next[perspectiveId] = filtered;
    }
    return next;
  }

  function buildPerspectiveGroups(clips, orderedPerspectives = [], orderedTracks = [], collapsedIds = [], hiddenIds = []) {
    const groups = new Map();
    for (const clip of clips) {
      const pKey = perspectiveKey(clip);
      if (!groups.has(pKey)) groups.set(pKey, { id: pKey, name: pKey, tracks: [], clips: [] });
      const group = groups.get(pKey);
      group.clips.push(clip);
      let track = group.tracks.find(t => t.id === clip.trackId);
      if (!track) {
        track = { id: clip.trackId, kind: clip.kind, perspectiveName: pKey, trackName: clip.trackName, clips: [] };
        group.tracks.push(track);
      }
      track.clips.push(clip);
    }
    const pIndex = new Map(orderedPerspectives.map((id, i) => [id, i]));
    const tIndex = new Map(orderedTracks.map((id, i) => [id, i]));
    return [...groups.values()].sort((a, b) => (pIndex.get(a.id) ?? 9999) - (pIndex.get(b.id) ?? 9999) || a.name.localeCompare(b.name)).map(group => {
      group.tracks.sort((a, b) => (tIndex.get(a.id) ?? 9999) - (tIndex.get(b.id) ?? 9999) || (a.kind === b.kind ? a.trackName.localeCompare(b.trackName) : a.kind === 'video' ? -1 : 1));
      group.videoTracks = group.tracks.filter(t => t.kind === 'video');
      group.audioTracks = group.tracks.filter(t => t.kind === 'audio');
      group.collapsed = collapsedIds.includes(group.id);
      group.hidden = hiddenIds.includes(group.id);
      return group;
    });
  }

  function flattenTimelineRows(groups) {
    const rows = [];
    for (const group of groups) {
      rows.push({ type: 'perspective', id: group.id, perspectiveName: group.name, group });
      if (group.collapsed) rows.push({ type: 'collapsed', id: `${group.id}:collapsed`, perspectiveName: group.name, group, kind: 'summary', clips: group.clips });
      else rows.push(...group.tracks.map(track => ({ ...track, type: 'track' })));
    }
    return rows;
  }

  function buildMonitorCells(groups, enabledTrackIds, hiddenPerspectives, ms) {
    const cells = [];
    for (const group of groups) {
      if (hiddenPerspectives.includes(group.id)) continue;
      const track = group.videoTracks.find(t => enabledTrackIds.includes(t.id));
      if (!track) continue;
      const clips = [...track.clips].sort((a, b) => a.wallclockStartMs - b.wallclockStartMs);
      cells.push({
        trackId: track.id,
        perspectiveId: group.id,
        perspectiveName: group.name,
        trackName: track.trackName,
        clips,
        activeClip: clips.find(clip => ms >= clip.wallclockStartMs && ms <= clip.wallclockStartMs + clip.durationMs) || null
      });
    }
    return cells;
  }

  function moveInOrder(listName, id, delta, scopeId = null) {
    if (listName === 'perspective') {
      const base = perspectiveOrder.length ? perspectiveOrder : perspectiveGroups.map(g => g.id);
      perspectiveOrder = moveItem(base, id, delta);
      persistPrefs();
      return;
    }
    const group = scopeId ? perspectiveGroups.find(g => g.id === scopeId) : null;
    const scopedIds = group ? group.tracks.map(t => t.id) : trackOrder;
    if (!scopedIds.includes(id)) return;
    const movedScoped = moveItem(scopedIds, id, delta);
    const movedById = new Map(movedScoped.map((trackId, index) => [trackId, index]));
    const allIds = [...new Set(allClips.map(c => c.trackId))];
    const outside = trackOrder.filter(trackId => !movedById.has(trackId));
    const missingOutside = allIds.filter(trackId => !movedById.has(trackId) && !outside.includes(trackId));
    const nextOrder = [];
    for (const perspective of perspectiveGroups) {
      const idsForPerspective = perspective.id === scopeId ? movedScoped : perspective.tracks.map(t => t.id);
      for (const trackId of idsForPerspective) if (!nextOrder.includes(trackId)) nextOrder.push(trackId);
    }
    for (const trackId of outside.concat(missingOutside)) if (!nextOrder.includes(trackId)) nextOrder.push(trackId);
    trackOrder = nextOrder;
    persistPrefs();
  }

  function moveItem(list, id, delta) {
    const arr = [...list];
    const idx = arr.indexOf(id);
    if (idx < 0) return arr;
    const next = Math.max(0, Math.min(arr.length - 1, idx + delta));
    if (next === idx) return arr;
    arr.splice(idx, 1);
    arr.splice(next, 0, id);
    return arr;
  }

  function togglePerspectiveCollapse(id) {
    collapsedPerspectiveIds = collapsedPerspectiveIds.includes(id) ? collapsedPerspectiveIds.filter(v => v !== id) : [...collapsedPerspectiveIds, id];
    collapsedPerspectiveIds = [...collapsedPerspectiveIds];
    persistPrefs();
  }

  async function togglePerspectiveView(group) {
    playbackToken += 1;
    const id = group.id;
    const ids = group.videoTracks.map(t => t.id);
    if (!ids.length) return;
    const enabledIds = ids.filter(trackId => visibleTrackIds.includes(trackId));
    const disabling = !hiddenPerspectiveIds.includes(id) && enabledIds.length > 0;
    if (disabling) {
      rememberedPerspectiveViewIds = { ...rememberedPerspectiveViewIds, [id]: enabledIds };
      visibleTrackIds = visibleTrackIds.filter(trackId => !ids.includes(trackId));
      hiddenPerspectiveIds = [...new Set([...hiddenPerspectiveIds, id])];
      ids.forEach(disableTrackMedia);
    } else {
      const remembered = (rememberedPerspectiveViewIds[id] || []).filter(trackId => ids.includes(trackId));
      const restoreIds = remembered.length ? remembered : enabledIds.length ? enabledIds : ids;
      visibleTrackIds = [...new Set([...visibleTrackIds, ...restoreIds])];
      hiddenPerspectiveIds = hiddenPerspectiveIds.filter(v => v !== id);
    }
    persistPrefs();
    await tick();
    attachAll();
    seekAll();
    if (playing) await playActiveMedia();
  }

  function groupViewEnabled(group) {
    return !hiddenPerspectiveIds.includes(group.id) && group.videoTracks.some(t => visibleTrackIds.includes(t.id));
  }

  function groupAudioEnabled(group) {
    return group.audioTracks.some(t => activeAudioIds.includes(t.id));
  }

  async function togglePerspectiveAudio(group) {
    playbackToken += 1;
    const ids = group.audioTracks.map(t => t.id);
    if (!ids.length) return;
    const enabledIds = ids.filter(id => activeAudioIds.includes(id));
    if (enabledIds.length) {
      rememberedPerspectiveAudioIds = { ...rememberedPerspectiveAudioIds, [group.id]: enabledIds };
      activeAudioIds = activeAudioIds.filter(id => !ids.includes(id));
      ids.forEach(disableTrackMedia);
    } else {
      const remembered = (rememberedPerspectiveAudioIds[group.id] || []).filter(id => ids.includes(id));
      const restoreIds = remembered.length ? remembered : ids;
      activeAudioIds = [...new Set([...activeAudioIds, ...restoreIds])];
    }
    persistPrefs();
    await tick();
    attachAll();
    seekAll();
    if (playing) await playActiveMedia();
  }

  function timelineEnd(clips, playhead) {
    const maxClipEnd = clips.reduce((max, clip) => Math.max(max, clip.wallclockStartMs + Math.max(clip.durationMs || 0, 1000)), 0);
    const maxRegionEnd = regions.reduce((max, region) => Math.max(max, region.region_end_ms || 0), 0);
    return Math.max(60000, maxClipEnd + 10000, maxRegionEnd + 10000, playhead + 10000);
  }

  function makeTicks(end) {
    const step = end > 20 * 60 * 1000 ? 60000 : end > 5 * 60 * 1000 ? 30000 : 10000;
    const ticks = [];
    for (let ms = 0; ms <= end; ms += step) ticks.push(ms);
    return ticks;
  }

  function streamDetails(stream) {
    if (stream.kind === 'video') return `${stream.codec || 'video'} ${stream.width || '?'}x${stream.height || '?'}`;
    return `${stream.codec || 'audio'} ${stream.channels || '?'}ch ${stream.channelLayout || ''}`.trim();
  }

  function waveBars(clip) {
    const seed = Number(clip.clipId || clip.trackId || 1) + Number(clip.streamIndex || 0) * 17;
    return Array.from({ length: 80 }, (_, i) => 18 + Math.abs(Math.sin((i + 1) * (seed % 11 + 3) * 0.37)) * 72);
  }


  function countStatuses(clips) {
    const counts = { total: clips.length, ready: 0, queued: 0, processing: 0, failed: 0 };
    for (const clip of clips) {
      const status = String(clip.ingestStatus || (clip.hlsURL ? 'SUCCESS' : 'PENDING')).toUpperCase();
      if (status === 'SUCCESS') counts.ready += 1;
      else if (status === 'PROCESSING') counts.processing += 1;
      else if (status === 'FAILED') counts.failed += 1;
      else counts.queued += 1;
    }
    return counts;
  }

  function clipStatus(clip) {
    return String(clip.ingestStatus || (clip.hlsURL ? 'SUCCESS' : 'PENDING')).toLowerCase();
  }

  function statusText(clip) {
    const status = clipStatus(clip);
    if (status === 'success') return '';
    if (status === 'processing') return 'processing';
    if (status === 'failed') return 'failed';
    return 'queued';
  }

  function openAnnotationEditor(type, item, event) {
    event?.preventDefault();
    event?.stopPropagation();
    annotationSaved = '';
    if (type === 'marker') {
      annotationEditor = {
        type,
        id: item.id,
        label: item.label || '',
        note: item.note || '',
        tsMs: Number(item.marker_ts_ms || item.tsMs || 0),
        color: markerColor(item),
        author: annotationAuthor(item)
      };
      return;
    }
    annotationEditor = {
      type,
      id: item.id,
      label: item.label || '',
      note: item.note || '',
      startMs: Number(item.region_start_ms || item.startMs || 0),
      endMs: Number(item.region_end_ms || item.endMs || 0),
      color: regionColor(item),
      author: annotationAuthor(item)
    };
  }

  async function saveAnnotationEditor() {
    if (!annotationEditor) return;
    if (!annotationEditorCanEdit()) { annotationSaved = readOnlyAnnotationMessage(); return; }
    annotationSaved = 'Saving...';
    if (annotationEditor.type === 'marker') {
      await patchJSON(`/api/projects/${current.id}/markers/${annotationEditor.id}`, {
        tsMs: Number(annotationEditor.tsMs || 0),
        label: annotationEditor.label,
        note: annotationEditor.note
      });
    } else {
      await patchJSON(`/api/projects/${current.id}/regions/${annotationEditor.id}`, {
        startMs: Number(annotationEditor.startMs || 0),
        endMs: Number(annotationEditor.endMs || 0),
        label: annotationEditor.label,
        note: annotationEditor.note
      });
    }
    annotationSaved = 'Saved';
    await refreshProject();
  }

  function handleKeyDown(event) {
    if (event.key === 'Escape' && annotationEditor) { event.preventDefault(); annotationEditor = null; annotationSaved = ''; return; }
    if (event.target?.tagName === 'INPUT' || event.target?.tagName === 'TEXTAREA') return;
    if (!current) return;
    if (event.code === 'Space') { event.preventDefault(); togglePlay(); }
    if (event.key === 'm' || event.key === 'M') addMarker();
    if (event.key === 'r' || event.key === 'R') addRegion();
    if (event.key === 'ArrowLeft') { wallclockMs = Math.max(0, wallclockMs - (event.shiftKey ? 1000 : 100)); seekAll(); }
    if (event.key === 'ArrowRight') { wallclockMs += event.shiftKey ? 1000 : 100; seekAll(); }
    if ((event.key === 'Delete' || event.key === 'Backspace') && selectedClip) deleteClip(selectedClip);
  }

  async function syncPlayingMedia() {
    if (!playing) return;
    wallclockMs += 250;
    await tick();
    attachAll();
    const targets = playbackTargets();
    const signature = targets.map(c => `${c.kind}:${c.kind === 'video' ? c.trackId : c.clipId}:${c.clipId}`).join(',');
    const changedTargets = signature !== previousTargetSignature;
    previousTargetSignature = signature;
    pauseInactiveMedia();
    for (const clip of targets) {
      const node = mediaNodeForClip(clip);
      if (!node) continue;
      const expected = clipLocalSeconds(wallclockMs, clip, softNudges[clip.clipId] || 0);
      if (expected >= 0 && expected * 1000 <= clip.durationMs && (changedTargets || Math.abs(node.currentTime - expected) > 0.45)) seekNode(node, expected);
      if (clip.kind === 'audio') node.muted = false;
      if (node.paused && isClipPlaybackEnabled(clip)) await safePlay(node, clip);
    }
  }
</script>

{#if !me}
  <main class="login-shell">
    <section class="login-card">
      <p class="eyebrow">Self-hosted multicam review</p>
      <h1>Multitrack Drifter</h1>
      <label>Username<input bind:value={username} /></label>
      <label>Password<input bind:value={password} type="password" /></label>
      <button onclick={login}>Log in</button>
      {#if error}<p class="error">{error}</p>{/if}
      <p class="muted">Development auth accepts any username/password when DEV_AUTH_ENABLED=true.</p>
    </section>
  </main>
{:else}
  <main class="app-shell">
    <header class="topbar">
      <div class="brand">
        <button class="projects-button" onclick={() => showProjectPicker = !showProjectPicker}>Projects</button>
        <strong>Drifter</strong><span>{current ? current.name : 'No project open'}</span>
      </div>
      <div class="transport-mini">
        <button onclick={togglePlay} disabled={!current}>{playing ? 'Pause' : 'Play'}</button>
        <button class="secondary" onclick={() => { wallclockMs = Math.max(0, wallclockMs - 5000); seekAll(); }} disabled={!current}>-5s</button>
        <strong>{format(wallclockMs)}</strong>
        <button class="secondary" onclick={() => { wallclockMs += 5000; seekAll(); }} disabled={!current}>+5s</button>
        {#if started}<button class="audio-chip" onclick={startSession} title="Audio unlocked — click to re-sync if audio stops">♪ Audio on</button>{/if}
        {#if current && (statusCounts.processing + statusCounts.queued + statusCounts.failed > 0)}<span class="status-strip"><b>{statusCounts.processing + statusCounts.queued}</b> preparing <b class:bad={statusCounts.failed > 0}>{statusCounts.failed}</b> failed</span>{/if}
      </div>
      <div class="user-chip"><span style={`background:${me.color}`}></span>{me.displayName}<button class="secondary" onclick={logout}>Logout</button></div>
    </header>

    {#if showProjectPicker}
      <section class="project-popover panel">
        <div class="panel-title"><h2>Projects</h2><button class="secondary" onclick={() => showProjectPicker = false}>Close</button></div>
        <div class="project-list">
          {#each projects as p}<button class:active={current?.id === p.id} class="secondary" onclick={() => { openProject(p.id); showProjectPicker = false; }}>{p.name}</button>{/each}
        </div>
        <div class="create-row"><input bind:value={newProjectName}><button onclick={createProject}>New</button></div>
      </section>
    {/if}

    <aside class="media-rail">
      {#if current}
        <section class="panel media-bin">
          <div class="panel-title"><h2>Add footage</h2><button class="secondary" onclick={() => browseSources(parentPrefix(sourcePrefix))}>Up</button></div>
          <p class="path-chip">/{sourcePrefix}</p>
          <div class="source-list">
            {#each sources as item}
              {#if item.isPrefix}
                <button class="source-item folder" title={item.ref.path} onclick={() => browseSources(item.ref.path)}>▸ {item.ref.path}</button>
              {:else}
                <button class="source-item" title={item.ref.path} onclick={() => inspectSource(item.ref.path)}>+ {item.name}</button>
              {/if}
            {/each}
          </div>
          {#if importProbe}
            <div class="stream-sheet">
              <div class="panel-title"><strong>{importProbe.name}</strong><button class="secondary" onclick={() => { importProbe = null; importStreams = []; importError = ''; }}>Cancel</button></div>
              <label>Perspective<input bind:value={importPerspective} /></label>
              {#each importStreams as stream}
                <div class="stream-row">
                  <label class="checkline"><input type="checkbox" bind:checked={stream.selected}><strong>{stream.kind.toUpperCase()} {stream.index}</strong><span>{streamDetails(stream)}</span></label>
                  <input bind:value={stream.track} aria-label="Track name" />
                  <input bind:value={stream.displayName} aria-label="Clip name" />
                </div>
              {/each}
              <button onclick={addSelectedStreams}>Add selected streams</button>
              {#if importError}<p class="error">{importError}</p>{/if}
            </div>
          {/if}
          {#if error}<p class="error error-dismissible">{error}<button class="error-close" onclick={() => { error = ''; }} aria-label="Dismiss">×</button></p>{/if}
        </section>
      {:else}
        <section class="panel empty-media"><p class="muted">Open a project from the Projects button.</p></section>
      {/if}
    </aside>

    {#if current}
      <section class="workspace">
        <section class="monitor-panel panel">
          <div class="panel-title">
            <h2>Program Monitor</h2>
            <div class="segmented">
              {#each ['1x1','1x2','2x2','2x3'] as preset}
                <button class:active={gridPreset === preset} class="secondary" onclick={() => { gridPreset = preset; persistPrefs(); }}>{preset}</button>
              {/each}
            </div>
          </div>
          {#if !started}
            <button class="start-overlay" onclick={startSession}>Start review / enable audio</button>
          {/if}
          <div class={`video-grid preset-${gridPreset}`}>
            {#each monitorCells as cell (cell.trackId)}
              <div class="cell">
                <video muted playsinline preload="auto" use:setTrackMedia={cell.trackId} use:fitVideoToCell></video>
                {#if cell.activeClip}
                  {#if !cell.activeClip.hlsURL}
                    <div class={`media-state ${clipStatus(cell.activeClip)}`}>{statusText(cell.activeClip)}</div>
                  {/if}
                  <button class="cell-label" title={`${cell.perspectiveName} / ${cell.trackName}`} onclick={() => selectedClipId = cell.activeClip.clipId}>{cell.perspectiveName} / {cell.trackName}</button>
                {:else}
                  <div class="gap-state">Gap on {cell.perspectiveName} / {cell.trackName}</div>
                  <button class="cell-label" title={`${cell.perspectiveName} / ${cell.trackName}`}>{cell.perspectiveName} / {cell.trackName}</button>
                {/if}
              </div>
            {:else}
              <div class="empty-monitor">Add footage, then enable a video track in the timeline header.</div>
            {/each}
          </div>
        </section>

        <section class="timeline-panel panel">
          <div class="timeline-toolbar">
            <div class="row-tight">
              <button onclick={togglePlay}>{playing ? 'Pause' : 'Play'}</button>
              <button class="secondary" onclick={addMarker}>Marker</button>
              <button class="secondary" onclick={addRegion}>5s Region</button>
              <button class="secondary" onclick={ingest} disabled={statusCounts.queued + statusCounts.processing === 0 && statusCounts.failed === 0}>Retry prepare</button>
              {#if statusCounts.queued + statusCounts.processing + statusCounts.failed > 0}<span class="prepare-readout"><b>{statusCounts.queued}</b> queued · <b>{statusCounts.processing}</b> running · <b class:bad={statusCounts.failed > 0}>{statusCounts.failed}</b> failed</span>{/if}
            </div>
            <div class="hint">Drag ruler = scrub. Drag clips = align. M = marker.</div>
          </div>

          <div class="timeline-body">
            <div class="timeline-label-rail">
              <div class="timeline-rail-corner"></div>
              {#each trackRows as row}
                {#if row.type === 'perspective'}
                  <div class="rail-perspective-row">
                    <div class="perspective-head">
                      <button class="mini" title="Move perspective up" onpointerdown={(event) => event.stopPropagation()} onclick={(event) => { event.stopPropagation(); moveInOrder('perspective', row.id, -1); }}>▲</button>
                      <button class="mini" title="Move perspective down" onpointerdown={(event) => event.stopPropagation()} onclick={(event) => { event.stopPropagation(); moveInOrder('perspective', row.id, 1); }}>▼</button>
                      <button class="collapse-button" onpointerdown={(event) => event.stopPropagation()} onclick={() => togglePerspectiveCollapse(row.id)}>{row.group.collapsed ? '▸' : '▾'} {row.perspectiveName}</button>
                      <button class:armed={groupViewEnabled(row.group)} class="track-toggle" title="Show/hide this perspective in the grid" onclick={() => togglePerspectiveView(row.group)}>View</button>
                      <button class:armed={groupAudioEnabled(row.group)} class="track-toggle audio-toggle" title="Enable/disable all audio tracks in this perspective" onclick={() => togglePerspectiveAudio(row.group)}>Hear</button>
                    </div>
                  </div>
                {:else if row.type === 'collapsed'}
                  <div class="rail-track-row collapsed summary">
                    <div class="track-head summary-head">
                      <strong>{row.perspectiveName}</strong>
                      <span>summary view</span>
                    </div>
                  </div>
                {:else}
                  <div class={`rail-track-row ${row.kind}`}>
                    <div class="track-head">
                      <div class="track-move">
                        <button class="mini" title="Move track up" onpointerdown={(event) => event.stopPropagation()} onclick={(event) => { event.stopPropagation(); moveInOrder('track', row.id, -1, row.perspectiveName); }}>▲</button>
                        <button class="mini" title="Move track down" onpointerdown={(event) => event.stopPropagation()} onclick={(event) => { event.stopPropagation(); moveInOrder('track', row.id, 1, row.perspectiveName); }}>▼</button>
                      </div>
                      <strong>{row.perspectiveName}</strong>
                      <span>{row.trackName}</span>
                      {#if row.kind === 'video'}
                        <button class:armed={visibleTrackIds.includes(row.id)} class="track-toggle" onclick={() => toggleTrack('video', row.id)}>View</button>
                      {:else}
                        <button class:armed={activeAudioIds.includes(row.id)} class="track-toggle audio-toggle" onclick={() => toggleTrack('audio', row.id)}>Hear</button>
                      {/if}
                    </div>
                  </div>
                {/if}
              {/each}
            </div>

            <div class="timeline-scroll" bind:this={timelineViewport}>
              <div class="timeline-canvas" bind:this={timelineCanvas} style={`width:${timelineWidthPx}px`}>
                <div class="ruler" onpointerdown={startPlayheadDrag}>
                  {#each tickMarks as tick}
                    <div class="tick" style={`left:${msToPx(tick)}px`}><span>{format(tick)}</span></div>
                  {/each}
                  {#each markers as marker}
                    <button class="marker-pin" style={markerStyle(marker)} title={marker.label} onpointerdown={(event) => openAnnotationEditor('marker', marker, event)}>◆</button>
                  {/each}
                  {#each regions as region}
                    <button class="region-band" style={regionStyle(region)} title={region.label} onpointerdown={(event) => openAnnotationEditor('region', region, event)}></button>
                  {/each}
                </div>

                <div class="playhead" style={`left:${msToPx(wallclockMs)}px`} onpointerdown={startPlayheadDrag}><span></span></div>

                {#each trackRows as row}
                  {#if row.type === 'perspective'}
                    <div class="lane-perspective-row">
                      <div class="perspective-lane"><span>{row.group.videoTracks.length} video · {row.group.audioTracks.length} audio</span></div>
                    </div>
                  {:else if row.type === 'collapsed'}
                    <div class="lane-track-row collapsed summary">
                      <div class="track-lane summary-lane" onpointerdown={startPlayheadDrag}>
                        {#each row.clips as clip (clip.clipId)}
                          <button
                            class={`clip-block ${clip.kind} ${clipStatus(clip)} summary-clip`}
                            class:selected={selectedClipId === clip.clipId}
                            title={`${clip.displayName || clip.trackName} · ${clip.kind}`}
                            style={clipBlockStyle(clip)}
                            onpointerdown={(event) => startClipDrag(event, clip)}
                            onclick={() => { selectedClipId = clip.clipId; persistPrefs(); }}
                          >
                            {#if clip.kind === 'audio'}
                              <span class="waveform" aria-hidden="true">{#each waveBars(clip) as h}<i style={`height:${h}%`}></i>{/each}</span>
                            {:else}
                              <span class="video-stripes" aria-hidden="true"></span>
                            {/if}
                          </button>
                        {/each}
                      </div>
                    </div>
                  {:else}
                    <div class={`lane-track-row ${row.kind}`}>
                      <div class="track-lane" onpointerdown={startPlayheadDrag}>
                        {#each row.clips as clip (clip.clipId)}
                          {#if dragState?.clipId === clip.clipId}
                            <div class={`clip-block ghost ${clip.kind}`} style={clipBlockStyle(clip, true)}>{clip.displayName}</div>
                          {/if}
                          <button
                            class={`clip-block ${clip.kind} ${clipStatus(clip)}`}
                            class:selected={selectedClipId === clip.clipId}
                            class:dragging={dragState?.clipId === clip.clipId}
                            title={`${clip.displayName || clip.trackName} · ${statusText(clip)}`}
                            style={clipBlockStyle(clip)}
                            onpointerdown={(event) => startClipDrag(event, clip)}
                            onclick={() => { selectedClipId = clip.clipId; persistPrefs(); }}
                          >
                            <span class="clip-title">{clip.displayName || clip.trackName}</span>
                            {#if clipStatus(clip) !== 'success'}<span class={`clip-status ${clipStatus(clip)}`}>{statusText(clip)}</span>{/if}
                            {#if clip.kind === 'audio'}
                              <span class="waveform" aria-hidden="true">{#each waveBars(clip) as h}<i style={`height:${h}%`}></i>{/each}</span>
                            {:else}
                              <span class="video-stripes" aria-hidden="true"></span>
                            {/if}
                            <span class="clip-meta">{format(clip.wallclockStartMs)} · {format(clip.durationMs)}</span>
                            {#if isProjectOwner()}<span class="clip-delete" role="button" tabindex="0" onclick={(event) => { event.stopPropagation(); deleteClip(clip); }}>×</span>{/if}
                          </button>
                        {/each}
                      </div>
                    </div>
                  {/if}
                {:else}
                  <div class="empty-timeline">Add footage from the left. The app prepares browser-playable review media automatically.</div>
                {/each}
              </div>
            </div>
          </div>
        </section>
      </section>

      {#if annotationEditor}
        <section class="annotation-popover" style={`--annotation-color:${annotationEditor.color}`}>
          <div class="popover-title">
            <strong>{annotationEditor.type === 'marker' ? 'Marker note' : 'Region note'}</strong>
            <button class="secondary" onclick={() => { annotationEditor = null; annotationSaved = ''; }}>Close</button>
          </div>
          <p class="annotation-meta">Author: <strong>{annotationEditor.author}</strong></p>
          <label>Label<input bind:value={annotationEditor.label} disabled={!annotationEditorCanEdit()} /></label>
          {#if annotationEditor.type === 'marker'}
            <label>Time <span class="label-hint">{format(annotationEditor.tsMs)}</span><input type="number" bind:value={annotationEditor.tsMs} disabled={!annotationEditorCanEdit()} /></label>
          {:else}
            <div class="two-cols">
              <label>Start <span class="label-hint">{format(annotationEditor.startMs)}</span><input type="number" bind:value={annotationEditor.startMs} disabled={!annotationEditorCanEdit()} /></label>
              <label>End <span class="label-hint">{format(annotationEditor.endMs)}</span><input type="number" bind:value={annotationEditor.endMs} disabled={!annotationEditorCanEdit()} /></label>
            </div>
          {/if}
          <label>Note<textarea bind:value={annotationEditor.note} rows="7" placeholder="Type marker notes here. This stays open until Close or Esc." disabled={!annotationEditorCanEdit()}></textarea></label>
          <div class="popover-actions"><span>{annotationEditorCanEdit() ? annotationSaved : readOnlyAnnotationMessage()}</span><button onclick={saveAnnotationEditor} disabled={!annotationEditorCanEdit()}>Save</button></div>
        </section>
      {/if}

      <div class="audio-deck" aria-label="active audio playback elements">
        {#each audioClips as clip (clip.clipId)}
          {#if clip.hlsURL}
            <audio preload="auto" use:setMedia={clip.clipId}></audio>
          {/if}
        {/each}
      </div>

      <aside class="inspector panel">
        <div class="panel-title"><h2>Inspector</h2><button class="secondary" onclick={refreshProject}>Refresh</button></div>
        {#if selectedClip}
          <section class="inspect-card">
            <p class="eyebrow">Selected clip</p>
            <h3>{selectedClip.displayName}</h3>
            <dl>
              <dt>Track</dt><dd>{selectedClip.perspectiveName} / {selectedClip.trackName}</dd>
              <dt>Kind</dt><dd>{selectedClip.kind} · stream {selectedClip.streamIndex}</dd>
              <dt>Start</dt><dd>{format(selectedClip.wallclockStartMs)}</dd>
              <dt>Duration</dt><dd>{format(selectedClip.durationMs)}</dd>
            </dl>
            <div class="nudge-grid">
              <button class="secondary" onclick={() => moveClip(selectedClip, -1000)} disabled={!isProjectOwner()}>-1s</button>
              <button class="secondary" onclick={() => moveClip(selectedClip, -100)} disabled={!isProjectOwner()}>-100ms</button>
              <button class="secondary" onclick={() => moveClip(selectedClip, 100)} disabled={!isProjectOwner()}>+100ms</button>
              <button class="secondary" onclick={() => moveClip(selectedClip, 1000)} disabled={!isProjectOwner()}>+1s</button>
            </div>
            {#if !isProjectOwner()}<p class="muted">Project owner required to align, rename, or delete clips.</p>{/if}
            <button class="secondary" onclick={renameSelectedClip} disabled={!isProjectOwner()}>Rename</button>
            <button class="danger" onclick={() => deleteClip(selectedClip)} disabled={!isProjectOwner()}>Delete clip</button>
          </section>
        {:else}
          <p class="muted">Select a clip in the timeline to rename, nudge, or delete it.</p>
        {/if}

        <section class="inspect-card mixer-card">
          <h3>Audio mixer</h3>
          <div class="mixer-list">
            {#each audioClips as clip}
              <div class="mixer-row">
                <label class="checkline mixer-label"><input type="checkbox" checked={activeAudioIds.includes(clip.trackId)} onchange={() => toggleTrack('audio', clip.trackId)}><span title={`${clip.perspectiveName} / ${clip.trackName}`}>{clip.perspectiveName} / {clip.trackName}</span></label>
                <input type="range" min="0" max="1" step="0.05" value={volumes[clip.clipId] ?? 0.85} oninput={(event) => setVolume(clip, event.currentTarget.value)}>
              </div>
            {:else}
              <p class="muted">No audio tracks prepared yet.</p>
            {/each}
          </div>
        </section>

        <section class="inspect-card annotation-card">
          <h3>Markers</h3>
          <div class="annotation-list">
            {#each markers as marker}
              <div class="marker-row annotation-row" title={`By ${annotationAuthor(marker)}`}>
                <span style={`color:${markerColor(marker)}`}>●</span>
                <button class="linkish" onclick={(event) => openAnnotationEditor('marker', marker, event)}>{format(marker.marker_ts_ms)}</button>
                <span class="annotation-main"><span class="annotation-label">{marker.label}</span><span class="annotation-author">by {annotationAuthor(marker)}</span></span>
                <button class="secondary" onclick={() => deleteMarker(marker.id)} disabled={!canEditAnnotation(marker)} title={canEditAnnotation(marker) ? 'Delete marker' : readOnlyAnnotationMessage()}>×</button>
              </div>
            {:else}
              <p class="muted">Press M or click Marker.</p>
            {/each}
          </div>
        </section>

        <section class="inspect-card annotation-card">
          <h3>Regions</h3>
          <div class="annotation-list">
            {#each regions as region}
              <div class="marker-row annotation-row" title={`By ${annotationAuthor(region)}`}>
                <span style={`color:${regionColor(region)}`}>■</span>
                <button class="linkish" onclick={(event) => openAnnotationEditor('region', region, event)}>{format(region.region_start_ms)}</button>
                <span class="annotation-main"><span class="annotation-label">{region.label}</span><span class="annotation-author">by {annotationAuthor(region)}</span></span>
              </div>
            {:else}
              <p class="muted">Click 5s Region to add a region.</p>
            {/each}
          </div>
        </section>

        <section class="inspect-card export-list">
          <h3>Export</h3>
          <a href={`/api/projects/${current.id}/export.csv`}>CSV</a>
          <a href={`/api/projects/${current.id}/export.md`}>Markdown</a>
          <a href={`/api/projects/${current.id}/export.json`}>JSON</a>
          <a href={`/api/projects/${current.id}/export.edl`}>EDL</a>
        </section>
      </aside>
    {:else}
      <section class="empty-project panel"><h1>Select or create a project</h1><p class="muted">Then add footage from a local source folder.</p></section>
    {/if}
  </main>
{/if}
