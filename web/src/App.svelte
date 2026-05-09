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
  let newProjectName = 'Local review';
  let showProjectPicker = false;
  let editingProjectName = '';
  let editingProjectDesc = '';
  let showProjectEdit = false;
  let members = [];
  let newMemberUsername = '';
  let newMemberRole = 'editor';
  let memberMessage = '';

  let manifest = { clips: [] };
  let wallclockMs = 0;
  let playing = false;
  let started = false;
  let playbackToken = 0;
  let previousTargetSignature = '';

  let gridPreset = '2x2';
  let visibleTrackIds = [];
  let activeAudioIds = [];
  let softNudges = {};
  let volumes = {};
  let selectedClipId = null;
  let selectedClipIds = [];
  let snapEnabled = true;
  let linkedMoveEnabled = true;
  let perspectiveOrder = [];
  let trackOrder = [];
  let collapsedPerspectiveIds = [];
  let hiddenPerspectiveIds = [];
  let rememberedPerspectiveViewIds = {};
  let rememberedPerspectiveAudioIds = {};

  let markers = [];
  let regions = [];
  let annotationEditor = null;
  let annotationSaved = '';

  let sources = [];
  let sourcePrefix = '';
  let importProbe = null;
  let importPerspective = '';
  let importStreams = [];

  let ingestJobs = [];
  let showIngestPanel = false;

  let presenceUsers = [];
  let ws;
  let wsConnected = false;

  let timelineViewport;
  let timelineCanvas;
  let dragState = null;
  let marqueeState = null;

  let mediaRefs = new Map();
  let cleanups = new Map();
  let attachedUrls = new Map();
  let attachedNodes = new Map();
  let trackMediaRefs = new Map();
  let trackCleanups = new Map();
  let trackAttachedUrls = new Map();
  let trackAttachedNodes = new Map();

  let showColorPicker = false;
  let showKeyboardHelp = false;
  let activeInspectorTab = 'clip';
  let renameTarget = null;
  let renameValue = '';

  $: allClips = manifest.clips || [];
  $: videoClips = allClips.filter(c => c.kind === 'video');
  $: audioClips = allClips.filter(c => c.kind === 'audio');
  $: perspectiveGroups = buildPerspectiveGroups(allClips, perspectiveOrder, trackOrder, collapsedPerspectiveIds, hiddenPerspectiveIds, visibleTrackIds, activeAudioIds);
  $: trackRows = flattenTimelineRows(perspectiveGroups);
  $: monitorCells = buildMonitorCells(perspectiveGroups, visibleTrackIds, hiddenPerspectiveIds, wallclockMs).slice(0, maxCells(gridPreset));
  $: visibleVideos = monitorCells.map(cell => cell.activeClip).filter(Boolean);
  $: selectedClip = allClips.find(c => c.clipId === selectedClipId) || null;
  $: selectedClips = selectedClipIds.map(id => allClips.find(c => String(c.clipId) === String(id))).filter(Boolean);
  $: timelineEndMs = timelineEnd(allClips, wallclockMs);
  $: timelineLaneWidthPx = Math.max(900, Math.ceil(timelineEndMs / 45));
  $: timelineWidthPx = timelineLaneWidthPx;
  $: tickMarks = makeTicks(timelineEndMs);
  $: activeAudioClips = audioClips.filter(c => hasId(activeAudioIds, c.trackId));
  $: statusCounts = countStatuses(allClips);
  $: pendingJobCount = ingestJobs.filter(j => j.state === 'PENDING' || j.state === 'PROCESSING').length;
  $: failedJobCount = ingestJobs.filter(j => j.state === 'FAILED').length;

  onMount(() => {
    window.addEventListener('keydown', handleKeyDown);
    const timer = setInterval(syncPlayingMedia, 250);
    const poller = setInterval(() => {
      if (current && (statusCounts.queued > 0 || statusCounts.processing > 0)) refreshProject();
      if (current && showIngestPanel && pendingJobCount > 0) refreshIngestJobs();
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
    me = null; current = null; projects = []; disconnectMedia(); presenceUsers = [];
  }

  async function setMyColor(color) {
    me = await postJSON('/api/me/color', { color });
    showColorPicker = false;
  }

  async function loadProjects() { projects = await api('/api/projects'); }

  async function createProject() {
    const p = await postJSON('/api/projects', { name: newProjectName, description: '' });
    await loadProjects();
    await openProject(p.id);
  }

  async function saveProjectEdit() {
    if (!current) return;
    await patchJSON(`/api/projects/${current.id}`, { name: editingProjectName, description: editingProjectDesc });
    current = { ...current, name: editingProjectName, description: editingProjectDesc };
    showProjectEdit = false;
    await loadProjects();
  }

  async function openProject(id) {
    current = await api(`/api/projects/${id}`);
    manifest = await api(`/api/projects/${id}/playback-manifest`);
    reconcileOrdering();
    markers = await api(`/api/projects/${id}/markers`);
    regions = await api(`/api/projects/${id}/regions`);
    await refreshMembers();
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
    selectedClipIds = Array.isArray(prefs.selectedClipIds) ? prefs.selectedClipIds : (selectedClipId ? [selectedClipId] : []);
    snapEnabled = prefs.snapEnabled ?? true;
    linkedMoveEnabled = prefs.linkedMoveEnabled ?? true;
    reconcileSelection();
    connectWS(id);
    if (canAnnotateProject()) await browseSources('');
    else sources = [];
    await refreshIngestJobs();
    await tick();
    attachAll();
  }

  async function refreshProject() {
    if (!current) return;
    const id = current.id;
    current = await api(`/api/projects/${id}`);
    manifest = await api(`/api/projects/${id}/playback-manifest`);
    reconcileOrdering();
    reconcileSelection();
    markers = await api(`/api/projects/${id}/markers`);
    regions = await api(`/api/projects/${id}/regions`);
    await refreshMembers();
    await tick();
    attachAll();
  }

  function connectWS(id) {
    if (ws) ws.close();
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    ws = new WebSocket(`${proto}//${location.host}/ws/projects/${id}`);
    ws.onopen = () => { wsConnected = true; };
    ws.onclose = () => { wsConnected = false; };
    ws.onmessage = async (ev) => {
      const msg = JSON.parse(ev.data);
      if (msg.type === 'presence.snapshot') {
        presenceUsers = msg.payload?.users || [];
      } else if (msg.type === 'user.joined') {
        const u = msg.payload;
        if (!presenceUsers.find(p => p.username === u.username)) presenceUsers = [...presenceUsers, u];
      } else if (msg.type === 'user.left') {
        presenceUsers = presenceUsers.filter(p => p.username !== msg.payload?.username);
      } else if (msg.type === 'clip.ingest.progress') {
        applyJobProgress(msg.payload || {});
      } else if (msg.type?.startsWith('project.member.')) {
        try {
          await refreshMembers();
          if (!canAnnotateProject()) { sources = []; importProbe = null; importStreams = []; }
          await loadProjects();
        } catch (e) {
          current = null; manifest = { clips: [] }; markers = []; regions = []; members = []; sources = [];
          if (ws) ws.close();
          setError('Your access to this project changed.');
          await loadProjects();
        }
      } else if (msg.type?.startsWith('marker.') || msg.type?.startsWith('region.') || msg.type?.startsWith('clip.')) {
        await refreshProject();
        if (msg.type?.startsWith('clip.') || msg.type?.startsWith('ingest.')) await refreshIngestJobs();
      }
    };
  }

  async function browseSources(prefix) {
    if (!current || !canAnnotateProject()) return;
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
    importProbe = await api(`/api/projects/${current.id}/sources/probe?path=${encodeURIComponent(path)}`);
    importPerspective = inferPerspective(path);
    importStreams = (importProbe.streams || []).map((stream) => ({
      ...stream, selected: true,
      track: stream.label || (stream.kind === 'audio' ? 'Audio' : 'Video'),
      displayName: `${path.split('/').pop()} - ${stream.label || stream.kind}`
    }));
  }

  async function addSelectedStreams() {
    const streams = importStreams.filter(s => s.selected).map(s => ({ streamIndex: s.index, kind: s.kind, track: s.track.trim(), displayName: s.displayName.trim(), label: s.label }));
    if (!streams.length) { importError = 'Select at least one stream.'; return; }
    await postJSON(`/api/projects/${current.id}/assets`, { sourcePath: importProbe.sourcePath, perspective: importPerspective, wallclockStartMs: wallclockMs, streams });
    importProbe = null; importStreams = []; importError = '';
    await refreshProject();
    await refreshIngestJobs();
    setTimeout(() => { refreshProject(); refreshIngestJobs(); }, 1200);
  }

  async function ingest() {
    await postJSON(`/api/projects/${current.id}/ingest`, {});
    await refreshIngestJobs();
    setTimeout(refreshProject, 1200);
  }

  async function refreshIngestJobs() {
    if (!current) return;
    ingestJobs = await api(`/api/projects/${current.id}/ingest-jobs`);
  }

  async function refreshMembers() {
    if (!current) { members = []; return; }
    members = await api(`/api/projects/${current.id}/members`);
  }

  async function addProjectMember() {
    if (!current || !isProjectOwner()) return;
    const usernameToAdd = newMemberUsername.trim();
    if (!usernameToAdd) { memberMessage = 'Enter a username.'; return; }
    memberMessage = 'Adding…';
    try {
      await postJSON(`/api/projects/${current.id}/members`, { username: usernameToAdd, role: newMemberRole });
      newMemberUsername = '';
      newMemberRole = 'editor';
      memberMessage = 'Added';
      await refreshMembers();
    } catch (e) {
      memberMessage = e.message;
    }
  }

  async function updateProjectMemberRole(member, role) {
    if (!current || !isProjectOwner() || member.role === 'owner') return;
    memberMessage = 'Saving…';
    try {
      await patchJSON(`/api/projects/${current.id}/members/${encodeURIComponent(member.username)}`, { role });
      memberMessage = 'Saved';
      await refreshMembers();
    } catch (e) {
      memberMessage = e.message;
    }
  }

  async function removeProjectMember(member) {
    if (!current || !isProjectOwner() || member.role === 'owner') return;
    if (!confirm(`Remove ${member.displayName || member.username} from this project?`)) return;
    memberMessage = 'Removing…';
    try {
      await del(`/api/projects/${current.id}/members/${encodeURIComponent(member.username)}`);
      memberMessage = 'Removed';
      await refreshMembers();
    } catch (e) {
      memberMessage = e.message;
    }
  }

  function applyJobProgress(payload) {
    const jobId = Number(payload.jobId || payload.job_id || 0);
    if (!jobId) return;
    let found = false;
    ingestJobs = ingestJobs.map((job) => {
      if (Number(job.id) !== jobId) return job;
      found = true;
      return {
        ...job,
        state: 'PROCESSING',
        stage: payload.stage || job.stage || 'transcoding',
        progress_pct: Math.max(jobProgress(job), Number(payload.pct ?? job.progress_pct ?? 0)),
        progress_time_ms: payload.timeMs ?? job.progress_time_ms ?? 0,
        total_duration_ms: payload.totalDurationMs ?? job.total_duration_ms ?? 0,
        ffmpeg_frame: payload.frame ?? job.ffmpeg_frame ?? 0,
        ffmpeg_fps: payload.fps ?? job.ffmpeg_fps ?? 0,
        ffmpeg_bitrate: payload.bitrate ?? job.ffmpeg_bitrate ?? '',
        ffmpeg_speed: payload.speed ?? job.ffmpeg_speed ?? ''
      };
    });
    if (!found) refreshIngestJobs();
  }

  async function retryClipIngest(clipId) {
    if (!current) return;
    await postJSON(`/api/projects/${current.id}/clips/${clipId}/ingest`, {});
    await refreshProject();
    await refreshIngestJobs();
  }

  async function addMarker() {
    if (!canAnnotateProject()) { setError(projectEditorMessage()); return; }
    await postJSON(`/api/projects/${current.id}/markers`, { tsMs: wallclockMs, label: 'Marker', note: '' });
  }
  async function addRegion() {
    if (!canAnnotateProject()) { setError(projectEditorMessage()); return; }
    await postJSON(`/api/projects/${current.id}/regions`, { startMs: wallclockMs, endMs: wallclockMs + 5000, label: 'Region', note: '' });
  }

  async function deleteMarker(id) {
    const m = markers.find(m => String(m.id) === String(id));
    if (m && !canEditAnnotation(m)) return;
    await del(`/api/projects/${current.id}/markers/${id}`);
  }

  async function deleteRegion(id) {
    const r = regions.find(r => String(r.id) === String(id));
    if (r && !canEditAnnotation(r)) return;
    await del(`/api/projects/${current.id}/regions/${id}`);
  }

  function openAnnotationEditor(type, item, event) {
    event?.preventDefault(); event?.stopPropagation();
    annotationSaved = '';
    if (type === 'marker') {
      annotationEditor = { type, id: item.id, label: item.label || '', note: item.note || '', tsMs: Number(item.marker_ts_ms || 0), color: markerColor(item), author: annotationAuthor(item) };
    } else {
      annotationEditor = { type, id: item.id, label: item.label || '', note: item.note || '', startMs: Number(item.region_start_ms || 0), endMs: Number(item.region_end_ms || 0), color: regionColor(item), author: annotationAuthor(item) };
    }
  }

  async function saveAnnotationEditor() {
    if (!annotationEditor || !annotationEditorCanEdit()) { annotationSaved = readOnlyAnnotationMessage(); return; }
    annotationSaved = 'Saving…';
    if (annotationEditor.type === 'marker') {
      await patchJSON(`/api/projects/${current.id}/markers/${annotationEditor.id}`, { tsMs: Number(annotationEditor.tsMs || 0), label: annotationEditor.label, note: annotationEditor.note });
    } else {
      await patchJSON(`/api/projects/${current.id}/regions/${annotationEditor.id}`, { startMs: Number(annotationEditor.startMs || 0), endMs: Number(annotationEditor.endMs || 0), label: annotationEditor.label, note: annotationEditor.note });
    }
    annotationSaved = 'Saved';
    await refreshProject();
  }

  function startRename(type, id, name) { renameTarget = { type, id }; renameValue = name; }

  async function commitRename() {
    if (!renameTarget || !current || !renameValue.trim()) { renameTarget = null; return; }
    const name = renameValue.trim();
    if (renameTarget.type === 'perspective') {
      await patchJSON(`/api/projects/${current.id}/perspectives/${renameTarget.id}`, { name });
    }
    renameTarget = null;
    await refreshProject();
  }

  async function moveClipTo(clip, startMs) {
    const plan = movementPlanFor(clip);
    const start = Math.max(0, Math.round(startMs));
    const starts = {};
    for (const item of plan.movingClips) starts[item.clipId] = start;
    await moveClipStarts(starts);
  }

  async function moveClip(clip, delta) {
    const plan = movementPlanFor(clip);
    await moveClipStarts(startsForMovementDelta(plan, delta));
  }

  async function moveClipStarts(startsById) {
    if (!isProjectOwner()) { setError(projectOwnerMessage()); return; }
    const entries = Object.entries(startsById)
      .map(([id, start]) => [id, Math.max(0, Math.round(Number(start)))])
      .filter(([id, start]) => Number.isFinite(start) && allClips.some(c => String(c.clipId) === String(id) && Math.round(c.wallclockStartMs) !== start));
    if (!entries.length) return;
    await patchJSON(`/api/projects/${current.id}/clips`, {
      updates: entries.map(([clipId, wallclockStartMs]) => ({ clipId: Number(clipId), wallclockStartMs }))
    });
    await refreshProject();
  }

  function selectClip(clip, event = null) {
    const id = clip.clipId;
    const multi = !!(event?.shiftKey || event?.metaKey || event?.ctrlKey);
    if (multi) {
      selectedClipIds = hasId(selectedClipIds, id) ? selectedClipIds.filter(v => String(v) !== String(id)) : [...selectedClipIds, id];
      if (!selectedClipIds.length) selectedClipIds = [id];
    } else if (!hasId(selectedClipIds, id) || selectedClipIds.length > 1) {
      selectedClipIds = [id];
    }
    selectedClipId = id;
    activeInspectorTab = 'clip';
    persistPrefs();
  }

  function isClipSelected(clip) { return hasId(selectedClipIds, clip.clipId); }

  function reconcileSelection() {
    const valid = new Set(allClips.map(c => String(c.clipId)));
    selectedClipIds = selectedClipIds.filter(id => valid.has(String(id)));
    if (selectedClipId && !valid.has(String(selectedClipId))) selectedClipId = selectedClipIds[0] || null;
    if (selectedClipId && !hasId(selectedClipIds, selectedClipId)) selectedClipIds = [selectedClipId, ...selectedClipIds];
  }

  function linkedGroupClipIdsFor(clip) {
    if (!linkedMoveEnabled || !clip?.linkGroupId) return [];
    return allClips.filter(c => c.linkGroupId && c.linkGroupId === clip.linkGroupId).map(c => c.clipId);
  }

  function movementClipSetFor(anchor) {
    const ids = new Set((isClipSelected(anchor) && selectedClipIds.length ? selectedClipIds : [anchor.clipId]).map(String));
    if (linkedMoveEnabled) {
      for (const clip of allClips) {
        if (!ids.has(String(clip.clipId))) continue;
        for (const linkedId of linkedGroupClipIdsFor(clip)) ids.add(String(linkedId));
      }
    }
    return allClips.filter(c => ids.has(String(c.clipId)));
  }

  function movementPlanFor(anchor) {
    const movingClips = movementClipSetFor(anchor);
    const originalStarts = Object.fromEntries(movingClips.map(c => [c.clipId, c.wallclockStartMs]));
    const baseStarts = { ...originalStarts };
    if (linkedMoveEnabled) {
      const groupBaseStarts = {};
      const setGroupBase = (groupId, start) => {
        if (!groupId || Object.prototype.hasOwnProperty.call(groupBaseStarts, groupId)) return;
        groupBaseStarts[groupId] = start;
      };
      if (anchor?.linkGroupId) groupBaseStarts[anchor.linkGroupId] = originalStarts[anchor.clipId] ?? anchor.wallclockStartMs;
      for (const clip of movingClips) {
        if (!clip.linkGroupId) continue;
        const selectedInGroup = movingClips.find(c => c.linkGroupId === clip.linkGroupId && hasId(selectedClipIds, c.clipId));
        setGroupBase(clip.linkGroupId, originalStarts[selectedInGroup?.clipId ?? clip.clipId]);
      }
      for (const clip of movingClips) {
        if (clip.linkGroupId && Object.prototype.hasOwnProperty.call(groupBaseStarts, clip.linkGroupId)) {
          baseStarts[clip.clipId] = groupBaseStarts[clip.linkGroupId];
        }
      }
    }
    return { movingClips, originalStarts, baseStarts };
  }

  function startsForMovementDelta(plan, delta) {
    const starts = {};
    for (const item of plan.movingClips) starts[item.clipId] = Math.max(0, Math.round((plan.baseStarts[item.clipId] ?? item.wallclockStartMs) + delta));
    return starts;
  }

  function dragOriginalStart(clip) { return dragState?.originalStarts?.[clip.clipId] ?? clip.wallclockStartMs; }
  function dragPreviewStart(clip) { return dragState?.previewStarts?.[clip.clipId] ?? clip.wallclockStartMs; }
  function isClipDragging(clip) { return !!dragState?.clipIds?.some(id => String(id) === String(clip.clipId)); }

  function clipSnapTargets(movingIds) {
    const ids = new Set(movingIds.map(String));
    const targets = [0, wallclockMs];
    for (const clip of allClips) {
      if (ids.has(String(clip.clipId))) continue;
      targets.push(clip.wallclockStartMs, clip.wallclockStartMs + Math.max(0, clip.durationMs || 0));
    }
    for (const marker of markers) targets.push(Number(marker.marker_ts_ms || 0));
    for (const region of regions) targets.push(Number(region.region_start_ms || 0), Number(region.region_end_ms || 0));
    return targets.filter(Number.isFinite);
  }

  function snapDragDelta(rawDelta, movingClips, baseStarts) {
    let delta = rawDelta;
    const minStart = Math.min(...movingClips.map(c => baseStarts[c.clipId]));
    delta = Math.max(delta, -minStart);
    if (!snapEnabled) return delta;
    const ids = movingClips.map(c => c.clipId);
    const targets = clipSnapTargets(ids);
    const thresholdMs = Math.max(40, Math.round((10 / Math.max(1, timelineLaneWidthPx)) * timelineEndMs));
    let best = { distance: Infinity, delta };
    for (const clip of movingClips) {
      const start = baseStarts[clip.clipId];
      const end = start + Math.max(0, clip.durationMs || 0);
      for (const target of targets) {
        for (const candidate of [target - start, target - end]) {
          const clamped = Math.max(candidate, -minStart);
          const distance = Math.abs(clamped - rawDelta);
          if (distance < best.distance && distance <= thresholdMs) best = { distance, delta: clamped };
        }
      }
    }
    return best.delta;
  }

  function hasId(list, id) { return (list || []).some(v => String(v) === String(id)); }
  function removeIds(list, ids) { const deny = new Set((ids || []).map(String)); return (list || []).filter(v => !deny.has(String(v))); }
  function addIds(list, ids) {
    const seen = new Set((list || []).map(String));
    const next = [...(list || [])];
    for (const id of ids || []) if (!seen.has(String(id))) { seen.add(String(id)); next.push(id); }
    return next;
  }

  function setSoftNudge(clipId, ms) {
    softNudges = { ...softNudges, [clipId]: Number(ms) };
    persistPrefs();
    seekAll();
  }

  async function renameSelectedClip() {
    if (!selectedClip || !isProjectOwner()) return;
    const name = prompt('Clip name', selectedClip.displayName || '');
    if (name === null) return;
    await patchJSON(`/api/projects/${current.id}/clips/${selectedClip.clipId}`, { displayName: name });
    await refreshProject();
  }

  function clipIdOf(clipOrId) {
    const id = clipOrId?.clipId ?? clipOrId?.id ?? clipOrId;
    return id === undefined || id === null || id === '' ? null : id;
  }

  function uniqueClipIds(ids) {
    const seen = new Set();
    const next = [];
    for (const raw of ids || []) {
      const id = clipIdOf(raw);
      if (id === null) continue;
      const key = String(id);
      if (seen.has(key)) continue;
      seen.add(key); next.push(id);
    }
    return next;
  }

  function clipDeleteLabel(ids) {
    const unique = uniqueClipIds(ids);
    if (unique.length === 1) {
      const clip = allClips.find(c => String(c.clipId) === String(unique[0]));
      return `"${clip?.displayName || 'selected clip'}"`;
    }
    return `${unique.length} selected clips`;
  }

  function removeDeletedClipsFromUI(ids) {
    const doomed = new Set(uniqueClipIds(ids).map(String));
    if (!doomed.size) return;
    const removedClips = (manifest.clips || []).filter(c => doomed.has(String(c.clipId)));
    for (const clip of removedClips) cleanupClipMedia(clip.clipId);
    manifest = { ...manifest, clips: (manifest.clips || []).filter(c => !doomed.has(String(c.clipId))) };
    selectedClipIds = selectedClipIds.filter(id => !doomed.has(String(id)));
    if (selectedClipId && doomed.has(String(selectedClipId))) selectedClipId = selectedClipIds[0] || null;
    reconcileOrdering();
    reconcileSelection();
    persistPrefs();
  }

  function isAlreadyDeletedError(error) {
    return error?.status === 404 || /(^|\s)404(\s|$)/.test(error?.message || '');
  }

  async function deleteClipIds(ids) {
    if (!current || !isProjectOwner()) return false;
    const targetIds = uniqueClipIds(ids);
    if (!targetIds.length) return false;

    const results = await Promise.all(targetIds.map(async (id) => {
      try {
        await del(`/api/projects/${current.id}/clips/${id}`);
        return { id, deleted: true };
      } catch (error) {
        if (isAlreadyDeletedError(error)) return { id, deleted: true, alreadyGone: true };
        return { id, deleted: false, error };
      }
    }));

    const deletedIds = results.filter(r => r.deleted).map(r => r.id);
    const failed = results.filter(r => !r.deleted);
    removeDeletedClipsFromUI(deletedIds);

    try { await refreshProject(); }
    catch (error) { if (!failed.length) setError(`Deleted clips, but refresh failed: ${error.message}`); }

    if (failed.length) {
      selectedClipIds = failed.map(r => r.id);
      selectedClipId = selectedClipIds[0] || null;
      persistPrefs();
      const message = failed.length === 1 ? failed[0].error.message : `${failed.length} clips could not be deleted`;
      setError(message);
      return false;
    }
    return true;
  }

  async function deleteClip(clip = selectedClip) {
    const id = clipIdOf(clip);
    if (!id || !isProjectOwner()) return;
    const label = clip?.displayName || 'clip';
    if (!confirm(`Remove "${label}" from this project timeline?`)) return;
    await deleteClipIds([id]);
  }

  async function deleteSelectedClips() {
    const ids = uniqueClipIds(selectedClipIds.length ? selectedClipIds : selectedClips.map(c => c.clipId));
    if (!ids.length || !isProjectOwner()) return;
    if (!confirm(`Remove ${clipDeleteLabel(ids)} from this project timeline?`)) return;
    await deleteClipIds(ids);
  }

  async function detachSelectedClips() {
    if (!selectedClips.length || !isProjectOwner()) return;
    const linked = selectedClips.filter(c => c.linkGroupId);
    if (!linked.length) return;
    for (const clip of linked) await patchJSON(`/api/projects/${current.id}/clips/${clip.clipId}`, { linkGroupId: '' });
    await refreshProject();
  }

  function persistPrefs() {
    if (!current) return;
    savePrefs(current.id, { gridPreset, visibleTrackIds, activeAudioIds, softNudges, volumes, selectedClipId, selectedClipIds, snapEnabled, linkedMoveEnabled, perspectiveOrder, trackOrder, collapsedPerspectiveIds, hiddenPerspectiveIds, rememberedPerspectiveViewIds, rememberedPerspectiveAudioIds });
  }

  function disconnectMedia() {
    for (const c of cleanups.values()) c();
    for (const c of trackCleanups.values()) c();
    cleanups.clear(); attachedUrls.clear(); attachedNodes.clear(); mediaRefs.clear();
    trackCleanups.clear(); trackAttachedUrls.clear(); trackAttachedNodes.clear(); trackMediaRefs.clear();
  }

  function cleanupClipMedia(clipId) { const c = cleanups.get(clipId); if (c) c(); cleanups.delete(clipId); attachedUrls.delete(clipId); attachedNodes.delete(clipId); }
  function cleanupTrackMedia(trackId) { const c = trackCleanups.get(trackId); if (c) c(); trackCleanups.delete(trackId); trackAttachedUrls.delete(trackId); trackAttachedNodes.delete(trackId); }

  function setMedia(node, key) {
    if (node) { mediaRefs.set(key, node); attachedUrls.delete(key); attachedNodes.delete(key); }
    return {
      update: (nk) => { if (nk === key) return; mediaRefs.delete(key); cleanupClipMedia(key); key = nk; mediaRefs.set(key, node); },
      destroy: () => { mediaRefs.delete(key); cleanupClipMedia(key); }
    };
  }

  function setTrackMedia(node, trackId) {
    if (node) { trackMediaRefs.set(trackId, node); trackAttachedUrls.delete(trackId); trackAttachedNodes.delete(trackId); }
    return {
      update: (nt) => { if (nt === trackId) return; trackMediaRefs.delete(trackId); cleanupTrackMedia(trackId); trackId = nt; trackMediaRefs.set(trackId, node); },
      destroy: () => { trackMediaRefs.delete(trackId); cleanupTrackMedia(trackId); }
    };
  }

  function attachAll() { attachAudioClips(); attachVideoCells(); }

  function attachAudioClips() {
    const live = new Set(audioClips.map(c => c.clipId));
    for (const [id, c] of cleanups.entries()) { if (!live.has(id)) { c(); cleanups.delete(id); attachedUrls.delete(id); attachedNodes.delete(id); } }
    for (const clip of audioClips) {
      const node = mediaRefs.get(clip.clipId);
      if (!node || !clip.hlsURL) continue;
      node.muted = !hasId(activeAudioIds, clip.trackId);
      node.volume = Number(volumes[clip.clipId] ?? 0.85);
      if (attachedUrls.get(clip.clipId) === clip.hlsURL && attachedNodes.get(clip.clipId) === node) continue;
      const ex = cleanups.get(clip.clipId); if (ex) ex();
      cleanups.set(clip.clipId, attachHLS(node, clip.hlsURL));
      attachedUrls.set(clip.clipId, clip.hlsURL); attachedNodes.set(clip.clipId, node);
    }
  }

  function attachVideoCells() {
    const live = new Set(monitorCells.map(c => c.trackId));
    for (const [id, c] of trackCleanups.entries()) { if (!live.has(id)) { c(); trackCleanups.delete(id); trackAttachedUrls.delete(id); trackAttachedNodes.delete(id); } }
    for (const cell of monitorCells) {
      const node = trackMediaRefs.get(cell.trackId);
      if (!node) continue;
      const clip = cell.activeClip;
      if (!clip || !clip.hlsURL || !isClipPlaybackEnabled(clip)) { node.pause(); node.dataset.activeClipId = ''; continue; }
      node.muted = true; node.dataset.activeClipId = String(clip.clipId);
      if (trackAttachedUrls.get(cell.trackId) === clip.hlsURL && trackAttachedNodes.get(cell.trackId) === node) continue;
      const ex = trackCleanups.get(cell.trackId); if (ex) ex();
      resetMediaElement(node);
      trackCleanups.set(cell.trackId, attachHLS(node, clip.hlsURL));
      trackAttachedUrls.set(cell.trackId, clip.hlsURL); trackAttachedNodes.set(cell.trackId, node);
    }
  }

  function mediaNodeForClip(clip) { return clip.kind === 'video' ? trackMediaRefs.get(clip.trackId) : mediaRefs.get(clip.clipId); }
  function resetMediaElement(node) { if (!node) return; try { node.pause(); } catch (_) {} try { node.removeAttribute('src'); node.load(); } catch (_) {} }

  function setError(msg, ms = 5000) {
    error = msg;
    if (errorTimer) clearTimeout(errorTimer);
    if (msg && ms > 0) errorTimer = setTimeout(() => { error = ''; errorTimer = null; }, ms);
  }

  function seekNode(node, seconds) {
    if (!node || !Number.isFinite(seconds)) return;
    const apply = () => { try { node.currentTime = Math.max(0, seconds); } catch (_) {} };
    if (node.readyState > 0) apply();
    else node.addEventListener('loadedmetadata', apply, { once: true });
  }

  function isPlaybackTargetNow(clip) { return playbackTargets().some(t => t.clipId === clip.clipId); }

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
    try { await node.play(); } catch (e) {
      const msg = e?.message || '';
      if (e?.name === 'AbortError' || /abort|interrupted|removed|detached/i.test(msg)) return;
      queueReadyPlay(node, clip, token);
      if (token === playbackToken && isClipPlaybackEnabled(clip) && node.readyState > 0) setError(`Playback blocked for ${clip.displayName || clip.trackName}: ${msg}`);
    }
  }

  async function startSession() {
    started = true; await tick();
    attachAll(); seekAll(); playing = true;
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
    if (!started) { await startSession(); return; }
    playbackToken += 1;
    playing = !playing;
    if (playing) await playActiveMedia();
    else pauseAllMedia();
  }

  async function playActiveMedia() {
    const token = playbackToken;
    await tick();
    if (token !== playbackToken) return;
    attachAll(); pauseInactiveMedia();
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

  function pauseAllMedia() { playbackToken += 1; for (const n of mediaRefs.values()) n.pause(); for (const n of trackMediaRefs.values()) n.pause(); }

  function pauseInactiveMedia() {
    const targets = playbackTargets();
    const tAudio = new Set(targets.filter(c => c.kind === 'audio').map(c => c.clipId));
    const tVideo = new Set(targets.filter(c => c.kind === 'video').map(c => c.trackId));
    for (const clip of audioClips) { if (tAudio.has(clip.clipId)) continue; const n = mediaRefs.get(clip.clipId); if (n) { n.pause(); n.muted = true; } }
    for (const [id, n] of trackMediaRefs.entries()) { if (!tVideo.has(id)) n.pause(); }
  }

  function playbackTargets() {
    return [...visibleVideos, ...activeAudioClips].filter(clip => clip.hlsURL && isClipPlaybackEnabled(clip) && wallclockMs >= clip.wallclockStartMs && wallclockMs <= clip.wallclockStartMs + clip.durationMs);
  }

  function isClipPlaybackEnabled(clip) {
    if (clip.kind === 'video') return hasId(visibleTrackIds, clip.trackId) && !hiddenPerspectiveIds.includes(perspectiveKey(clip));
    return hasId(activeAudioIds, clip.trackId);
  }

  async function toggleTrack(listName, id) {
    playbackToken += 1;
    const list = listName === 'video' ? visibleTrackIds : activeAudioIds;
    const disabling = hasId(list, id);
    const next = disabling ? removeIds(list, [id]) : addIds(list, [id]);
    if (listName === 'video') visibleTrackIds = next; else activeAudioIds = next;
    if (disabling) disableTrackMedia(id);
    persistPrefs();
    await tick(); attachAll(); seekAll();
    if (playing) await playActiveMedia();
  }

  function disableTrackMedia(trackId) { for (const clip of allClips.filter(c => c.trackId === trackId)) { const n = mediaNodeForClip(clip); if (n) { n.pause(); if (clip.kind === 'audio') n.muted = true; } } }

  function setVolume(clip, value) {
    volumes = { ...volumes, [clip.clipId]: Number(value) };
    const n = mediaRefs.get(clip.clipId); if (n) n.volume = Number(value);
    persistPrefs();
  }

  function startPlayheadDrag(event) {
    if (!timelineCanvas || !timelineViewport) return;
    if (event.shiftKey) { startMarqueeSelect(event); return; }
    event.preventDefault();
    event.currentTarget?.setPointerCapture?.(event.pointerId);
    document.body.classList.add('dragging-timeline');
    setWallclockFromClient(event.clientX); seekAll();
    const move = (ev) => { ev.preventDefault(); setWallclockFromClient(ev.clientX); seekAll(); };
    const up = () => { document.body.classList.remove('dragging-timeline'); window.removeEventListener('pointermove', move); window.removeEventListener('pointerup', up); };
    window.addEventListener('pointermove', move); window.addEventListener('pointerup', up);
  }

  function startLanePointerDown(event) {
    if (event.shiftKey) startMarqueeSelect(event);
    else startPlayheadDrag(event);
  }

  function startClipDrag(event, clip) {
    event.preventDefault(); event.stopPropagation();
    const multi = !!(event.shiftKey || event.metaKey || event.ctrlKey);
    if (!multi && isClipSelected(clip)) {
      selectedClipId = clip.clipId;
      activeInspectorTab = 'clip';
      persistPrefs();
    } else {
      selectClip(clip, event);
    }
    if (!isProjectOwner()) return;
    event.currentTarget?.setPointerCapture?.(event.pointerId);
    document.body.classList.add('dragging-timeline');
    const plan = movementPlanFor(clip);
    const { movingClips, originalStarts, baseStarts } = plan;
    dragState = { clipId: clip.clipId, clipIds: movingClips.map(c => c.clipId), originalStarts, baseStarts, previewStarts: { ...originalStarts }, pointerStartMs: clientToTimelineMs(event.clientX), moved: false };
    const move = (ev) => {
      ev.preventDefault();
      if (!dragState) return;
      const rawDelta = clientToTimelineMs(ev.clientX) - dragState.pointerStartMs;
      const delta = snapDragDelta(rawDelta, movingClips, baseStarts);
      const previewStarts = startsForMovementDelta(plan, delta);
      const moved = Object.entries(previewStarts).some(([id, start]) => Math.abs(start - (originalStarts[id] ?? 0)) >= 1);
      dragState = { ...dragState, previewStarts, moved };
      wallclockMs = previewStarts[clip.clipId] ?? wallclockMs;
    };
    const up = async () => {
      document.body.classList.remove('dragging-timeline');
      window.removeEventListener('pointermove', move); window.removeEventListener('pointerup', up);
      const finalDrag = dragState; dragState = null;
      if (finalDrag?.moved) await moveClipStarts(finalDrag.previewStarts);
    };
    window.addEventListener('pointermove', move); window.addEventListener('pointerup', up);
  }

  function startMarqueeSelect(event) {
    if (!timelineCanvas || !timelineViewport) return;
    event.preventDefault(); event.stopPropagation();
    const startX = event.clientX;
    const startY = event.clientY;
    marqueeState = { startX, startY, x: startX, y: startY };
    document.body.classList.add('dragging-timeline');
    const move = (ev) => { ev.preventDefault(); marqueeState = { ...marqueeState, x: ev.clientX, y: ev.clientY }; };
    const up = () => {
      document.body.classList.remove('dragging-timeline');
      window.removeEventListener('pointermove', move); window.removeEventListener('pointerup', up);
      const box = marqueeClientRect(marqueeState);
      const ids = [];
      document.querySelectorAll('[data-clip-id]').forEach((node) => {
        const rect = node.getBoundingClientRect();
        if (rectsIntersect(box, rect)) ids.push(node.dataset.clipId);
      });
      selectedClipIds = [...new Set(ids.map(id => Number(id) || id))];
      selectedClipId = selectedClipIds[selectedClipIds.length - 1] || null;
      activeInspectorTab = 'clip';
      marqueeState = null;
      persistPrefs();
    };
    window.addEventListener('pointermove', move); window.addEventListener('pointerup', up);
  }

  function marqueeClientRect(state) {
    const left = Math.min(state?.startX ?? 0, state?.x ?? 0);
    const top = Math.min(state?.startY ?? 0, state?.y ?? 0);
    const right = Math.max(state?.startX ?? 0, state?.x ?? 0);
    const bottom = Math.max(state?.startY ?? 0, state?.y ?? 0);
    return { left, top, right, bottom, width: right - left, height: bottom - top };
  }

  function marqueeStyle(state) {
    if (!state || !timelineCanvas) return '';
    const rect = timelineCanvas.getBoundingClientRect();
    const box = marqueeClientRect(state);
    return `left:${box.left - rect.left}px;top:${box.top - rect.top}px;width:${box.width}px;height:${box.height}px;`;
  }

  function rectsIntersect(a, b) { return a.left <= b.right && a.right >= b.left && a.top <= b.bottom && a.bottom >= b.top; }

  function setWallclockFromClient(clientX) { wallclockMs = snapMs(clientToTimelineMs(clientX), 25); }
  function clientToTimelineMs(clientX) {
    if (!timelineViewport) return wallclockMs;
    const rect = timelineViewport.getBoundingClientRect();
    const x = Math.max(0, clientX - rect.left + timelineViewport.scrollLeft);
    return Math.min(timelineEndMs, Math.round((x / timelineLaneWidthPx) * timelineEndMs));
  }

  function moveInOrder(listName, id, delta, scopeId = null) {
    if (listName === 'perspective') { perspectiveOrder = moveItem(perspectiveOrder.length ? perspectiveOrder : perspectiveGroups.map(g => g.id), id, delta); persistPrefs(); return; }
    const group = scopeId ? perspectiveGroups.find(g => g.id === scopeId) : null;
    const scopedIds = group ? group.tracks.map(t => t.id) : trackOrder;
    if (!hasId(scopedIds, id)) return;
    const moved = moveItem(scopedIds, id, delta);
    const nextOrder = [];
    for (const p of perspectiveGroups) { const ids = p.id === scopeId ? moved : p.tracks.map(t => t.id); for (const tid of ids) if (!nextOrder.includes(tid)) nextOrder.push(tid); }
    const all = [...new Set(allClips.map(c => c.trackId))];
    for (const tid of trackOrder.filter(t => !hasId(moved, t)).concat(all.filter(t => !hasId(nextOrder, t)))) if (!hasId(nextOrder, tid)) nextOrder.push(tid);
    trackOrder = nextOrder; persistPrefs();
  }

  function moveItem(list, id, delta) {
    const arr = [...list]; const idx = arr.indexOf(id); if (idx < 0) return arr;
    const next = Math.max(0, Math.min(arr.length - 1, idx + delta)); if (next === idx) return arr;
    arr.splice(idx, 1); arr.splice(next, 0, id); return arr;
  }

  function togglePerspectiveCollapse(id) { collapsedPerspectiveIds = collapsedPerspectiveIds.includes(id) ? collapsedPerspectiveIds.filter(v => v !== id) : [...collapsedPerspectiveIds, id]; persistPrefs(); }

  async function togglePerspectiveView(group) {
    playbackToken += 1;
    const id = group.id; const ids = group.videoTracks.map(t => t.id);
    if (!ids.length) return;
    const enabled = ids.filter(t => hasId(visibleTrackIds, t));
    if (!hiddenPerspectiveIds.includes(id) && enabled.length > 0) {
      rememberedPerspectiveViewIds = { ...rememberedPerspectiveViewIds, [id]: enabled };
      visibleTrackIds = removeIds(visibleTrackIds, ids);
      hiddenPerspectiveIds = [...new Set([...hiddenPerspectiveIds, id])];
      ids.forEach(disableTrackMedia);
    } else {
      const rem = (rememberedPerspectiveViewIds[id] || []).filter(t => hasId(ids, t));
      visibleTrackIds = addIds(visibleTrackIds, rem.length ? rem : enabled.length ? enabled : ids);
      hiddenPerspectiveIds = hiddenPerspectiveIds.filter(v => v !== id);
    }
    persistPrefs(); await tick(); attachAll(); seekAll(); if (playing) await playActiveMedia();
  }


  async function togglePerspectiveAudio(group) {
    playbackToken += 1;
    const ids = group.audioTracks.map(t => t.id);
    if (!ids.length) return;
    const enabled = ids.filter(id => hasId(activeAudioIds, id));
    if (enabled.length > 0) {
      rememberedPerspectiveAudioIds = { ...rememberedPerspectiveAudioIds, [group.id]: enabled };
      activeAudioIds = removeIds(activeAudioIds, ids);
      ids.forEach(disableTrackMedia);
    } else {
      const rem = (rememberedPerspectiveAudioIds[group.id] || []).filter(id => hasId(ids, id));
      activeAudioIds = addIds(activeAudioIds, rem.length ? rem : ids);
    }
    persistPrefs(); await tick(); attachAll(); seekAll(); if (playing) await playActiveMedia();
  }

  function timelineEnd(clips, playhead) {
    const mc = clips.reduce((m, c) => Math.max(m, c.wallclockStartMs + Math.max(c.durationMs || 0, 1000)), 0);
    const mr = regions.reduce((m, r) => Math.max(m, r.region_end_ms || 0), 0);
    return Math.max(60000, mc + 10000, mr + 10000, playhead + 10000);
  }

  function makeTicks(end) {
    const step = end > 20*60*1000 ? 60000 : end > 5*60*1000 ? 30000 : 10000;
    const t = [];
    for (let ms = 0; ms <= end; ms += step) t.push(ms);
    return t;
  }

  function reconcileOrdering() {
    const pKeys = [...new Set(allClips.map(c => perspectiveKey(c)))].sort((a, b) => a.localeCompare(b));
    perspectiveOrder = [...perspectiveOrder.filter(p => pKeys.includes(p)), ...pKeys.filter(p => !perspectiveOrder.includes(p))];
    const tKeys = [...new Set(allClips.map(c => c.trackId))];
    const trackById = new Map(allClips.map(c => [c.trackId, c]));
    const existing = trackOrder.filter(t => tKeys.includes(t));
    const known = new Set(existing);
    const fallback = [...tKeys].sort((a, b) => {
      const ca = trackById.get(a); const cb = trackById.get(b);
      const pa = perspectiveOrder.indexOf(perspectiveKey(ca)); const pb = perspectiveOrder.indexOf(perspectiveKey(cb));
      if (pa !== pb) return pa - pb;
      if (ca.kind !== cb.kind) return ca.kind === 'video' ? -1 : 1;
      return String(ca.trackName || '').localeCompare(String(cb.trackName || ''));
    });
    trackOrder = [...existing, ...fallback.filter(t => !existing.includes(t))];
    visibleTrackIds = visibleTrackIds.filter(id => hasId(tKeys, id));
    activeAudioIds = activeAudioIds.filter(id => hasId(tKeys, id));
    for (const id of tKeys) {
      const clip = trackById.get(id);
      if (!known.has(id) && clip?.kind === 'video' && !hasId(visibleTrackIds, id)) visibleTrackIds = addIds(visibleTrackIds, [id]);
      if (!known.has(id) && clip?.kind === 'audio' && !hasId(activeAudioIds, id)) activeAudioIds = addIds(activeAudioIds, [id]);
    }
    collapsedPerspectiveIds = collapsedPerspectiveIds.filter(p => pKeys.includes(p));
    hiddenPerspectiveIds = hiddenPerspectiveIds.filter(p => pKeys.includes(p));
    rememberedPerspectiveViewIds = pruneTrackMemory(rememberedPerspectiveViewIds, tKeys, pKeys);
    rememberedPerspectiveAudioIds = pruneTrackMemory(rememberedPerspectiveAudioIds, tKeys, pKeys);
  }

  function pruneTrackMemory(memory, validTrackIds, validPerspectiveIds) {
    const vt = new Set(validTrackIds); const vp = new Set(validPerspectiveIds);
    const next = {};
    for (const [pid, ids] of Object.entries(memory || {})) {
      if (!vp.has(pid)) continue;
      const f = [...new Set(Array.isArray(ids) ? ids : [])].filter(id => vt.has(id));
      if (f.length) next[pid] = f;
    }
    return next;
  }

  function buildPerspectiveGroups(clips, orderedP = [], orderedT = [], collapsedIds = [], hiddenIds = [], enabledVideoTrackIds = [], enabledAudioTrackIds = []) {
    const groups = new Map();
    for (const clip of clips) {
      const pk = perspectiveKey(clip);
      if (!groups.has(pk)) groups.set(pk, { id: pk, name: pk, tracks: [], clips: [] });
      const g = groups.get(pk); g.clips.push(clip);
      let track = g.tracks.find(t => t.id === clip.trackId);
      if (!track) { track = { id: clip.trackId, kind: clip.kind, perspectiveName: pk, trackName: clip.trackName, clips: [] }; g.tracks.push(track); }
      track.clips.push(clip);
    }
    const pIdx = new Map(orderedP.map((id, i) => [id, i]));
    const tIdx = new Map(orderedT.map((id, i) => [id, i]));
    return [...groups.values()].sort((a, b) => (pIdx.get(a.id) ?? 9999) - (pIdx.get(b.id) ?? 9999) || a.name.localeCompare(b.name)).map(g => {
      g.tracks.sort((a, b) => (tIdx.get(a.id) ?? 9999) - (tIdx.get(b.id) ?? 9999) || (a.kind === b.kind ? a.trackName.localeCompare(b.trackName) : a.kind === 'video' ? -1 : 1));
      g.videoTracks = g.tracks.filter(t => t.kind === 'video');
      g.audioTracks = g.tracks.filter(t => t.kind === 'audio');
      g.collapsed = collapsedIds.includes(g.id);
      g.hidden = hiddenIds.includes(g.id);
      g.viewEnabled = !g.hidden && g.videoTracks.some(t => hasId(enabledVideoTrackIds, t.id));
      g.audioEnabled = g.audioTracks.some(t => hasId(enabledAudioTrackIds, t.id));
      return g;
    });
  }

  function flattenTimelineRows(groups) {
    const rows = [];
    for (const g of groups) {
      rows.push({ type: 'perspective', id: g.id, perspectiveName: g.name, group: g });
      if (g.collapsed) rows.push({ type: 'collapsed', id: `${g.id}:collapsed`, perspectiveName: g.name, group: g, kind: 'summary', clips: g.clips });
      else rows.push(...g.tracks.map(t => ({ ...t, type: 'track' })));
    }
    return rows;
  }

  function buildMonitorCells(groups, enabledTrackIds, hiddenPerspectives, ms) {
    const cells = [];
    for (const g of groups) {
      if (hiddenPerspectives.includes(g.id)) continue;
      const track = g.videoTracks.find(t => hasId(enabledTrackIds, t.id));
      if (!track) continue;
      const clips = [...track.clips].sort((a, b) => a.wallclockStartMs - b.wallclockStartMs);
      cells.push({ trackId: track.id, perspectiveId: g.id, perspectiveName: g.name, trackName: track.trackName, clips, activeClip: clips.find(c => ms >= c.wallclockStartMs && ms <= c.wallclockStartMs + c.durationMs) || null });
    }
    return cells;
  }

  function handleKeyDown(event) {
    if (event.key === 'Escape') {
      if (annotationEditor) { event.preventDefault(); annotationEditor = null; annotationSaved = ''; return; }
      if (showColorPicker) { event.preventDefault(); showColorPicker = false; return; }
      if (showKeyboardHelp) { event.preventDefault(); showKeyboardHelp = false; return; }
      if (renameTarget) { event.preventDefault(); renameTarget = null; return; }
    }
    if (event.target?.tagName === 'INPUT' || event.target?.tagName === 'TEXTAREA') return;
    if (!current) return;
    if (event.code === 'Space') { event.preventDefault(); togglePlay(); }
    if (event.key === 'm' || event.key === 'M') addMarker();
    if (event.key === 'r' || event.key === 'R') addRegion();
    if (event.key === 'j' || event.key === 'J') { showIngestPanel = !showIngestPanel; if (showIngestPanel) refreshIngestJobs(); }
    if (event.key === 'ArrowLeft') { wallclockMs = Math.max(0, wallclockMs - (event.shiftKey ? 1000 : 100)); seekAll(); }
    if (event.key === 'ArrowRight') { wallclockMs += event.shiftKey ? 1000 : 100; seekAll(); }
    if ((event.key === 'Delete' || event.key === 'Backspace') && selectedClipIds.length) { event.preventDefault(); deleteSelectedClips(); }
    if (event.key === '?') showKeyboardHelp = !showKeyboardHelp;
  }

  async function syncPlayingMedia() {
    if (!playing) return;
    wallclockMs += 250;
    await tick();
    attachAll();
    const targets = playbackTargets();
    const sig = targets.map(c => `${c.kind}:${c.kind === 'video' ? c.trackId : c.clipId}:${c.clipId}`).join(',');
    const changed = sig !== previousTargetSignature;
    previousTargetSignature = sig;
    pauseInactiveMedia();
    for (const clip of targets) {
      const node = mediaNodeForClip(clip);
      if (!node) continue;
      const exp = clipLocalSeconds(wallclockMs, clip, softNudges[clip.clipId] || 0);
      if (exp >= 0 && exp * 1000 <= clip.durationMs && (changed || Math.abs(node.currentTime - exp) > 0.45)) seekNode(node, exp);
      if (clip.kind === 'audio') node.muted = false;
      if (node.paused && isClipPlaybackEnabled(clip)) await safePlay(node, clip);
    }
  }

  function snapMs(ms, step) { return Math.round(ms / step) * step; }
  function msToLanePx(ms) { return Math.round((Math.max(0, ms) / timelineEndMs) * timelineLaneWidthPx); }
  function msToPx(ms) { return msToLanePx(ms); }
  function maxCells(p) { return p === '1x1' ? 1 : p === '1x2' ? 2 : p === '2x2' ? 4 : 6; }
  function format(ms) { const d = new Date(Math.max(0, ms)); return d.toISOString().substring(11, 23); }
  function inferPerspective(path) { const parts = path.split('/').filter(Boolean); return parts.length > 1 ? parts[parts.length - 2] : 'Default'; }
  function clipBlockStyle(clip, ghost = false) {
    const isDragged = isClipDragging(clip);
    const start = isDragged && !ghost ? dragPreviewStart(clip) : dragOriginalStart(clip);
    return `left:${msToLanePx(start)}px;width:${Math.max(36, msToLanePx(clip.durationMs || 1000))}px;`;
  }
  function markerStyle(m) { return `left:${msToPx(m.marker_ts_ms)}px;color:${markerColor(m)};`; }
  function regionStyle(r) {
    const c = regionColor(r);
    return `left:${msToPx(r.region_start_ms)}px;width:${Math.max(10, msToLanePx(r.region_end_ms - r.region_start_ms))}px;background:${withAlpha(c,'33')};border-color:${withAlpha(c,'aa')};`;
  }
  function markerColor(m) { return m.author_color || m.authorColor || m.color || me?.color || '#f6c85f'; }
  function regionColor(r) { return r.author_color || r.authorColor || r.color || me?.color || '#8f70ff'; }
  function withAlpha(c, a) { return /^#[0-9a-fA-F]{6}$/.test(c) ? `${c}${a}` : c; }
  function isProjectOwner() { return !!(current && me && current.ownerUsername === me.username); }
  function myProjectRole() {
    if (!current || !me) return '';
    if (isProjectOwner()) return 'owner';
    return members.find(m => m.username === me.username)?.role || '';
  }
  function canAnnotateProject() { return ['owner','editor','member'].includes(myProjectRole()); }
  function annotationAuthor(item) { return item?.author_username || item?.authorUsername || item?.author || 'unknown'; }
  function canEditAnnotation(item) { return !!(item && me && (annotationAuthor(item) === me.username || isProjectOwner())); }
  function annotationEditorCanEdit() { return !!(annotationEditor && me && (annotationEditor.author === me.username || isProjectOwner())); }
  function readOnlyAnnotationMessage() { return 'Read-only: only the author or project owner can edit.'; }
  function projectOwnerMessage() { return 'Only the project owner can do this.'; }
  function projectEditorMessage() { return 'Ask the project owner to add you as an editor before marking up this project.'; }
  function perspectiveKey(item) { return item.perspectiveName || item.perspective || 'Default'; }
  function streamDetails(s) { return s.kind === 'video' ? `${s.codec||'video'} ${s.width||'?'}×${s.height||'?'}` : `${s.codec||'audio'} ${s.channels||'?'}ch`.trim(); }
  function waveBars(clip) {
    const seed = Number(clip.clipId || clip.trackId || 1) + Number(clip.streamIndex || 0) * 17;
    return Array.from({ length: 80 }, (_, i) => 18 + Math.abs(Math.sin((i + 1) * (seed % 11 + 3) * 0.37)) * 72);
  }
  function countStatuses(clips) {
    const c = { total: clips.length, ready: 0, queued: 0, processing: 0, failed: 0 };
    for (const clip of clips) {
      const s = String(clip.ingestStatus || (clip.hlsURL ? 'SUCCESS' : 'PENDING')).toUpperCase();
      if (s === 'SUCCESS') c.ready++; else if (s === 'PROCESSING') c.processing++; else if (s === 'FAILED') c.failed++; else c.queued++;
    }
    return c;
  }
  function clipStatus(clip) { return String(clip.ingestStatus || (clip.hlsURL ? 'SUCCESS' : 'PENDING')).toLowerCase(); }
  function statusText(clip) { const s = clipStatus(clip); return s === 'success' ? '' : s === 'processing' ? 'encoding' : s === 'failed' ? 'failed' : 'queued'; }
  function jobProgress(job) { return Math.max(0, Math.min(1, Number(job.progress_pct || 0))); }
  function jobProgressPct(job) { return `${Math.round(jobProgress(job) * 100)}%`; }
  function jobStage(job) { const state = String(job.state || 'job'); return job.stage || (state === 'PROCESSING' ? 'working' : state.toLowerCase()); }
  function jobClipLabel(job) { return job.clip_name || `clip ${job.clip_id}`; }
  function jobStats(job) {
    const parts = [];
    if (Number(job.progress_time_ms) > 0 && Number(job.total_duration_ms) > 0) parts.push(`${format(Number(job.progress_time_ms))}/${format(Number(job.total_duration_ms))}`);
    if (Number(job.ffmpeg_fps) > 0) parts.push(`${Number(job.ffmpeg_fps).toFixed(1)} fps`);
    if (job.ffmpeg_speed) parts.push(`${job.ffmpeg_speed}`);
    if (job.ffmpeg_bitrate) parts.push(`${job.ffmpeg_bitrate}`);
    return parts.join(' · ');
  }

  const ACCENT_COLORS = ['#ff6c70','#f6c85f','#33c899','#5d94ff','#b882ff','#ff9d5c','#60d8ff','#ff72b8'];
</script>

{#if !me}
<main class="login-shell">
  <section class="login-card">
    <div class="login-brand">
      <svg width="32" height="32" viewBox="0 0 32 32" fill="none"><circle cx="16" cy="16" r="15" stroke="#5d94ff" stroke-width="1.5"/><path d="M9 22V10l7 4.5 7-4.5v12l-7-4.5L9 22z" fill="#5d94ff"/></svg>
      <span>DRIFTER</span>
    </div>
    <p class="login-sub">Self-hosted multicam review</p>
    <label>Username<input bind:value={username} autocomplete="username" /></label>
    <label>Password<input bind:value={password} type="password" autocomplete="current-password" /></label>
    <button class="btn-primary full" onclick={login}>Sign in</button>
    {#if error}<p class="error">{error}</p>{/if}
    <p class="muted hint-small">Dev auth accepts any credentials when DEV_AUTH_ENABLED=true.</p>
  </section>
</main>

{:else}
<main class="app-shell">

  <!-- TOP BAR -->
  <header class="topbar">
    <div class="topbar-left">
      <button class="topbar-icon-btn" onclick={() => showProjectPicker = !showProjectPicker} title="Projects">
        <svg width="14" height="14" viewBox="0 0 14 14"><rect x="1" y="1" width="5" height="5" rx="1" stroke="currentColor" stroke-width="1.3" fill="none"/><rect x="8" y="1" width="5" height="5" rx="1" stroke="currentColor" stroke-width="1.3" fill="none"/><rect x="1" y="8" width="5" height="5" rx="1" stroke="currentColor" stroke-width="1.3" fill="none"/><rect x="8" y="8" width="5" height="5" rx="1" stroke="currentColor" stroke-width="1.3" fill="none"/></svg>
      </button>
      <span class="brand-wordmark">DRIFTER</span>
      <span class="topbar-divider"></span>
      {#if current}
        <button class="project-title-btn" onclick={() => { if (isProjectOwner()) { editingProjectName = current.name; editingProjectDesc = current.description || ''; showProjectEdit = true; } }} title={isProjectOwner() ? 'Click to edit project' : current.name}>
          {current.name}
        </button>
        {#if !isProjectOwner()}
          <span class="owner-tag">{current.ownerUsername}</span>
        {/if}
      {:else}
        <span class="no-project-label">No project</span>
      {/if}
    </div>

    <div class="topbar-center">
      <button class="tb-jog" onclick={() => { wallclockMs = Math.max(0, wallclockMs - 10000); seekAll(); }} disabled={!current} title="−10s (Shift+←)">⏮</button>
      <button class="tb-jog" onclick={() => { wallclockMs = Math.max(0, wallclockMs - 1000); seekAll(); }} disabled={!current} title="−1s">◂</button>
      <button class="tb-play {playing ? 'tb-playing' : ''}" onclick={togglePlay} disabled={!current} title={playing ? 'Pause' : 'Play'}>
        {#if playing}■{:else}▶{/if}
      </button>
      <button class="tb-jog" onclick={() => { wallclockMs += 1000; seekAll(); }} disabled={!current} title="+1s">▸</button>
      <button class="tb-jog" onclick={() => { wallclockMs += 10000; seekAll(); }} disabled={!current} title="+10s">⏭</button>
      <span class="timecode-display">{format(wallclockMs)}</span>
      {#if started}
        <button class="audio-live-btn" onclick={startSession} title="Audio unlocked — click to re-sync">♪ LIVE</button>
      {/if}
    </div>

    <div class="topbar-right">
      {#if current && (statusCounts.processing + statusCounts.queued + statusCounts.failed > 0)}
        <button class="status-chip {statusCounts.failed > 0 ? 'chip-warn' : 'chip-info'}" onclick={() => { showIngestPanel = !showIngestPanel; if (showIngestPanel) refreshIngestJobs(); }}>
          {#if statusCounts.processing > 0}<span class="pulse-dot"></span>{/if}
          {statusCounts.queued + statusCounts.processing} preparing
          {#if statusCounts.failed > 0} · <strong>{statusCounts.failed} failed</strong>{/if}
        </button>
      {:else if current && statusCounts.total > 0}
        <span class="status-chip chip-ok">{statusCounts.ready}/{statusCounts.total}</span>
      {/if}

      <span class="ws-indicator {wsConnected ? 'ws-live' : 'ws-dead'}" title={wsConnected ? 'Connected' : 'Disconnected'}></span>

      <div class="presence-group">
        {#each presenceUsers.filter(u => u.username !== me?.username).slice(0, 6) as u}
          <span class="presence-dot" style="background:{u.color}" title={u.username}>{u.username[0]?.toUpperCase()}</span>
        {/each}
      </div>

      <button class="user-chip-btn" onclick={() => showColorPicker = !showColorPicker} title="Change accent color">
        <span class="user-color-dot" style="background:{me.color}"></span>
        <span class="user-chip-name">{me.displayName || me.username}</span>
      </button>
      <button class="topbar-icon-btn" onclick={logout} title="Sign out">⏻</button>
      <button class="topbar-icon-btn" onclick={() => showKeyboardHelp = !showKeyboardHelp} title="Keyboard shortcuts (?)">?</button>
    </div>
  </header>

  <!-- FLOATS / POPOVERS -->
  {#if showColorPicker}
    <div class="float-panel color-float">
      <div class="float-head"><span>Accent color</span><button class="topbar-icon-btn" onclick={() => showColorPicker = false}>×</button></div>
      <div class="color-swatches">
        {#each ACCENT_COLORS as c}
          <button class="color-swatch {me.color === c ? 'swatch-active' : ''}" style="background:{c}" onclick={() => setMyColor(c)}></button>
        {/each}
      </div>
    </div>
  {/if}

  {#if showProjectPicker}
    <div class="float-panel project-float">
      <div class="float-head"><span>Projects</span><button class="topbar-icon-btn" onclick={() => showProjectPicker = false}>×</button></div>
      <div class="project-scroll">
        {#each projects as p}
          <button class="project-item {current?.id === p.id ? 'proj-active' : ''}" onclick={() => { openProject(p.id); showProjectPicker = false; }}>
            <span class="proj-name">{p.name}</span>
            <span class="proj-owner">{p.ownerUsername}</span>
          </button>
        {:else}
          <p class="muted padded">No projects yet.</p>
        {/each}
      </div>
      <div class="float-footer">
        <input bind:value={newProjectName} placeholder="New project name" />
        <button onclick={() => { createProject(); showProjectPicker = false; }}>Create</button>
      </div>
    </div>
  {/if}

  {#if showProjectEdit}
    <div class="float-panel project-edit-float">
      <div class="float-head"><span>Edit project</span><button class="topbar-icon-btn" onclick={() => showProjectEdit = false}>×</button></div>
      <label>Name<input bind:value={editingProjectName} /></label>
      <label>Description<textarea bind:value={editingProjectDesc} rows="2"></textarea></label>
      <div class="float-footer">
        <button onclick={saveProjectEdit}>Save</button>
        <button class="btn-ghost" onclick={() => showProjectEdit = false}>Cancel</button>
      </div>
    </div>
  {/if}

  {#if showKeyboardHelp}
    <div class="float-panel kbd-float">
      <div class="float-head"><span>Keyboard shortcuts</span><button class="topbar-icon-btn" onclick={() => showKeyboardHelp = false}>×</button></div>
      <table class="kbd-table">
        <tbody>
          <tr><td><kbd>Space</kbd></td><td>Play / Pause</td></tr>
          <tr><td><kbd>M</kbd></td><td>Add marker at playhead</td></tr>
          <tr><td><kbd>R</kbd></td><td>Add 5s region at playhead</td></tr>
          <tr><td><kbd>J</kbd></td><td>Toggle jobs panel</td></tr>
          <tr><td><kbd>←</kbd> / <kbd>→</kbd></td><td>±100ms</td></tr>
          <tr><td><kbd>⇧←</kbd> / <kbd>⇧→</kbd></td><td>±1s</td></tr>
          <tr><td><kbd>Del</kbd></td><td>Delete selected clip</td></tr>
          <tr><td><kbd>?</kbd></td><td>This help</td></tr>
          <tr><td><kbd>Esc</kbd></td><td>Close panel</td></tr>
        </tbody>
      </table>
    </div>
  {/if}

  {#if showIngestPanel && current}
    <div class="float-panel ingest-float">
      <div class="float-head">
        <span>Ingest jobs</span>
        <div class="float-head-actions">
          <button class="btn-sm" onclick={ingest}>Retry all</button>
          <button class="topbar-icon-btn" onclick={refreshIngestJobs} title="Refresh">⟳</button>
          <button class="topbar-icon-btn" onclick={() => showIngestPanel = false}>×</button>
        </div>
      </div>
      <div class="job-scroll">
        {#each ingestJobs.slice(0, 60) as job}
          <div class="job-row">
            <div class="job-main">
              <div class="job-line">
                <span class="job-badge {job.state === 'SUCCESS' ? 'jb-ok' : job.state === 'FAILED' ? 'jb-fail' : job.state === 'PROCESSING' ? 'jb-run' : 'jb-pend'}">{job.state}</span>
                <span class="job-id">#{job.id}</span>
                <span class="job-clip muted" title={jobClipLabel(job)}>{jobClipLabel(job)}</span>
                {#if job.error}<span class="job-err" title={job.error}>⚠</span>{/if}
                {#if job.state === 'FAILED'}
                  <button class="btn-sm btn-warn-sm" onclick={() => retryClipIngest(job.clip_id)}>Retry</button>
                {/if}
              </div>
              <div class="job-detail">
                <span>{jobStage(job)}</span>
                {#if job.state === 'PROCESSING' || jobProgress(job) > 0}
                  <span class="job-pct">{jobProgressPct(job)}</span>
                {/if}
                {#if jobStats(job)}<span class="job-stats">{jobStats(job)}</span>{/if}
              </div>
              {#if job.state === 'PROCESSING' || jobProgress(job) > 0}
                <div class="job-progress"><i style="width:{jobProgressPct(job)}"></i></div>
              {/if}
            </div>
          </div>
        {:else}
          <p class="muted padded">No jobs.</p>
        {/each}
      </div>
    </div>
  {/if}

  {#if annotationEditor}
    <div class="float-panel ann-float" style="--ann:{annotationEditor.color}">
      <div class="float-head">
        <span class="ann-type-tag">{annotationEditor.type === 'marker' ? '◆ Marker' : '▬ Region'}</span>
        <button class="topbar-icon-btn" onclick={() => { annotationEditor = null; annotationSaved = ''; }}>×</button>
      </div>
      <p class="ann-meta">by <strong>{annotationEditor.author}</strong></p>
      <label>Label<input bind:value={annotationEditor.label} disabled={!annotationEditorCanEdit()} /></label>
      {#if annotationEditor.type === 'marker'}
        <label>Time <span class="label-hint">{format(annotationEditor.tsMs)}</span>
          <input type="number" bind:value={annotationEditor.tsMs} disabled={!annotationEditorCanEdit()} />
        </label>
      {:else}
        <div class="two-col-inputs">
          <label>Start <span class="label-hint">{format(annotationEditor.startMs)}</span>
            <input type="number" bind:value={annotationEditor.startMs} disabled={!annotationEditorCanEdit()} />
          </label>
          <label>End <span class="label-hint">{format(annotationEditor.endMs)}</span>
            <input type="number" bind:value={annotationEditor.endMs} disabled={!annotationEditorCanEdit()} />
          </label>
        </div>
      {/if}
      <label>Note<textarea bind:value={annotationEditor.note} rows="5" placeholder="Notes…" disabled={!annotationEditorCanEdit()}></textarea></label>
      <div class="float-footer">
        <span class="save-msg">{annotationEditorCanEdit() ? annotationSaved : readOnlyAnnotationMessage()}</span>
        <button onclick={saveAnnotationEditor} disabled={!annotationEditorCanEdit()}>Save</button>
      </div>
    </div>
  {/if}

  {#if renameTarget}
    <div class="float-panel rename-float">
      <div class="float-head"><span>Rename {renameTarget.type}</span><button class="topbar-icon-btn" onclick={() => renameTarget = null}>×</button></div>
      <input bind:value={renameValue} onkeydown={(e) => { if (e.key === 'Enter') commitRename(); if (e.key === 'Escape') renameTarget = null; }} />
      <div class="float-footer"><button onclick={commitRename}>Rename</button></div>
    </div>
  {/if}

  {#if current}
  <div class="body-cols">

    <!-- LEFT: Source browser -->
    <aside class="left-panel">
      <div class="panel-head"><span class="panel-label">Sources</span>
        {#if sourcePrefix}<button class="topbar-icon-btn" onclick={() => browseSources(parentPrefix(sourcePrefix))} title="Up">↑</button>{/if}
      </div>
      {#if sourcePrefix}
        <div class="breadcrumb">
          <button class="crumb" onclick={() => browseSources('')}>root</button>
          {#each sourcePrefix.split('/').filter(Boolean) as part, i}
            <span class="crumb-sep">/</span>
            <button class="crumb" onclick={() => browseSources(sourcePrefix.split('/').filter(Boolean).slice(0, i+1).join('/') + '/')}>{part}</button>
          {/each}
        </div>
      {/if}
      <div class="source-file-list">
        {#each sources as item}
          {#if item.isPrefix}
            <button class="src-row src-folder" title={item.ref.path} onclick={() => browseSources(item.ref.path)}>
              <span class="src-icon-folder">📁</span>
              <span class="src-name">{item.ref.path.split('/').filter(Boolean).pop()}/</span>
            </button>
          {:else}
            <button class="src-row src-file" title={item.ref.path} onclick={() => inspectSource(item.ref.path)}>
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
            <button class="topbar-icon-btn" onclick={() => { importProbe = null; importStreams = []; importError = ''; }}>×</button>
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
            Add {importStreams.filter(s=>s.selected).length} stream{importStreams.filter(s=>s.selected).length !== 1 ? 's' : ''} @ {format(wallclockMs)}
          </button>
          {#if importError}<p class="error">{importError}</p>{/if}
        </div>
      {/if}

      {#if error}
        <div class="error-bar">
          <span>{error}</span>
          <button class="topbar-icon-btn" onclick={() => error = ''}>×</button>
        </div>
      {/if}
    </aside>

    <!-- CENTER: Monitor + Timeline -->
    <section class="workspace">

      <!-- Monitor -->
      <section class="monitor-section">
        <div class="monitor-head">
          <span class="panel-label">Program monitor</span>
          <div class="grid-btns">
            {#each ['1x1','1x2','2x2','2x3'] as p}
              <button class="grid-btn {gridPreset === p ? 'grid-active' : ''}" onclick={() => { gridPreset = p; persistPrefs(); }}>{p}</button>
            {/each}
          </div>
          {#if !started}
            <button class="start-review-btn" onclick={startSession}>▶ Start / unlock audio</button>
          {/if}
        </div>
        <div class="video-grid preset-{gridPreset}">
          {#each monitorCells as cell (cell.trackId)}
            <div class="monitor-cell">
              <video muted playsinline preload="auto" use:setTrackMedia={cell.trackId} use:fitVideoToCell></video>
              {#if cell.activeClip}
                {#if !cell.activeClip.hlsURL}
                  <div class="cell-overlay status-overlay {clipStatus(cell.activeClip)}">{statusText(cell.activeClip)}</div>
                {/if}
                <button class="cell-label-btn" title="{cell.perspectiveName} / {cell.trackName}" onclick={() => { selectedClipId = cell.activeClip.clipId; activeInspectorTab = 'clip'; }}>
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
          {:else}
            <div class="empty-monitor">Enable a video track in the timeline header.</div>
          {/each}
        </div>
      </section>

      <!-- Timeline -->
      <section class="timeline-section">
        <div class="timeline-toolbar">
          <div class="tbar-l">
            <button class="tbar-play-btn {playing ? 'tbar-pause' : 'tbar-play'}" onclick={togglePlay}>{playing ? 'Pause' : 'Play'}</button>
            <button class="tbar-tool-btn" onclick={addMarker} title="Marker (M)">
              <svg width="10" height="10" viewBox="0 0 10 10"><path d="M5 1L6.5 4h3l-2.5 2 1 3L5 7l-3 3 1-3L.5 4h3z" fill="#f6c85f"/></svg>Marker
            </button>
            <button class="tbar-tool-btn" onclick={addRegion} title="Region (R)">
              <svg width="10" height="10" viewBox="0 0 10 10"><rect x="0.5" y="3" width="9" height="4" rx="1" stroke="#8f70ff" stroke-width="1.2" fill="rgba(143,112,255,.2)"/></svg>Region
            </button>
            <button class="tbar-tool-btn {snapEnabled ? 'tbar-active' : ''}" onclick={() => { snapEnabled = !snapEnabled; persistPrefs(); }} title="Snap clip edges to playhead, markers, regions, and other clip edges">Snap</button>
            <button class="tbar-tool-btn {linkedMoveEnabled ? 'tbar-active' : ''}" onclick={() => { linkedMoveEnabled = !linkedMoveEnabled; persistPrefs(); }} title="Move linked video/audio clips together">Link</button>
            <div class="tbar-sep"></div>
            <button class="tbar-tool-btn {showIngestPanel ? 'tbar-active' : ''}" onclick={() => { showIngestPanel = !showIngestPanel; if (showIngestPanel) refreshIngestJobs(); }}>
              Jobs {pendingJobCount > 0 ? `(${pendingJobCount})` : ''}{failedJobCount > 0 ? ` ⚠${failedJobCount}` : ''}
            </button>
          </div>
          <div class="tbar-r">
            <span class="tbar-hint">Shift-click = multi-select · Shift-drag empty lane = marquee · Drag selected = move selection · Delete = remove selection</span>
          </div>
        </div>

        <div class="timeline-body">
          <!-- Fixed label rail -->
          <div class="label-rail">
            <div class="label-corner"></div>
            {#each trackRows as row}
              {#if row.type === 'perspective'}
                <div class="label-persp-row">
                  <div class="persp-controls">
                    <div class="persp-order">
                      <button class="mini-btn" onclick={(e) => { e.stopPropagation(); moveInOrder('perspective', row.id, -1); }}>▲</button>
                      <button class="mini-btn" onclick={(e) => { e.stopPropagation(); moveInOrder('perspective', row.id, 1); }}>▼</button>
                    </div>
                    <button class="collapse-btn" onclick={() => togglePerspectiveCollapse(row.id)}>{row.group.collapsed ? '▸' : '▾'}</button>
                    <button class="persp-name-btn" title="Dbl-click to rename" ondblclick={() => startRename('perspective', row.id, row.perspectiveName)}>{row.perspectiveName}</button>
                    <div class="persp-toggles">
                      <button class="toggle-btn {row.group.viewEnabled ? 'tog-v-on' : ''}" aria-pressed={row.group.viewEnabled} title="Show/hide in grid" onclick={() => togglePerspectiveView(row.group)}>V</button>
                      <button class="toggle-btn {row.group.audioEnabled ? 'tog-a-on' : ''}" aria-pressed={row.group.audioEnabled} title="Enable/disable audio" onclick={() => togglePerspectiveAudio(row.group)}>A</button>
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
                      <button class="mini-btn" onclick={(e) => { e.stopPropagation(); moveInOrder('track', row.id, -1, row.perspectiveName); }}>▲</button>
                      <button class="mini-btn" onclick={(e) => { e.stopPropagation(); moveInOrder('track', row.id, 1, row.perspectiveName); }}>▼</button>
                    </div>
                    <div class="track-names">
                      <span class="track-label-name">{row.trackName}</span>
                      <span class="track-label-sub muted">{row.perspectiveName}</span>
                    </div>
                    {#if row.kind === 'video'}
                      <button class="toggle-btn {hasId(visibleTrackIds, row.id) ? 'tog-v-on' : ''}" onclick={() => toggleTrack('video', row.id)} title="Show in grid">V</button>
                    {:else}
                      <button class="toggle-btn {hasId(activeAudioIds, row.id) ? 'tog-a-on' : ''}" onclick={() => toggleTrack('audio', row.id)} title="Enable audio">A</button>
                    {/if}
                  </div>
                </div>
              {/if}
            {/each}
          </div>

          <!-- Scrolling lanes -->
          <div class="lane-scroll" bind:this={timelineViewport}>
            <div class="lane-canvas" bind:this={timelineCanvas} style="width:{timelineWidthPx}px">
              {#if marqueeState}<div class="marquee-box" style={marqueeStyle(marqueeState)}></div>{/if}
              <!-- Ruler -->
              <div class="ruler" onpointerdown={startPlayheadDrag}>
                {#each tickMarks as tick}
                  <div class="tick" style="left:{msToPx(tick)}px"><span>{format(tick)}</span></div>
                {/each}
                {#each markers as m}
                  <button class="marker-pin" style={markerStyle(m)} title="{m.label} – {annotationAuthor(m)}" onpointerdown={(e) => openAnnotationEditor('marker', m, e)}>◆</button>
                {/each}
                {#each regions as r}
                  <button class="region-band" style={regionStyle(r)} title="{r.label} – {annotationAuthor(r)}" onpointerdown={(e) => openAnnotationEditor('region', r, e)}></button>
                {/each}
              </div>
              <div class="playhead" style="left:{msToPx(wallclockMs)}px" onpointerdown={startPlayheadDrag}><span></span></div>

              <!-- Lanes -->
              {#each trackRows as row}
                {#if row.type === 'perspective'}
                  <div class="lane-persp-row">
                    <div class="persp-lane-inner">
                      <span class="persp-lane-meta muted">{row.group.videoTracks.length}V · {row.group.audioTracks.length}A</span>
                    </div>
                  </div>
                {:else if row.type === 'collapsed'}
                  <div class="lane-track-row lane-collapsed">
                    <div class="clip-lane" onpointerdown={startLanePointerDown}>
                      {#each row.clips as clip (clip.clipId)}
                        {#if isClipDragging(clip)}
                          <div class="clip-block {clip.kind} clip-ghost clip-summary" style={clipBlockStyle(clip, true)}></div>
                        {/if}
                        <button data-clip-id={clip.clipId} class="clip-block {clip.kind} {clipStatus(clip)} clip-summary" class:clip-selected={isClipSelected(clip)} class:clip-dragging={isClipDragging(clip)} title="{clip.displayName} · {clip.kind}" style={clipBlockStyle(clip)} onpointerdown={(e) => startClipDrag(e, clip)}>
                          {#if clip.kind === 'audio'}<span class="waveform">{#each waveBars(clip) as h}<i style="height:{h}%"></i>{/each}</span>
                          {:else}<span class="video-stripe"></span>{/if}
                        </button>
                      {/each}
                    </div>
                  </div>
                {:else}
                  <div class="lane-track-row lane-{row.kind}">
                    <div class="clip-lane" onpointerdown={startLanePointerDown}>
                      {#each row.clips as clip (clip.clipId)}
                        {#if isClipDragging(clip)}
                          <div class="clip-block {clip.kind} clip-ghost" style={clipBlockStyle(clip, true)}><span class="clip-title">{clip.displayName}</span></div>
                        {/if}
                        <button data-clip-id={clip.clipId} class="clip-block {clip.kind} {clipStatus(clip)}" class:clip-selected={isClipSelected(clip)} class:clip-dragging={isClipDragging(clip)} class:clip-linked={clip.linkGroupId && linkedMoveEnabled} title="{clip.displayName || clip.trackName}" style={clipBlockStyle(clip)} onpointerdown={(e) => startClipDrag(e, clip)}>
                          <span class="clip-title">{clip.displayName || clip.trackName}</span>
                          {#if clipStatus(clip) !== 'success'}<span class="clip-badge {clipStatus(clip)}">{statusText(clip)}</span>{/if}
                          {#if clip.linkGroupId}<span class="clip-link-badge">🔗</span>{/if}
                          {#if clip.kind === 'audio'}<span class="waveform">{#each waveBars(clip) as h}<i style="height:{h}%"></i>{/each}</span>
                          {:else}<span class="video-stripe"></span>{/if}
                          <span class="clip-timecode">{format(clip.wallclockStartMs)} · {format(clip.durationMs)}</span>
                          {#if isProjectOwner()}<span class="clip-del" role="button" tabindex="0" onpointerdown={(e) => e.stopPropagation()} onclick={(e) => { e.stopPropagation(); deleteClip(clip); }}>×</span>{/if}
                        </button>
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
    </section>

    <!-- RIGHT: Inspector -->
    <aside class="right-panel">
      <div class="inspector-tabs">
        {#each [['clip','Clip'],['mixer','Mix'],['markers','Mkr'],['regions','Rgn'],['export','Export'],['members','People']] as [tab, label]}
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
                    <button class="btn-sm btn-warn-sm" onclick={() => retryClipIngest(selectedClip.clipId)}>Retry</button>
                  {/if}
                </dd>
              </dl>
            </div>

            <div class="ins-section">
              <p class="ins-section-label">Timeline align {isProjectOwner() ? '' : '(owner only)'}</p>
              {#if selectedClips.length > 1}
                <p class="muted">Nudges and drags move all selected clips together.</p>
              {:else if selectedClip.linkGroupId && linkedMoveEnabled}
                <p class="muted">Linked A/V is enabled, so matching video/audio clips move together.</p>
              {/if}
              <div class="nudge-grid">
                <button class="nudge-btn nudge-wide" onclick={() => moveClipTo(selectedClip, wallclockMs)} disabled={!isProjectOwner()} title="Move selected clips so they all start at the current playhead time">Align to Playhead</button>
                <button class="nudge-btn" onclick={() => moveClip(selectedClip, -1000)} disabled={!isProjectOwner()}>−1s</button>
                <button class="nudge-btn" onclick={() => moveClip(selectedClip, -100)} disabled={!isProjectOwner()}>−100ms</button>
                <button class="nudge-btn" onclick={() => moveClip(selectedClip, 100)} disabled={!isProjectOwner()}>+100ms</button>
                <button class="nudge-btn" onclick={() => moveClip(selectedClip, 1000)} disabled={!isProjectOwner()}>+1s</button>
              </div>
            </div>

            <div class="ins-section">
              <p class="ins-section-label">Soft A/V nudge (local only, not saved to server)</p>
              <div class="soft-nudge-row">
                <button class="nudge-btn" onclick={() => setSoftNudge(selectedClip.clipId, (softNudges[selectedClip.clipId] || 0) - 50)}>−50ms</button>
                <span class="soft-val">{softNudges[selectedClip.clipId] || 0}ms</span>
                <button class="nudge-btn" onclick={() => setSoftNudge(selectedClip.clipId, (softNudges[selectedClip.clipId] || 0) + 50)}>+50ms</button>
                <button class="btn-sm-ghost" onclick={() => setSoftNudge(selectedClip.clipId, 0)}>Reset</button>
              </div>
            </div>

            <div class="ins-section ins-actions">
              <button class="action-btn" onclick={renameSelectedClip} disabled={!isProjectOwner() || selectedClips.length !== 1}>Rename…</button>
              <button class="action-btn" onclick={detachSelectedClips} disabled={!isProjectOwner() || !selectedClips.some(c => c.linkGroupId)}>Detach A/V</button>
              <button class="action-btn action-danger" onclick={deleteSelectedClips} disabled={!isProjectOwner()}>Delete {selectedClips.length > 1 ? 'selection' : 'clip'}</button>
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
                  <input type="checkbox" checked={hasId(activeAudioIds, clip.trackId)} onchange={() => toggleTrack('audio', clip.trackId)} />
                  <span class="mixer-name" title="{clip.perspectiveName} / {clip.trackName}">{clip.perspectiveName} / {clip.trackName}</span>
                </label>
                <div class="mixer-vol-row">
                  <input type="range" min="0" max="1" step="0.05" value={volumes[clip.clipId] ?? 0.85} oninput={(e) => setVolume(clip, e.currentTarget.value)} />
                  <span class="vol-pct">{Math.round((volumes[clip.clipId] ?? 0.85) * 100)}%</span>
                </div>
              </div>
            {:else}
              <p class="muted">No audio tracks prepared.</p>
            {/each}
          </div>

        {:else if activeInspectorTab === 'markers'}
          <div class="ann-toolbar">
            <button class="tbar-tool-btn" onclick={addMarker} disabled={!canAnnotateProject()}>+ Marker @ {format(wallclockMs)}</button>
          </div>
          {#if !canAnnotateProject()}<p class="muted padded">You can view this project, but only editors can add markers.</p>{/if}
          {#each markers as m}
            <div class="ann-row">
              <span class="ann-dot" style="background:{markerColor(m)}"></span>
              <button class="ann-time-btn" onclick={(e) => { wallclockMs = Number(m.marker_ts_ms); seekAll(); openAnnotationEditor('marker', m, e); }}>{format(m.marker_ts_ms)}</button>
              <div class="ann-text">
                <span class="ann-label">{m.label || '—'}</span>
                <span class="ann-author muted">{annotationAuthor(m)}</span>
              </div>
              {#if canEditAnnotation(m)}
                <button class="topbar-icon-btn" onclick={() => deleteMarker(m.id)}>×</button>
              {/if}
            </div>
          {:else}
            <p class="muted padded">Press M to add a marker at the playhead.</p>
          {/each}

        {:else if activeInspectorTab === 'regions'}
          <div class="ann-toolbar">
            <button class="tbar-tool-btn" onclick={addRegion} disabled={!canAnnotateProject()}>+ Region @ {format(wallclockMs)}</button>
          </div>
          {#if !canAnnotateProject()}<p class="muted padded">You can view this project, but only editors can add regions.</p>{/if}
          {#each regions as r}
            <div class="ann-row">
              <span class="ann-dot" style="background:{regionColor(r)};border-radius:2px"></span>
              <button class="ann-time-btn" onclick={(e) => { wallclockMs = Number(r.region_start_ms); seekAll(); openAnnotationEditor('region', r, e); }}>{format(r.region_start_ms)}</button>
              <div class="ann-text">
                <span class="ann-label">{r.label || '—'}</span>
                <span class="ann-author muted">{format(r.region_end_ms - r.region_start_ms)} · {annotationAuthor(r)}</span>
              </div>
              {#if canEditAnnotation(r)}
                <button class="topbar-icon-btn" onclick={() => deleteRegion(r.id)}>×</button>
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
          <div class="ins-section">
            <p class="ins-section-label">Maintenance</p>
            <button class="action-btn" onclick={ingest}>Retry all pending / failed</button>
            <button class="action-btn" onclick={refreshProject}>Refresh project</button>
          </div>

        {:else if activeInspectorTab === 'members'}
          <div class="ins-section">
            <p class="ins-section-label">Project access</p>
            {#if isProjectOwner()}
              <div class="member-add-row">
                <input bind:value={newMemberUsername} placeholder="LDAP username" onkeydown={(e) => { if (e.key === 'Enter') addProjectMember(); }} />
                <select bind:value={newMemberRole}>
                  <option value="editor">Editor</option>
                  <option value="viewer">Viewer</option>
                </select>
                <button class="btn-sm" onclick={addProjectMember}>Add</button>
              </div>
              <p class="member-hint muted">Editors can add markers and regions. Viewers can only watch and export. Users must sign in once before they can be added.</p>
              {#if memberMessage}<p class="member-message">{memberMessage}</p>{/if}
            {:else}
              <p class="member-hint muted">Your role: <strong>{myProjectRole() || 'viewer'}</strong>. Ask the owner to change access.</p>
            {/if}
          </div>

          <div class="member-list">
            {#each members as member}
              <div class="member-row">
                <span class="member-avatar" style="background:{member.color}">{member.username[0]?.toUpperCase()}</span>
                <div class="member-main">
                  <span class="member-name">{member.displayName || member.username}</span>
                  <span class="member-user muted">{member.username}</span>
                </div>
                {#if isProjectOwner() && member.role !== 'owner'}
                  <select class="member-role-select" value={member.role === 'member' ? 'editor' : member.role} onchange={(e) => updateProjectMemberRole(member, e.currentTarget.value)}>
                    <option value="editor">Editor</option>
                    <option value="viewer">Viewer</option>
                  </select>
                  <button class="topbar-icon-btn" title="Remove" onclick={() => removeProjectMember(member)}>×</button>
                {:else}
                  <span class="role-pill {member.role}">{member.role === 'member' ? 'editor' : member.role}</span>
                {/if}
              </div>
            {:else}
              <p class="muted padded">No members yet.</p>
            {/each}
          </div>
        {/if}
      </div>
    </aside>

  </div>

  {:else}
  <div class="no-project-screen">
    <button class="no-proj-open-btn" onclick={() => showProjectPicker = true}>Open a project to begin</button>
  </div>
  {/if}

  <!-- Hidden audio elements -->
  <div class="audio-deck" aria-hidden="true">
    {#each audioClips as clip (clip.clipId)}
      {#if clip.hlsURL}
        <audio preload="auto" use:setMedia={clip.clipId}></audio>
      {/if}
    {/each}
  </div>

</main>
{/if}
