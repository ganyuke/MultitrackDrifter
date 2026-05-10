<script>
  let { renameTarget, renameValue = $bindable(), oncommit, onclose } = $props();
</script>

{#if renameTarget}
  <div class="float-panel rename-float">
    <div class="float-head"><span>Rename {renameTarget.type}</span><button class="topbar-icon-btn" onclick={onclose}>×</button></div>
    <input bind:value={renameValue} onkeydown={(event) => { if (event.key === 'Enter') oncommit?.(); if (event.key === 'Escape') onclose?.(); }} />
    <div class="float-footer"><button class="btn-theme" onclick={oncommit}>Rename</button></div>
  </div>
{/if}
