<script>
  import FloatPanel from './FloatPanel.svelte';
  import { getAppActions } from '../lib/app-actions.js';
  import { jobClipLabel, jobProgress, jobProgressPct, jobStage, jobStats, format } from '../lib/timeline.js';
  import { ACCENT_COLORS, JOBS } from '../lib/constants.js';

  const app = getAppActions();

  let {
    me,
    current,
    projects,
    showColorPicker,
    showProjectPicker,
    showProjectEdit,
    showProjectMenu,
    showMembersPanel,
    showMaintenancePanel,
    showKeyboardHelp,
    showIngestPanel,
    annotationEditor = $bindable(),
    annotationSaved,
    ingestJobs,
    members,
    memberMessage,
    editingProjectName = $bindable(),
    editingProjectDesc = $bindable(),
    newProjectName = $bindable(),
    newMemberUsername = $bindable(),
    newMemberRole = $bindable()
  } = $props();
</script>

{#if showColorPicker}
  <FloatPanel title="Accent color" klass="color-float" onclose={app.closeColorPicker}>
    <div class="color-swatches">
      {#each ACCENT_COLORS as color}
        <button class="color-swatch {me.color === color ? 'swatch-active' : ''}" style="background:{color}" onclick={() => app.setMyColor?.(color)}></button>
      {/each}
    </div>
  </FloatPanel>
{/if}

{#if showProjectPicker}
  <FloatPanel title="Projects" klass="project-float" onclose={app.closeProjectPicker}>
    <div class="project-scroll">
      {#each projects as project}
        <button class="project-item {current?.id === project.id ? 'proj-active' : ''}" onclick={() => app.openProjectFromPicker?.(project.id)}>
          <span class="proj-name">{project.name}</span>
          <span class="proj-owner">{project.ownerUsername}</span>
        </button>
      {:else}
        <p class="muted padded">No projects yet.</p>
      {/each}
    </div>
    <div class="float-footer">
      <input bind:value={newProjectName} placeholder="New project name" onkeydown={(event) => { if (event.key === 'Enter') app.createProjectFromPicker?.(newProjectName); }} />
      <button class="btn-theme" onclick={() => app.createProjectFromPicker?.(newProjectName)}>Create</button>
    </div>
  </FloatPanel>
{/if}

{#if showProjectEdit}
  <FloatPanel title="Edit project" klass="project-edit-float" onclose={app.closeProjectEdit}>
    <label>Name<input bind:value={editingProjectName} /></label>
    <label>Description<textarea bind:value={editingProjectDesc} rows="2"></textarea></label>
    <div class="float-footer">
      <button class="btn-theme" onclick={app.saveProjectEdit}>Save</button>
      <button class="btn-ghost" onclick={app.closeProjectEdit}>Cancel</button>
    </div>
  </FloatPanel>
{/if}

{#if showProjectMenu && current}
  <FloatPanel title={current.name} klass="proj-menu-float" onclose={app.closeProjectMenu}>
    <button class="proj-menu-item" onclick={app.openProjectEditFromMenu}>
      <svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" aria-hidden="true" role="img" width="16" height="16" viewBox="0 0 24 24"><g fill="currentColor"><path fill-rule="evenodd" d="M10 4H8v2H5a3 3 0 0 0-3 3v6a3 3 0 0 0 3 3h3v2h2zM8 8v8H5a1 1 0 0 1-1-1V9a1 1 0 0 1 1-1z" clip-rule="evenodd"></path><path d="M19 16h-7v2h7a3 3 0 0 0 3-3V9a3 3 0 0 0-3-3h-7v2h7a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1"></path></g></svg>
      Rename / Edit
    </button>
    <button class="proj-menu-item" onclick={app.openMembersPanelFromMenu}>
      <svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" aria-hidden="true" role="img" width="16" height="16" viewBox="0 0 24 24"><path fill="none" stroke="currentColor" stroke-linecap="square" stroke-width="2" d="M16 20v-1a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v1M12.5 7a4 4 0 1 1-8 0a4 4 0 0 1 8 0Zm3 4a4 4 0 0 0 0-8M23 20v-1a4 4 0 0 0-4-4"></path></svg>
      Members
    </button>
    <button class="proj-menu-item" onclick={app.openMaintenancePanelFromMenu}>
      <svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" aria-hidden="true" role="img" width="16" height="16" viewBox="0 0 24 24"><path fill="currentColor" d="M6.12 20.75c-.76 0-1.48-.3-2.03-.84a2.86 2.86 0 0 1 0-4.05l5.51-5.51c-.5-1.94.04-4.03 1.46-5.45a5.67 5.67 0 0 1 5.48-1.46c.26.07.46.27.53.53s0 .53-.19.72l-2.45 2.45l.52 1.91l1.91.52l2.45-2.45c.19-.19.47-.26.72-.19c.26.07.46.27.53.53c.53 1.95-.02 4.05-1.46 5.48c-1.42 1.42-3.51 1.96-5.45 1.46l-5.51 5.51c-.54.54-1.26.84-2.02.84m8.56-15.98c-.96.08-1.87.5-2.57 1.2c-1.14 1.14-1.51 2.81-.96 4.35c.1.27.03.58-.18.78l-5.83 5.83c-.53.53-.53 1.4 0 1.93c.26.26.6.4.97.4c.36 0 .71-.14.96-.4l5.83-5.83c.21-.21.51-.27.78-.18c1.54.54 3.21.18 4.35-.96c.7-.7 1.11-1.61 1.2-2.57l-1.63 1.63c-.19.19-.47.26-.73.19l-2.74-.75a.75.75 0 0 1-.53-.53l-.75-2.74c-.07-.26 0-.54.19-.73l1.63-1.63Z"></path></svg>
      Maintenance
    </button>
  </FloatPanel>
{/if}

{#if showMembersPanel && current}
  <FloatPanel title={`Members — ${current.name}`} klass="members-float" onclose={app.closeMembersPanel}>
    <div class="members-float-body">
      {#if app.isProjectOwner}
        <div class="member-add-row">
          <input bind:value={newMemberUsername} placeholder="LDAP username" onkeydown={(event) => { if (event.key === 'Enter') app.addProjectMember?.(); }} />
          <select bind:value={newMemberRole}>
            <option value="editor">Editor</option>
            <option value="viewer">Viewer</option>
          </select>
          <button class="btn-sm btn-theme" onclick={app.addProjectMember}>Add</button>
        </div>
        <p class="member-hint muted">Editors can add markers and regions. Viewers can only watch and export. Users must sign in once before they can be added.</p>
        {#if memberMessage}<p class="member-message">{memberMessage}</p>{/if}
      {:else}
        <p class="member-hint muted">Your role: <strong>{app.myProjectRole || 'viewer'}</strong>.</p>
      {/if}
      <div class="member-list">
        {#each members as member}
          <div class="member-row">
            <span class="member-avatar" style="background:{member.color}">{member.username[0]?.toUpperCase()}</span>
            <div class="member-main">
              <span class="member-name">{member.displayName || member.username}</span>
              <span class="member-user muted">{member.username}</span>
            </div>
            {#if app.isProjectOwner && member.role !== 'owner'}
              <select class="member-role-select" value={member.role} onchange={(event) => app.updateProjectMemberRole?.(member, event.currentTarget.value)}>
                <option value="editor">Editor</option>
                <option value="viewer">Viewer</option>
              </select>
              <button class="topbar-icon-btn" title="Remove" onclick={() => app.removeProjectMember?.(member)}>×</button>
            {:else}
              <span class="role-pill {member.role}">{member.role}</span>
            {/if}
          </div>
        {:else}
          <p class="muted padded">No members yet.</p>
        {/each}
      </div>
    </div>
  </FloatPanel>
{/if}

{#if showMaintenancePanel && current}
  <FloatPanel title={`Maintenance — ${current.name}`} klass="maintenance-float" onclose={app.closeMaintenancePanel}>
    <div class="ins-section">
      <p class="ins-section-label">Ingest</p>
      <button class="action-btn" onclick={app.runMaintenanceIngest}>Retry all pending / failed</button>
    </div>
    <div class="ins-section">
      <p class="ins-section-label">Data</p>
      <button class="action-btn" onclick={app.runMaintenanceRefresh}>Refresh project</button>
    </div>
  </FloatPanel>
{/if}

{#if showKeyboardHelp}
  <FloatPanel title="Keyboard shortcuts" klass="kbd-float" onclose={app.closeKeyboardHelp}>
    <table class="kbd-table">
      <tbody>
        <tr><td><kbd>⌘Z</kbd> / <kbd>Ctrl+Z</kbd></td><td>Undo clip move</td></tr>
        <tr><td><kbd>⌘⇧Z</kbd> / <kbd>Ctrl+Y</kbd></td><td>Redo</td></tr>
        <tr><td><kbd>+</kbd> / <kbd>−</kbd></td><td>Zoom timeline in/out</td></tr>
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
  </FloatPanel>
{/if}

{#if showIngestPanel && current}
  <FloatPanel title="Ingest jobs" klass="ingest-float">
    {#snippet actions()}
      <button class="btn-sm" onclick={app.ingest}>Retry all</button>
      <button class="topbar-icon-btn" onclick={app.refreshIngestJobs} title="Refresh">⟳</button>
      <button class="topbar-icon-btn" onclick={app.closeIngestPanel}>×</button>
    {/snippet}
    <div class="job-scroll">
      {#each ingestJobs.slice(0, JOBS.maxVisibleRows) as job}
        <div class="job-row">
          <div class="job-main">
            <div class="job-line">
              <span class="job-badge {job.state === 'SUCCESS' ? 'jb-ok' : job.state === 'FAILED' ? 'jb-fail' : job.state === 'PROCESSING' ? 'jb-run' : 'jb-pend'}">{job.state}</span>
              <span class="job-id">#{job.id}</span>
              <span class="job-clip muted" title={jobClipLabel(job)}>{jobClipLabel(job)}</span>
              {#if job.error}<span class="job-err" title={job.error}>⚠</span>{/if}
              {#if job.state === 'FAILED'}
                <button class="btn-sm btn-warn-sm" onclick={() => app.retryClipIngest?.(job.clip_id)}>Retry</button>
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
  </FloatPanel>
{/if}

{#if annotationEditor}
  <div class="float-panel ann-float" style="--ann:{annotationEditor.color}">
    <div class="float-head">
      <span class="ann-type-tag">{annotationEditor.type === 'marker' ? '◆ Marker' : '▬ Region'}</span>
      <button class="topbar-icon-btn" onclick={app.closeAnnotationEditor}>×</button>
    </div>
    <p class="ann-meta">by <strong>{annotationEditor.author}</strong></p>
    <label>Label<input bind:value={annotationEditor.label} disabled={!app.annotationEditorCanEdit} /></label>
    {#if annotationEditor.type === 'marker'}
      <label>Time <span class="label-hint">{format(annotationEditor.tsMs)}</span>
        <input type="number" bind:value={annotationEditor.tsMs} disabled={!app.annotationEditorCanEdit} />
      </label>
    {:else}
      <div class="two-col-inputs">
        <label>Start <span class="label-hint">{format(annotationEditor.startMs)}</span>
          <input type="number" bind:value={annotationEditor.startMs} disabled={!app.annotationEditorCanEdit} />
        </label>
        <label>End <span class="label-hint">{format(annotationEditor.endMs)}</span>
          <input type="number" bind:value={annotationEditor.endMs} disabled={!app.annotationEditorCanEdit} />
        </label>
      </div>
    {/if}
    <label>Note<textarea bind:value={annotationEditor.note} rows="5" placeholder="Notes…" disabled={!app.annotationEditorCanEdit}></textarea></label>
    <div class="float-footer ann-footer">
      <span class="save-msg">{app.annotationEditorCanEdit ? annotationSaved : app.readOnlyAnnotationMessage}</span>
      <div class="ann-footer-actions">
        {#if app.annotationEditorCanEdit}
          <button class="btn-danger-sm" onclick={() => { if (annotationEditor.type === 'marker') app.deleteMarker?.(annotationEditor.id); else app.deleteRegion?.(annotationEditor.id); }} title="Delete">Delete</button>
        {/if}
        <button class="btn-theme" onclick={app.saveAnnotationEditor} disabled={!app.annotationEditorCanEdit}>Save</button>
      </div>
    </div>
  </div>
{/if}
