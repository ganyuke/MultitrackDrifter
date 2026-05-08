import Hls from 'hls.js';

export function attachHLS(media, url) {
  if (!media || !url) return () => {};
  if (media.canPlayType('application/vnd.apple.mpegurl')) {
    media.src = url;
    return () => { media.removeAttribute('src'); media.load(); };
  }
  if (Hls.isSupported()) {
    // hls.js ESM builds can require an explicit workerPath under bundlers; disabled for the POC to avoid asset-path surprises.
    const hls = new Hls({ lowLatencyMode: false, enableWorker: false });
    hls.on(Hls.Events.ERROR, (_event, data) => {
      if (!data?.fatal) return;
      if (data.type === Hls.ErrorTypes.MEDIA_ERROR) {
        hls.recoverMediaError();
        return;
      }
      hls.destroy();
    });
    hls.loadSource(url);
    hls.attachMedia(media);
    return () => hls.destroy();
  }
  return () => {};
}

export function loadPrefs(projectId) {
  try { return JSON.parse(localStorage.getItem(`drifter:prefs:${projectId}`)) || {}; } catch (_) { return {}; }
}

export function savePrefs(projectId, prefs) {
  localStorage.setItem(`drifter:prefs:${projectId}`, JSON.stringify(prefs));
}

export function clipLocalSeconds(wallclockMs, clip, softNudgeMs = 0) {
  return (wallclockMs - clip.wallclockStartMs + softNudgeMs) / 1000;
}

export function fitVideoToCell(node) {
  let ro;

  function fit() {
    const parent = node.parentElement;
    if (!parent) return;
    const width = parent.clientWidth;
    const height = parent.clientHeight;
    const videoWidth = node.videoWidth || 16;
    const videoHeight = node.videoHeight || 9;
    if (!width || !height || !videoWidth || !videoHeight) return;

    const parentAspect = width / height;
    const videoAspect = videoWidth / videoHeight;
    if (parentAspect > videoAspect) {
      node.style.height = `${height}px`;
      node.style.width = `${Math.floor(height * videoAspect)}px`;
    } else {
      node.style.width = `${width}px`;
      node.style.height = `${Math.floor(width / videoAspect)}px`;
    }
  }

  node.addEventListener('loadedmetadata', fit);
  window.addEventListener('resize', fit);
  if ('ResizeObserver' in window && node.parentElement) {
    ro = new ResizeObserver(fit);
    ro.observe(node.parentElement);
  }
  queueMicrotask(fit);

  return {
    update: fit,
    destroy: () => {
      node.removeEventListener('loadedmetadata', fit);
      window.removeEventListener('resize', fit);
      ro?.disconnect();
    }
  };
}
