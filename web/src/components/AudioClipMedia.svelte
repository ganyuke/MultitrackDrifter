<script>
  import { attachHLS } from '../playback.js';
  import { AUDIO } from '../lib/constants.js';

  let { clip, active = false, volume = AUDIO.defaultVolume, registerNode } = $props();
  let audio = $state(null);
  let attachedNode = null;
  let attachedUrl = '';
  let detachHls = () => {};

  function resetMediaElement(node) {
    if (!node) return;
    try { node.pause(); } catch (_) {}
    try { node.removeAttribute('src'); node.load(); } catch (_) {}
  }

  function detachCurrent() {
    detachHls();
    resetMediaElement(attachedNode);
    detachHls = () => {};
    attachedNode = null;
    attachedUrl = '';
  }

  function attachCurrent(node, url) {
    if (attachedNode === node && attachedUrl === url) return;
    detachCurrent();
    if (!node || !url) return;
    detachHls = attachHLS(node, url);
    attachedNode = node;
    attachedUrl = url;
  }

  $effect(() => {
    const node = audio;
    const clipId = clip.clipId;
    if (!node || !registerNode) return;
    registerNode(clipId, node);
    return () => registerNode(clipId, null);
  });

  $effect(() => {
    if (!audio) return;
    audio.muted = !active;
    audio.volume = Number(volume ?? AUDIO.defaultVolume);
  });

  $effect(() => {
    attachCurrent(audio, clip.hlsURL || '');
  });

  $effect(() => detachCurrent);
</script>

<audio preload="auto" bind:this={audio}></audio>
