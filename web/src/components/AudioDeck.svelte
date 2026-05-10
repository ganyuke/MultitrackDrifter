<script>
  import AudioClipMedia from './AudioClipMedia.svelte';
  import { hasId } from '../lib/timeline.js';
  import { AUDIO } from '../lib/constants.js';

  let { audioClips, activeAudioIds, volumes, registerNode } = $props();
</script>

<div class="audio-deck" aria-hidden="true">
  {#each audioClips as clip (clip.clipId)}
    {#if clip.hlsURL}
      <AudioClipMedia {clip} active={hasId(activeAudioIds, clip.trackId)} volume={volumes[clip.clipId] ?? AUDIO.defaultVolume} {registerNode} />
    {/if}
  {/each}
</div>
