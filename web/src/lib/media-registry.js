export function createMediaRegistry() {
  const clipNodes = new Map();
  const trackNodes = new Map();

  return {
    registerClip(id, node) {
      if (node) clipNodes.set(id, node);
      else clipNodes.delete(id);
    },
    registerTrack(id, node) {
      if (node) trackNodes.set(id, node);
      else trackNodes.delete(id);
    },
    nodeForClip(clip) {
      return clip?.kind === 'video' ? trackNodes.get(clip.trackId) : clipNodes.get(clip.clipId);
    },
    pauseAll() {
      for (const node of clipNodes.values()) node.pause();
      for (const node of trackNodes.values()) node.pause();
    },
    pauseInactive(audioClips, targetAudioClipIds, targetVideoTrackIds) {
      const targetAudio = new Set(targetAudioClipIds.map(String));
      const targetVideo = new Set(targetVideoTrackIds.map(String));
      for (const clip of audioClips) {
        if (targetAudio.has(String(clip.clipId))) continue;
        const node = clipNodes.get(clip.clipId);
        if (node) {
          node.pause();
          node.muted = true;
        }
      }
      for (const [trackId, node] of trackNodes.entries()) {
        if (!targetVideo.has(String(trackId))) node.pause();
      }
    },
    disableTrack(allClips, trackId) {
      for (const clip of allClips.filter((item) => item.trackId === trackId)) {
        const node = clip.kind === 'video' ? trackNodes.get(clip.trackId) : clipNodes.get(clip.clipId);
        if (node) {
          node.pause();
          if (clip.kind === 'audio') node.muted = true;
        }
      }
    },
    clear() {
      this.pauseAll();
      clipNodes.clear();
      trackNodes.clear();
    }
  };
}
