<script>
  import { onMount, tick } from 'svelte';
  import { on } from 'svelte/events';
  import { api, postJSON, patchJSON, del, deleteJSON } from './api.js';
  import { clipLocalSeconds, loadPrefs, savePrefs } from './playback.js';
  import LoginView from './components/LoginView.svelte';
  import TopBar from './components/TopBar.svelte';
  import Popovers from './components/Popovers.svelte';
  import ProjectSplash from './components/ProjectSplash.svelte';
  import SourceBrowser from './components/SourceBrowser.svelte';
  import MonitorGrid from './components/MonitorGrid.svelte';
  import TimelineView from './components/TimelineView.svelte';
  import InspectorPanel from './components/InspectorPanel.svelte';
  import RenamePanel from './components/RenamePanel.svelte';
  import AudioDeck from './components/AudioDeck.svelte';
  import { createMediaRegistry } from './lib/media-registry.js';
  import { setAppActions } from './lib/app-actions.js';
  import {
    ANNOTATION_COLORS,
    DEFAULT_GRID_PRESET,
    DEFAULT_MEMBER_ROLE,
    DEFAULT_PROJECT_NAME,
    HISTORY,
    HTTP_STATUS,
    MS_PER_SECOND,
    TIMELINE_LAYOUT,
    TIMING,
    ZOOM
  } from './lib/constants.js';
  import { stepZoomLevel } from './lib/ui.js';
  import {
    hasId,
    removeIds,
    addIds,
    normalizeTrackIds,
    moveItem,
    maxCells,
    inferPerspective,
    perspectiveKey,
    timelineEnd,
    makeTicks,
    buildPerspectiveGroups,
    flattenTimelineRows,
    buildMonitorCells,
    reconcileOrdering,
    countStatuses,
    jobProgress,
    annotationAuthor,
    withAlpha
  } from './lib/timeline.js';

  let me = $state(null);
  let devAuthMode = $state(false);
  let loading = $state(false);
  let loadingProject = $state(false);
  let error = $state('');
  let importError = $state('');
  let errorTimer = null;
  let prefsReady = $state(false);

  let projects = $state([]);
  let current = $state(null);
  let newProjectName = $state(DEFAULT_PROJECT_NAME);
  let showProjectPicker = $state(false);
  let showProjectMenu = $state(false);
  let showMembersPanel = $state(false);
  let showMaintenancePanel = $state(false);
  let editingProjectName = $state('');
  let editingProjectDesc = $state('');
  let showProjectEdit = $state(false);
  let members = $state([]);
  let newMemberUsername = $state('');
  let newMemberRole = $state(DEFAULT_MEMBER_ROLE);
  let memberMessage = $state('');

  let manifest = $state({ clips: [] });
  let wallclockMs = $state(0);
  let playing = $state(false);
  let started = $state(false);
  let playbackToken = 0;
  let previousTargetSignature = '';

  let gridPreset = $state(DEFAULT_GRID_PRESET);
  let visibleTrackIds = $state([]);
  let activeAudioIds = $state([]);
  let softNudges = $state({});
  let volumes = $state({});
  let selectedClipId = $state(null);
  let selectedClipIds = $state([]);
  let snapEnabled = $state(true);
  let linkedMoveEnabled = $state(true);
  let undoStack = $state([]);
  let redoStack = $state([]);
  let perspectiveOrder = $state([]);
  let trackOrder = $state([]);
  let collapsedPerspectiveIds = $state([]);
  let hiddenPerspectiveIds = $state([]);
  let rememberedPerspectiveViewIds = $state({});
  let rememberedPerspectiveAudioIds = $state({});

  let markers = $state([]);
  let regions = $state([]);
  let annotationEditor = $state(null);
  let annotationSaved = $state('');

  let sources = $state([]);
  let sourcePrefix = $state('');
  let importProbe = $state(null);
  let importPerspective = $state('');
  let importStreams = $state([]);

  let ingestJobs = $state([]);
  let showIngestPanel = $state(false);

  let presenceUsers = $state([]);
  let ws;
  let wsConnected = $state(false);

  let timelineViewport = $state(null);
  let timelineCanvas = $state(null);
  let zoomLevel = $state(ZOOM.default);
  let dragState = $state(null);
  let marqueeState = $state(null);

  let showColorPicker = $state(false);
  let showKeyboardHelp = $state(false);
  let activeInspectorTab = $state('clip');
  let renameTarget = $state(null);
  let renameValue = $state('');

  const mediaRegistry = createMediaRegistry();
  const notFoundStatusPattern = new RegExp(`(^|\\s)${HTTP_STATUS.notFound}(\\s|$)`);


  let allClips = $derived(manifest.clips || []);
  let audioClips = $derived(allClips.filter(c => c.kind === 'audio'));
  let perspectiveGroups = $derived(buildPerspectiveGroups(allClips, perspectiveOrder, trackOrder, collapsedPerspectiveIds, hiddenPerspectiveIds, visibleTrackIds, activeAudioIds));
  let trackRows = $derived(flattenTimelineRows(perspectiveGroups));
  let monitorCells = $derived(buildMonitorCells(perspectiveGroups, visibleTrackIds, hiddenPerspectiveIds, wallclockMs).slice(0, maxCells(gridPreset)));
  let visibleVideos = $derived(monitorCells.map(cell => cell.activeClip).filter(Boolean));
  let selectedClip = $derived(allClips.find(c => c.clipId === selectedClipId) || null);
  let selectedClips = $derived(selectedClipIds.map(id => allClips.find(c => String(c.clipId) === String(id))).filter(Boolean));
  let timelineEndMs = $derived(timelineEnd(allClips, wallclockMs, regions));
  let timelineLaneWidthPx = $derived(Math.max(TIMELINE_LAYOUT.minLaneWidthPx, Math.ceil((timelineEndMs / TIMELINE_LAYOUT.msPerBasePixel) * zoomLevel)));
  let timelineWidthPx = $derived(timelineLaneWidthPx);
  let tickMarks = $derived(makeTicks(timelineEndMs));
  let activeAudioClips = $derived(audioClips.filter(c => hasId(activeAudioIds, c.trackId)));
  let statusCounts = $derived(countStatuses(allClips));
  let pendingJobCount = $derived(ingestJobs.filter(j => j.state === 'PENDING' || j.state === 'PROCESSING').length);
  let failedJobCount = $derived(ingestJobs.filter(j => j.state === 'FAILED').length);

  $effect(() => {
    if (!current || !prefsReady) return;
    savePrefs(current.id, {
      gridPreset,
      visibleTrackIds,
      activeAudioIds,
      softNudges,
      volumes,
      selectedClipId,
      selectedClipIds,
      snapEnabled,
      linkedMoveEnabled,
      perspectiveOrder,
      trackOrder,
      collapsedPerspectiveIds,
      hiddenPerspectiveIds,
      rememberedPerspectiveViewIds,
      rememberedPerspectiveAudioIds
    });
  });

  onMount(() => {
    const offKeyDown = on(window, 'keydown', handleKeyDown);
    const timer = setInterval(syncPlayingMedia, TIMING.playbackSyncIntervalMs);
    // Ingest progress comes via WebSocket (clip.ingest.progress events).
    // No polling needed; refreshIngestJobs is called on demand when panel opens.
    const poller = setInterval(() => {
      if (current && showIngestPanel) refreshIngestJobs();
    }, TIMING.ingestRefreshIntervalMs);
    boot();
    return () => {
      offKeyDown();
      clearInterval(timer);
      clearInterval(poller);
    };
  });

  async function boot() {
    try { me = await api('/api/me'); devAuthMode = me.devAuth || false; await loadProjects(); }
    catch (_) { me = null; }
  }

  async function login(username, password) {
    loading = true;
    try { error = ''; me = await postJSON('/api/login', { username, password }); await loadProjects(); }
    catch (e) { setError(e.message, 0); }
    finally { loading = false; }
  }

  async function logout() {
    await postJSON('/api/logout', {});
    me = null; current = null; projects = []; disconnectMedia(); presenceUsers = [];
  }

  async function setMyColor(color) {
    try {
      error = '';
      me = await postJSON('/api/me/color', { color });
      applyUserColor(me.username, me.color);
      showColorPicker = false;
    } catch (e) {
      setError(e.message || 'Could not update accent color', TIMING.colorErrorTimeoutMs);
    }
  }

  async function loadProjects() { projects = await api('/api/projects'); }

  async function createProject(name = newProjectName) {
    const projectName = (name || newProjectName || DEFAULT_PROJECT_NAME).trim();
    newProjectName = projectName;
    const p = await postJSON('/api/projects', { name: projectName, description: '' });
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
    loadingProject = true;
    prefsReady = false;
    // Single round-trip replaces: project + playback-manifest + markers + regions + members
    const state = await api(`/api/projects/${id}/state`);
    current = { id: state.id, name: state.name, description: state.description, ownerUsername: state.ownerUsername };
    const clips = state.clips || [];
    manifest = { clips };
    markers = state.markers || [];
    regions = state.regions || [];
    members = state.members || [];
    const prefs = loadPrefs(id);
    gridPreset = prefs.gridPreset || DEFAULT_GRID_PRESET;
    perspectiveOrder = prefs.perspectiveOrder || [];
    trackOrder = prefs.trackOrder || [];
    collapsedPerspectiveIds = prefs.collapsedPerspectiveIds || [];
    hiddenPerspectiveIds = prefs.hiddenPerspectiveIds || [];
    rememberedPerspectiveViewIds = prefs.rememberedPerspectiveViewIds || {};
    rememberedPerspectiveAudioIds = prefs.rememberedPerspectiveAudioIds || {};
    const allVideoTrackIds = [...new Set(clips.filter(c => c.kind === 'video').map(c => c.trackId))];
    const allAudioTrackIds = [...new Set(clips.filter(c => c.kind === 'audio').map(c => c.trackId))];
    visibleTrackIds = prefs.visibleTrackIds || allVideoTrackIds;
    activeAudioIds = prefs.activeAudioIds || allAudioTrackIds;
    softNudges = prefs.softNudges || {};
    volumes = prefs.volumes || {};
    selectedClipId = prefs.selectedClipId || null;
    selectedClipIds = Array.isArray(prefs.selectedClipIds) ? prefs.selectedClipIds : (selectedClipId ? [selectedClipId] : []);
    snapEnabled = prefs.snapEnabled ?? true;
    linkedMoveEnabled = prefs.linkedMoveEnabled ?? true;
    applyReconciledOrdering(clips);
    reconcileSelection();
    connectWS(id);
    if (canAnnotateProject()) await browseSources('');
    else sources = [];
    await refreshIngestJobs();
    loadingProject = false;
    prefsReady = true;
  }

  async function refreshProject() {
    if (!current) return;
    const id = current.id;
    const state = await api(`/api/projects/${id}/state`);
    current = { id: state.id, name: state.name, description: state.description, ownerUsername: state.ownerUsername };
    const clips = state.clips || [];
    manifest = { clips };
    markers = state.markers || [];
    regions = state.regions || [];
    members = state.members || [];
    applyReconciledOrdering(clips);
    reconcileSelection();
  }

  function applyUserColor(username, color) {
    if (!username || !color) return;
    if (me?.username === username) me = { ...me, color };
    presenceUsers = presenceUsers.map(u => u.username === username ? { ...u, color } : u);
    members = members.map(m => m.username === username ? { ...m, color } : m);
    // markers/regions: authorColor comes from a live JOIN on next /state fetch;
    // patch the open editor so it shows the new color immediately without a refresh.
    if (annotationEditor?.author === username) annotationEditor = { ...annotationEditor, color };
  }

  function connectWS(id) {
    if (ws) ws.close();
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    ws = new WebSocket(`${proto}//${location.host}/ws/projects/${id}`);
    ws.onopen = () => { wsConnected = true; };
    ws.onclose = () => { wsConnected = false; };
    ws.onmessage = (event) => handleSocketMessage(JSON.parse(event.data));
  }

  async function handleSocketMessage(msg) {
    const exactHandlers = {
      'presence.snapshot': handlePresenceSnapshot,
      'user.joined': handleUserJoined,
      'user.left': handleUserLeft,
      'user.color.updated': handleUserColorUpdated,
      'clip.ingest.progress': handleIngestProgress
    };
    const exact = exactHandlers[msg.type];
    if (exact) return exact(msg);
    if (msg.type?.startsWith('project.member.')) return handleMemberProjectChange();
    if (msg.type?.startsWith('marker.') || msg.type?.startsWith('region.') || msg.type?.startsWith('clip.')) {
      return handleProjectDataChange(msg.type);
    }
  }

  function handlePresenceSnapshot(msg) {
    presenceUsers = msg.payload?.users || [];
  }

  function handleUserJoined(msg) {
    const user = msg.payload;
    if (!user?.username) return;
    presenceUsers = presenceUsers.some((present) => present.username === user.username)
      ? presenceUsers.map((present) => present.username === user.username ? { ...present, ...user } : present)
      : [...presenceUsers, user];
  }

  function handleUserLeft(msg) {
    presenceUsers = presenceUsers.filter((present) => present.username !== msg.payload?.username);
  }

  function handleUserColorUpdated(msg) {
    applyUserColor(msg.payload?.username || msg.user, msg.payload?.color || msg.color);
  }

  async function handleMemberProjectChange() {
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
  }

  function handleIngestProgress(msg) {
    applyJobProgress(msg.payload || {});
    if (showIngestPanel) refreshIngestJobs();
  }

  async function handleProjectDataChange(type) {
    await refreshProject();
    if (type?.startsWith('clip.') || type?.startsWith('ingest.')) await refreshIngestJobs();
  }

  async function browseSources(prefix) {
    if (!current || !canAnnotateProject()) return;
    sourcePrefix = prefix;
    sources = await api(`/api/projects/${current.id}/sources?prefix=${encodeURIComponent(prefix)}&delimiter=/`);
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
    setTimeout(() => { refreshProject(); refreshIngestJobs(); }, TIMING.ingestRefreshDelayMs);
  }

  async function ingest() {
    await postJSON(`/api/projects/${current.id}/ingest`, {});
    await refreshIngestJobs();
    setTimeout(refreshProject, TIMING.ingestRefreshDelayMs);
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
      newMemberRole = DEFAULT_MEMBER_ROLE;
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
    await postJSON(`/api/projects/${current.id}/regions`, { startMs: wallclockMs, endMs: wallclockMs + TIMING.defaultRegionDurationMs, label: 'Region', note: '' });
  }

  async function deleteMarker(id) {
    const m = markers.find(m => String(m.id) === String(id));
    if (m && !canEditAnnotation(m)) return;
    await del(`/api/projects/${current.id}/markers/${id}`);
    if (annotationEditor?.type === 'marker' && String(annotationEditor?.id) === String(id)) { annotationEditor = null; annotationSaved = ''; }
  }

  async function deleteRegion(id) {
    const r = regions.find(r => String(r.id) === String(id));
    if (r && !canEditAnnotation(r)) return;
    await del(`/api/projects/${current.id}/regions/${id}`);
    if (annotationEditor?.type === 'region' && String(annotationEditor?.id) === String(id)) { annotationEditor = null; annotationSaved = ''; }
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
    // Push undo snapshot before committing
    const snapshot = {};
    for (const id of Object.keys(startsById)) {
      const clip = allClips.find(c => String(c.clipId) === String(id));
      if (clip) snapshot[id] = clip.wallclockStartMs;
    }
    if (Object.keys(snapshot).length) {
      undoStack = [...undoStack.slice(1 - HISTORY.maxUndoEntries), { clipIds: Object.keys(startsById), starts: snapshot }];
      redoStack = [];
    }
    const entries = Object.entries(startsById)
      .map(([id, start]) => [id, Math.max(0, Math.round(Number(start)))])
      .filter(([id, start]) => Number.isFinite(start) && allClips.some(c => String(c.clipId) === String(id) && Math.round(c.wallclockStartMs) !== start));
    if (!entries.length) return;
    const result = await patchJSON(`/api/projects/${current.id}/clips`, {
      updates: entries.map(([clipId, wallclockStartMs]) => ({ clipId: Number(clipId), wallclockStartMs }))
    });
    if (result?.missingClipIds?.length) removeDeletedClipsFromUI(result.missingClipIds);
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
    const thresholdMs = Math.max(
      TIMELINE_LAYOUT.minimumSnapThresholdMs,
      Math.round((TIMELINE_LAYOUT.snapThresholdPx / Math.max(1, timelineLaneWidthPx)) * timelineEndMs)
    );
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

  function setSoftNudge(clipId, ms) {
    softNudges = { ...softNudges, [clipId]: Number(ms) };

    seekAll();
  }

  async function renameSelectedClip() {
    if (!selectedClip || !isProjectOwner()) return;
    startRename('clip', selectedClip.clipId, selectedClip.displayName || '');
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
    const updatedClips = (manifest.clips || []).filter(c => !doomed.has(String(c.clipId)));
    manifest = { ...manifest, clips: updatedClips };
    selectedClipIds = selectedClipIds.filter(id => !doomed.has(String(id)));
    if (selectedClipId && doomed.has(String(selectedClipId))) selectedClipId = selectedClipIds[0] || null;
    applyReconciledOrdering(updatedClips);
    reconcileSelection();

  }

  function isAlreadyDeletedError(error) {
    return error?.status === HTTP_STATUS.notFound || notFoundStatusPattern.test(error?.message || '');
  }

  async function deleteClipIds(ids) {
    if (!current || !isProjectOwner()) return false;
    const targetIds = uniqueClipIds(ids);
    if (!targetIds.length) return false;

    try {
      const result = await deleteJSON(`/api/projects/${current.id}/clips`, {
        clipIds: targetIds.map(id => Number(id)).filter(Number.isFinite)
      });
      const removed = uniqueClipIds([...(result?.deletedClipIds || []), ...(result?.missingClipIds || [])]);
      removeDeletedClipsFromUI(removed);
      await refreshProject();
      return true;
    } catch (error) {
      setError(error.message || 'Could not delete selected clips');
      try { await refreshProject(); } catch (_) {}
      return false;
    }
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

  function disconnectMedia() {
    mediaRegistry.clear();
  }

  function registerClipMedia(clipId, node) {
    mediaRegistry.registerClip(clipId, node);
  }

  function registerTrackMedia(trackId, node) {
    mediaRegistry.registerTrack(trackId, node);
  }

  function mediaNodeForClip(clip) {
    return mediaRegistry.nodeForClip(clip);
  }

  function setError(msg, ms = TIMING.defaultErrorTimeoutMs) {
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
    seekAll(); playing = true;
    await playActiveMedia();
  }

  function seekAll() {
    previousTargetSignature = '';

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
    pauseInactiveMedia();
    for (const clip of playbackTargets()) {
      if (token !== playbackToken || !isClipPlaybackEnabled(clip)) continue;
      const node = mediaNodeForClip(clip);
      if (!node || !clip.hlsURL) continue;
      if (clip.kind === 'audio') node.muted = false;
      const local = clipLocalSeconds(wallclockMs, clip, softNudges[clip.clipId] || 0);
      if (local >= 0 && local * MS_PER_SECOND <= clip.durationMs && Math.abs(node.currentTime - local) > TIMING.mediaSeekToleranceSeconds) seekNode(node, local);
      await safePlay(node, clip, token);
      if (token !== playbackToken || !isClipPlaybackEnabled(clip)) node.pause();
    }
  }

  function pauseAllMedia() { playbackToken += 1; mediaRegistry.pauseAll(); }

  function pauseInactiveMedia() {
    const targets = playbackTargets();
    mediaRegistry.pauseInactive(
      audioClips,
      targets.filter(c => c.kind === 'audio').map(c => c.clipId),
      targets.filter(c => c.kind === 'video').map(c => c.trackId)
    );
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
    if (listName === 'video') visibleTrackIds = normalizeTrackIds(next); else activeAudioIds = normalizeTrackIds(next);
    if (disabling) disableTrackMedia(id);

    await tick(); seekAll();
    if (playing) await playActiveMedia();
  }

  function disableTrackMedia(trackId) { mediaRegistry.disableTrack(allClips, trackId); }

  function setVolume(clip, value) {
    volumes = { ...volumes, [clip.clipId]: Number(value) };
    const node = mediaNodeForClip(clip); if (node) node.volume = Number(value);
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

  function setWallclockFromClient(clientX) { wallclockMs = snapMs(clientToTimelineMs(clientX), TIMING.timelineSnapStepMs); }
  function clientToTimelineMs(clientX) {
    if (!timelineViewport) return wallclockMs;
    const rect = timelineViewport.getBoundingClientRect();
    const x = Math.max(0, clientX - rect.left + timelineViewport.scrollLeft);
    return Math.min(timelineEndMs, Math.round((x / timelineLaneWidthPx) * timelineEndMs));
  }

  function moveInOrder(listName, id, delta, scopeId = null) {
    if (listName === 'perspective') { perspectiveOrder = moveItem(perspectiveOrder.length ? perspectiveOrder : perspectiveGroups.map(g => g.id), id, delta);  return; }
    const group = scopeId ? perspectiveGroups.find(g => g.id === scopeId) : null;
    const scopedIds = group ? group.tracks.map(t => t.id) : trackOrder;
    if (!hasId(scopedIds, id)) return;
    const moved = moveItem(scopedIds, id, delta);
    const nextOrder = [];
    for (const p of perspectiveGroups) { const ids = p.id === scopeId ? moved : p.tracks.map(t => t.id); for (const tid of ids) if (!nextOrder.includes(tid)) nextOrder.push(tid); }
    const all = [...new Set(allClips.map(c => c.trackId))];
    for (const tid of trackOrder.filter(t => !hasId(moved, t)).concat(all.filter(t => !hasId(nextOrder, t)))) if (!hasId(nextOrder, tid)) nextOrder.push(tid);
    trackOrder = nextOrder;
  }

  function togglePerspectiveCollapse(id) { collapsedPerspectiveIds = collapsedPerspectiveIds.includes(id) ? collapsedPerspectiveIds.filter(v => v !== id) : [...collapsedPerspectiveIds, id];  }

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
      visibleTrackIds = normalizeTrackIds(addIds(visibleTrackIds, rem.length ? rem : enabled.length ? enabled : ids));
      hiddenPerspectiveIds = hiddenPerspectiveIds.filter(v => v !== id);
    }
    await tick(); seekAll(); if (playing) await playActiveMedia();
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
      activeAudioIds = normalizeTrackIds(addIds(activeAudioIds, rem.length ? rem : ids));
    }
    await tick(); seekAll(); if (playing) await playActiveMedia();
  }

  function applyReconciledOrdering(clips = allClips) {
    const next = reconcileOrdering({
      clips,
      perspectiveOrder,
      trackOrder,
      visibleTrackIds,
      activeAudioIds,
      collapsedPerspectiveIds,
      hiddenPerspectiveIds,
      rememberedPerspectiveViewIds,
      rememberedPerspectiveAudioIds
    });

    perspectiveOrder = next.perspectiveOrder;
    trackOrder = next.trackOrder;
    visibleTrackIds = next.visibleTrackIds;
    activeAudioIds = next.activeAudioIds;
    collapsedPerspectiveIds = next.collapsedPerspectiveIds;
    hiddenPerspectiveIds = next.hiddenPerspectiveIds;
    rememberedPerspectiveViewIds = next.rememberedPerspectiveViewIds;
    rememberedPerspectiveAudioIds = next.rememberedPerspectiveAudioIds;
  }

  async function undo() {
    if (!undoStack.length || !current) return;
    const entry = undoStack[undoStack.length - 1];
    // Save redo snapshot
    const redoSnap = {};
    for (const id of entry.clipIds) {
      const clip = allClips.find(c => String(c.clipId) === String(id));
      if (clip) redoSnap[id] = clip.wallclockStartMs;
    }
    redoStack = [...redoStack, { clipIds: entry.clipIds, starts: redoSnap }];
    undoStack = undoStack.slice(0, -1);
    await applyClipStarts(entry.starts);
  }

  async function redo() {
    if (!redoStack.length || !current) return;
    const entry = redoStack[redoStack.length - 1];
    const undoSnap = {};
    for (const id of entry.clipIds) {
      const clip = allClips.find(c => String(c.clipId) === String(id));
      if (clip) undoSnap[id] = clip.wallclockStartMs;
    }
    undoStack = [...undoStack, { clipIds: entry.clipIds, starts: undoSnap }];
    redoStack = redoStack.slice(0, -1);
    await applyClipStarts(entry.starts);
  }

  async function applyClipStarts(starts) {
    const updates = Object.entries(starts).map(([id, ms]) => ({ clipId: Number(id), wallclockStartMs: ms }));
    await patchJSON(`/api/projects/${current.id}/clips`, { updates });
    await refreshProject();
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
    if (event.key === 'ArrowLeft') { wallclockMs = Math.max(0, wallclockMs - (event.shiftKey ? TIMING.largePlayheadJogMs : TIMING.smallPlayheadJogMs)); seekAll(); }
    if (event.key === 'ArrowRight') { wallclockMs += event.shiftKey ? TIMING.largePlayheadJogMs : TIMING.smallPlayheadJogMs; seekAll(); }
    if ((event.key === 'Delete' || event.key === 'Backspace') && selectedClipIds.length) { event.preventDefault(); deleteSelectedClips(); }
    if (event.key === '+' || event.key === '=') { zoomLevel = nextZoomLevel(1); }
    if (event.key === '-') { zoomLevel = nextZoomLevel(-1); }
    if ((event.metaKey || event.ctrlKey) && !event.shiftKey && event.key === 'z') { event.preventDefault(); undo(); }
    if ((event.metaKey || event.ctrlKey) && (event.shiftKey && event.key === 'z' || event.key === 'y')) { event.preventDefault(); redo(); }
    if (event.key === '?') showKeyboardHelp = !showKeyboardHelp;
    if (event.key === 'Escape') { showProjectMenu = false; showMembersPanel = false; showMaintenancePanel = false; }
  }

  async function syncPlayingMedia() {
    if (!playing) return;
    wallclockMs += TIMING.playbackSyncIntervalMs;
    await tick();

    const targets = playbackTargets();
    const sig = targets.map(c => `${c.kind}:${c.kind === 'video' ? c.trackId : c.clipId}:${c.clipId}`).join(',');
    const changed = sig !== previousTargetSignature;
    previousTargetSignature = sig;
    pauseInactiveMedia();
    for (const clip of targets) {
      const node = mediaNodeForClip(clip);
      if (!node) continue;
      const exp = clipLocalSeconds(wallclockMs, clip, softNudges[clip.clipId] || 0);
      if (exp >= 0 && exp * MS_PER_SECOND <= clip.durationMs && (changed || Math.abs(node.currentTime - exp) > TIMING.mediaSeekToleranceSeconds)) seekNode(node, exp);
      if (clip.kind === 'audio') node.muted = false;
      if (node.paused && isClipPlaybackEnabled(clip)) await safePlay(node, clip);
    }
  }

  function snapMs(ms, step) { return Math.round(ms / step) * step; }
  function msToLanePx(ms) { return Math.round((Math.max(0, ms) / timelineEndMs) * timelineLaneWidthPx); }
  function msToPx(ms) { return msToLanePx(ms); }

  function clipBlockStyle(clip, ghost = false) {
    const isDragged = isClipDragging(clip);
    const start = isDragged && !ghost ? dragPreviewStart(clip) : dragOriginalStart(clip);
    return `left:${msToLanePx(start)}px;width:${Math.max(TIMELINE_LAYOUT.minClipWidthPx, msToLanePx(clip.durationMs || TIMING.minimumClipDurationMs))}px;`;
  }
  function markerStyle(m) { return `left:${msToPx(m.marker_ts_ms)}px;color:${markerColor(m)};`; }
  function regionStyle(r) {
    const c = regionColor(r);
    return `left:${msToPx(r.region_start_ms)}px;width:${Math.max(TIMELINE_LAYOUT.minRegionWidthPx, msToLanePx(r.region_end_ms - r.region_start_ms))}px;background:${withAlpha(c, ANNOTATION_COLORS.regionFillAlpha)};border-color:${withAlpha(c, ANNOTATION_COLORS.regionBorderAlpha)};`;
  }
  function colorForUsername(username, fallback) {
    if (username && me?.username === username && me.color) return me.color;
    const member = members.find(m => m.username === username);
    if (member?.color) return member.color;
    const present = presenceUsers.find(u => u.username === username);
    if (present?.color) return present.color;
    return fallback;
  }
  function annotationColor(item, fallback) {
    // authorColor is the single canonical field from the server JOIN on users.color.
    const stored = item?.authorColor || fallback;
    return colorForUsername(annotationAuthor(item), stored);
  }
  function markerColor(m) { return annotationColor(m, ANNOTATION_COLORS.markerDefault); }
  function regionColor(r) { return annotationColor(r, ANNOTATION_COLORS.regionDefault); }

  function isProjectOwner() { return !!(current && me && current.ownerUsername === me.username); }
  function myProjectRole() {
    if (!current || !me) return '';
    if (isProjectOwner()) return 'owner';
    return members.find(m => m.username === me.username)?.role || '';
  }
  function canAnnotateProject() { return ['owner','editor','member'].includes(myProjectRole()); }

  function canEditAnnotation(item) { return !!(item && me && (annotationAuthor(item) === me.username || isProjectOwner())); }
  function annotationEditorCanEdit() { return !!(annotationEditor && me && (annotationEditor.author === me.username || isProjectOwner())); }
  function readOnlyAnnotationMessage() { return 'Read-only: only the author or project owner can edit.'; }
  function projectOwnerMessage() { return 'Only the project owner can do this.'; }
  function projectEditorMessage() { return 'Ask the project owner to add you as an editor before marking up this project.'; }


  function closeColorPicker() { showColorPicker = false; }
  function closeProjectPicker() { showProjectPicker = false; }
  function closeProjectEdit() { showProjectEdit = false; }
  function closeProjectMenu() { showProjectMenu = false; }
  function closeMembersPanel() { showMembersPanel = false; }
  function closeMaintenancePanel() { showMaintenancePanel = false; }
  function closeKeyboardHelp() { showKeyboardHelp = false; }
  function closeIngestPanel() { showIngestPanel = false; }

  function closeAnnotationEditor() {
    annotationEditor = null;
    annotationSaved = '';
  }

  function openProjectFromPicker(id) {
    openProject(id);
    showProjectPicker = false;
  }

  function createProjectFromPicker(name) {
    createProject(name);
    showProjectPicker = false;
  }

  function openProjectEditFromMenu() {
    if (!current) return;
    editingProjectName = current.name;
    editingProjectDesc = current.description || '';
    showProjectEdit = true;
    showProjectMenu = false;
  }

  function openMembersPanelFromMenu() {
    showMembersPanel = !showMembersPanel;
    showMaintenancePanel = false;
    showProjectMenu = false;
    refreshMembers();
  }

  function openMaintenancePanelFromMenu() {
    showMaintenancePanel = !showMaintenancePanel;
    showMembersPanel = false;
    showProjectMenu = false;
  }

  async function runMaintenanceIngest() {
    await ingest();
    showMaintenancePanel = false;
  }

  async function runMaintenanceRefresh() {
    await refreshProject();
    showMaintenancePanel = false;
  }

  function toggleProjectPicker() {
    showProjectPicker = !showProjectPicker;
    showProjectMenu = false;
  }

  function toggleProjectMenu() {
    showProjectMenu = !showProjectMenu;
    showProjectPicker = false;
  }

  function toggleIngestPanel() {
    showIngestPanel = !showIngestPanel;
    if (showIngestPanel) refreshIngestJobs();
  }

  function jogPlayhead(deltaMs) {
    wallclockMs = Math.max(0, wallclockMs + deltaMs);
    seekAll();
  }

  function selectMonitorClip(clip) {
    if (!clip) return;
    selectedClipId = clip.clipId;
    selectedClipIds = [clip.clipId];
    activeInspectorTab = 'clip';
  }

  function registerTimelineNodes(viewport, canvas) {
    timelineViewport = viewport;
    timelineCanvas = canvas;
  }

  function seekToAnnotation(type, item, event) {
    wallclockMs = Number(type === 'marker' ? item.marker_ts_ms : item.region_start_ms);
    seekAll();
    openAnnotationEditor(type, item, event);
  }

  function clearImportProbe() {
    importProbe = null;
    importStreams = [];
    importError = '';
  }

  function timelineBlockApi() {
    return { clip: clipBlockStyle, msToPx };
  }


  function nextZoomLevel(direction) {
    return stepZoomLevel(zoomLevel, direction);
  }

  setAppActions({
    get setMyColor() { return setMyColor; },
    get openProject() { return openProject; },
    get createProject() { return createProject; },
    get saveProjectEdit() { return saveProjectEdit; },
    get refreshMembers() { return refreshMembers; },
    get addProjectMember() { return addProjectMember; },
    get updateProjectMemberRole() { return updateProjectMemberRole; },
    get removeProjectMember() { return removeProjectMember; },
    get ingest() { return ingest; },
    get refreshProject() { return refreshProject; },
    get refreshIngestJobs() { return refreshIngestJobs; },
    get retryClipIngest() { return retryClipIngest; },
    get saveAnnotationEditor() { return saveAnnotationEditor; },
    get deleteMarker() { return deleteMarker; },
    get deleteRegion() { return deleteRegion; },
    get moveClipTo() { return moveClipTo; },
    get moveClip() { return moveClip; },
    get setSoftNudge() { return setSoftNudge; },
    get renameSelectedClip() { return renameSelectedClip; },
    get detachSelectedClips() { return detachSelectedClips; },
    get deleteSelectedClips() { return deleteSelectedClips; },
    get toggleTrack() { return toggleTrack; },
    get setVolume() { return setVolume; },
    get addMarker() { return addMarker; },
    get addRegion() { return addRegion; },
    get seekToAnnotation() { return seekToAnnotation; },
    get canEditAnnotation() { return canEditAnnotation; },
    get isProjectOwner() { return isProjectOwner(); },
    get myProjectRole() { return myProjectRole(); },
    get canAnnotateProject() { return canAnnotateProject(); },
    get annotationEditorCanEdit() { return annotationEditorCanEdit(); },
    get readOnlyAnnotationMessage() { return readOnlyAnnotationMessage(); },
    get closeColorPicker() { return closeColorPicker; },
    get closeProjectPicker() { return closeProjectPicker; },
    get closeProjectEdit() { return closeProjectEdit; },
    get closeProjectMenu() { return closeProjectMenu; },
    get closeMembersPanel() { return closeMembersPanel; },
    get closeMaintenancePanel() { return closeMaintenancePanel; },
    get closeKeyboardHelp() { return closeKeyboardHelp; },
    get closeIngestPanel() { return closeIngestPanel; },
    get closeAnnotationEditor() { return closeAnnotationEditor; },
    get openProjectFromPicker() { return openProjectFromPicker; },
    get createProjectFromPicker() { return createProjectFromPicker; },
    get openProjectEditFromMenu() { return openProjectEditFromMenu; },
    get openMembersPanelFromMenu() { return openMembersPanelFromMenu; },
    get openMaintenancePanelFromMenu() { return openMaintenancePanelFromMenu; },
    get runMaintenanceIngest() { return runMaintenanceIngest; },
    get runMaintenanceRefresh() { return runMaintenanceRefresh; }
  });

