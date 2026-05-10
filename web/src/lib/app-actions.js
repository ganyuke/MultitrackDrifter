import { getContext, setContext } from 'svelte';

const APP_ACTIONS = Symbol('app-actions');

export function setAppActions(actions) {
  setContext(APP_ACTIONS, actions);
  return actions;
}

export function getAppActions() {
  const actions = getContext(APP_ACTIONS);
  if (!actions) throw new Error('App actions context is unavailable. Render this component under App.svelte.');
  return actions;
}
