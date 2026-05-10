<script>
  let { title = '', klass = '', onclose, children, actions } = $props();
</script>

<div class="float-panel {klass}">
  {#if title || actions || onclose}
    <div class="float-head">
      <span>{title}</span>
      {#if actions}
        <div class="float-head-actions">{@render actions()}</div>
      {:else if onclose}
        <button class="topbar-icon-btn" onclick={onclose}>×</button>
      {/if}
    </div>
  {/if}
  {@render children?.()}
</div>