</script>

{#if !me}
  <LoginView {loading} {error} {devAuthMode} onlogin={login} />
{:else}
  <main class="app-shell">
    <TopBar
      {me}
      {current}
      {playing}
      {started}
      {wallclockMs}
      {statusCounts}
      {presenceUsers}
      {wsConnected}
      {showIngestPanel}
      projectOwner={isProjectOwner()}
      ontoggleprojectpicker={toggleProjectPicker}
      ontoggleprojectmenu={toggleProjectMenu}
      onjog={jogPlayhead}
      ontoggleplay={togglePlay}
      onstartsession={startSession}
      ontoggleingest={toggleIngestPanel}
      onrefreshjobs={refreshIngestJobs}
      ontogglecolor={() => showColorPicker = !showColorPicker}
      onlogout={logout}
      ontogglehelp={() => showKeyboardHelp = !showKeyboardHelp}
    />

    <Popovers
      {me}
      {current}
      {projects}
      {showColorPicker}
      {showProjectPicker}
      {showProjectEdit}
      {showProjectMenu}
      {showMembersPanel}
      {showMaintenancePanel}
      {showKeyboardHelp}
      {showIngestPanel}
      bind:annotationEditor
      {annotationSaved}
      {ingestJobs}
      {members}
      {memberMessage}
      bind:editingProjectName
      bind:editingProjectDesc
      bind:newProjectName
      bind:newMemberUsername
      bind:newMemberRole
    />

    {#if loadingProject}<div class="loading-overlay"><span class="loading-spinner"></span></div>{/if}

    {#if current}
      <div class="body-cols">
        <SourceBrowser
          {sourcePrefix}
          {sources}
          {importProbe}
          bind:importPerspective
          bind:importStreams
          {importError}
          {wallclockMs}
          {error}
          {browseSources}
          {inspectSource}
          {addSelectedStreams}
          clearImport={clearImportProbe}
          clearError={() => error = ''}
        />

        <section class="workspace">
          <MonitorGrid
            {gridPreset}
            {monitorCells}
            {started}
            playbackEnabled={true}
            registerNode={registerTrackMedia}
            ongridpreset={(preset) => gridPreset = preset}
            onstart={startSession}
            onselect={selectMonitorClip}
          />

          <TimelineView
            {playing}
            {snapEnabled}
            {linkedMoveEnabled}
            {showIngestPanel}
            {pendingJobCount}
            {failedJobCount}
            bind:zoomLevel
            {trackRows}
            {tickMarks}
            {markers}
            {regions}
            {wallclockMs}
            {timelineWidthPx}
            {marqueeState}
            owner={isProjectOwner()}
            {visibleTrackIds}
            {activeAudioIds}
            blockStyle={timelineBlockApi()}
            {markerStyle}
            {regionStyle}
            {marqueeStyle}
            {isClipSelected}
            {isClipDragging}
            {annotationAuthor}
            {registerTimelineNodes}
            ontoggleplay={togglePlay}
            onaddmarker={addMarker}
            onaddregion={addRegion}
            ontogglesnap={() => snapEnabled = !snapEnabled}
            ontogglelink={() => linkedMoveEnabled = !linkedMoveEnabled}
            ontogglejobs={toggleIngestPanel}
            onrefreshjobs={refreshIngestJobs}
            onmoveorder={moveInOrder}
            ontogglecollapse={togglePerspectiveCollapse}
            onrename={startRename}
            ontoggleperspectiveview={togglePerspectiveView}
            ontoggleperspectiveaudio={togglePerspectiveAudio}
            ontoggletrack={toggleTrack}
            onplayheaddrag={startPlayheadDrag}
            onlanepointerdown={startLanePointerDown}
            onclipdrag={startClipDrag}
            onannotationopen={openAnnotationEditor}
            ondeleteclip={deleteClip}
          />
        </section>

        <RenamePanel bind:renameValue {renameTarget} oncommit={commitRename} onclose={() => renameTarget = null} />

        <InspectorPanel
          bind:activeInspectorTab
          {current}
          {selectedClip}
          {selectedClips}
          {audioClips}
          {activeAudioIds}
          {volumes}
          {softNudges}
          {wallclockMs}
          {linkedMoveEnabled}
          {markers}
          {regions}
          {me}
          {members}
          {presenceUsers}
        />
      </div>
    {:else}
      <ProjectSplash {projects} {me} onopen={openProject} oncreate={createProject} />
    {/if}

    <AudioDeck {audioClips} {activeAudioIds} {volumes} registerNode={registerClipMedia} />
  </main>
{/if}
