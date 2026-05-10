<script>
  let { projects, me, onopen, oncreate } = $props();
  let newProjectName = $state('Local review');
</script>

<div class="project-splash">
  <div class="splash-inner">
    <div class="splash-brand">
      <svg width="40" height="40" viewBox="0 0 32 32" fill="none"><circle cx="16" cy="16" r="15" stroke="#5d94ff" stroke-width="1.5"/><path d="M9 22V10l7 4.5 7-4.5v12l-7-4.5L9 22z" fill="#5d94ff"/></svg>
      <span class="splash-wordmark">DRIFTER</span>
    </div>
    <p class="splash-sub">Multi-camera review</p>
    <div class="splash-projects">
      <p class="splash-section-label">Recent projects</p>
      {#each projects as project}
        <button class="splash-project-row" onclick={() => onopen?.(project.id)}>
          <span class="splash-proj-name">{project.name}</span>
          <span class="splash-proj-owner muted">{project.ownerUsername}</span>
        </button>
      {:else}
        <p class="muted padded">No projects yet.</p>
      {/each}
    </div>
    {#if me?.canCreateProjects}
      <div class="splash-create">
        <input bind:value={newProjectName} placeholder="New project name" onkeydown={(event) => { if (event.key === 'Enter') oncreate?.(newProjectName); }} />
        <button class="btn-primary" onclick={() => oncreate?.(newProjectName)}>Create project</button>
      </div>
    {/if}
  </div>
</div>
