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
      <svg width="11" height="11" viewBox="0 0 12 12"><path d="M8.5 1.5l2 2L3 11H1v-2L8.5 1.5z" stroke="currentColor" stroke-width="1.2" fill="none" stroke-linejoin="round"/></svg>
      Rename / Edit
    </button>
    <button class="proj-menu-item" onclick={app.openMembersPanelFromMenu}>
      <svg width="11" height="11" viewBox="0 0 12 12"><circle cx="4.5" cy="3.5" r="2" stroke="currentColor" stroke-width="1.2" fill="none"/><path d="M1 10c0-2 1.6-3 3.5-3s3.5 1 3.5 3" stroke="currentColor" stroke-width="1.2" fill="none" stroke-linecap="round"/><path d="M8 5.5c1.1.3 2 1.2 2 2.5" stroke="currentColor" stroke-width="1.2" fill="none" stroke-linecap="round"/><circle cx="9" cy="3" r="1.2" stroke="currentColor" stroke-width="1.1" fill="none"/></svg>
      Members
    </button>
    <button class="proj-menu-item" onclick={app.openMaintenancePanelFromMenu}>
      <svg width="11" height="11" viewBox="0 0 12 12"><path d="M9.5 2.5l-1 1-2-2 1-1a2 2 0 012.5 0l-.5.5v1.5zM2 10l5-5" stroke="currentColor" stroke-width="1.2" fill="none" stroke-linecap="round"/><circle cx="2.5" cy="9.5" r="1.5" stroke="currentColor" stroke-width="1.2" fill="none"/></svg>
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
              <select class="member-role-select" value={member.role === 'member' ? 'editor' : member.role} onchange={(event) => app.updateProjectMemberRole?.(member, event.currentTarget.value)}>
                <option value="editor">Editor</option>
                <option value="viewer">Viewer</option>
              </select>
              <button class="topbar-icon-btn" title="Remove" onclick={() => app.removeProjectMember?.(member)}>×</button>
            {:else}
              <span class="role-pill {member.role}">{member.role === 'member' ? 'editor' : member.role}</span>
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
