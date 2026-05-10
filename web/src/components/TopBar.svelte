<script>
  import { format } from '../lib/timeline.js';
  import { PRESENCE, TIMING } from '../lib/constants.js';

  let {
    me,
    current,
    playing,
    started,
    wallclockMs,
    statusCounts,
    presenceUsers,
    wsConnected,
    projectOwner = false,
    showIngestPanel = false,
    ontoggleprojectpicker,
    ontoggleprojectmenu,
    onjog,
    ontoggleplay,
    onstartsession,
    ontoggleingest,
    onrefreshjobs,
    ontogglecolor,
    onlogout,
    ontogglehelp
  } = $props();
</script>

<header class="topbar">
  <div class="topbar-left">
    <button class="topbar-icon-btn topbar-grid-btn" onclick={ontoggleprojectpicker} title="Projects">
      <svg width="14" height="14" viewBox="0 0 14 14"><rect x="1" y="1" width="5" height="5" rx="1" stroke="currentColor" stroke-width="1.3" fill="none"/><rect x="8" y="1" width="5" height="5" rx="1" stroke="currentColor" stroke-width="1.3" fill="none"/><rect x="1" y="8" width="5" height="5" rx="1" stroke="currentColor" stroke-width="1.3" fill="none"/><rect x="8" y="8" width="5" height="5" rx="1" stroke="currentColor" stroke-width="1.3" fill="none"/></svg>
    </button>
    <span class="brand-wordmark">DRIFTER</span>
    <span class="topbar-divider"></span>
    {#if current}
      <span class="project-title-display">{current.name}{#if !projectOwner}<span class="owner-tag">{current.ownerUsername}</span>{/if}</span>
      {#if projectOwner}
        <button class="ribbon-settings-btn" onclick={ontoggleprojectmenu} title="Project menu">
          <svg width="13" height="13" viewBox="0 0 14 14"><circle cx="7" cy="7" r="2" stroke="currentColor" stroke-width="1.3" fill="none"/><path d="M7 1v1.5M7 11.5V13M1 7h1.5M11.5 7H13M2.93 2.93l1.06 1.06M10.01 10.01l1.06 1.06M2.93 11.07l1.06-1.06M10.01 3.99l1.06-1.06" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"/></svg>
        </button>
      {/if}
    {:else}
      <span class="no-project-label">Open a project</span>
    {/if}
  </div>

  <div class="topbar-center">
    <button class="tb-jog" onclick={() => onjog?.(-TIMING.topbarFastJogMs)} disabled={!current} title="−10s (Shift+←)">⏮</button>
    <button class="tb-jog" onclick={() => onjog?.(-TIMING.largePlayheadJogMs)} disabled={!current} title="−1s">◂</button>
    <button class="tb-play {playing ? 'tb-playing' : ''}" onclick={ontoggleplay} disabled={!current} title={playing ? 'Pause' : 'Play'}>
      {#if playing}■{:else}▶{/if}
    </button>
    <button class="tb-jog" onclick={() => onjog?.(TIMING.largePlayheadJogMs)} disabled={!current} title="+1s">▸</button>
    <button class="tb-jog" onclick={() => onjog?.(TIMING.topbarFastJogMs)} disabled={!current} title="+10s">⏭</button>
    <span class="timecode-display">{format(wallclockMs)}</span>
    {#if started}
      <button class="audio-live-btn" onclick={onstartsession} title="Audio unlocked — click to re-sync">♪ LIVE</button>
    {/if}
  </div>

  <div class="topbar-right">
    {#if current && (statusCounts.processing + statusCounts.queued + statusCounts.failed > 0)}
      <button class="status-chip {statusCounts.failed > 0 ? 'chip-warn' : 'chip-info'}" onclick={() => { ontoggleingest?.(); if (!showIngestPanel) onrefreshjobs?.(); }}>
        {#if statusCounts.processing > 0}<span class="pulse-dot"></span>{/if}
        {statusCounts.queued + statusCounts.processing} preparing
        {#if statusCounts.failed > 0} · <strong>{statusCounts.failed} failed</strong>{/if}
      </button>
    {:else if current && statusCounts.total > 0}
      <span class="status-chip chip-ok">{statusCounts.ready}/{statusCounts.total}</span>
    {/if}

    <span class="ws-indicator {wsConnected ? 'ws-live' : 'ws-dead'}" title={wsConnected ? 'Connected' : 'Disconnected'}></span>

    <div class="presence-group">
      {#each presenceUsers.filter((user) => user.username !== me?.username).slice(0, PRESENCE.maxVisibleUsers) as user}
        <span class="presence-dot" style="background:{user.color}" title={user.username}>{user.username[0]?.toUpperCase()}</span>
      {/each}
    </div>

    <button class="user-chip-btn" onclick={ontogglecolor} title="Change accent color">
      <span class="user-color-dot" style="background:{me.color}"></span>
      <span class="user-chip-name">{me.displayName || me.username}</span>
    </button>
    <button class="topbar-icon-btn" onclick={onlogout} title="Sign out">⏻</button>
    <button class="topbar-icon-btn" onclick={ontogglehelp} title="Keyboard shortcuts (?)">?</button>
  </div>
</header>
