<script>
  let { loading = false, error = '', devAuthMode = false, onlogin } = $props();
  let username = $state('');
  let password = $state('');

  function submit() {
    onlogin?.(username, password);
  }
</script>

<main class="login-shell">
  <section class="login-card">
    <div class="login-brand">
      <svg width="32" height="32" viewBox="0 0 32 32" fill="none"><circle cx="16" cy="16" r="15" stroke="#5d94ff" stroke-width="1.5"/><path d="M9 22V10l7 4.5 7-4.5v12l-7-4.5L9 22z" fill="#5d94ff"/></svg>
      <span>DRIFTER</span>
    </div>
    <p class="login-sub">Self-hosted multicam review</p>
    <label>Username<input bind:value={username} autocomplete="username" /></label>
    <label>Password<input bind:value={password} type="password" autocomplete="current-password" /></label>
    <button class="btn-primary full" onclick={submit} disabled={loading}>{loading ? 'Signing in…' : 'Sign in'}</button>
    {#if error}<p class="error">{error}</p>{/if}
    {#if devAuthMode}<p class="warn-hint">⚠ DEV_AUTH_ENABLED — any credentials accepted. Not for production.</p>{/if}
  </section>
</main>
