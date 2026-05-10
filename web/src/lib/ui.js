import { ZOOM } from './constants.js';

export function stepZoomLevel(currentZoom, direction) {
  const delta = direction * ZOOM.step;
  const unclamped = currentZoom + delta;
  const clamped = Math.min(ZOOM.max, Math.max(ZOOM.min, unclamped));
  return Number(clamped.toFixed(ZOOM.precisionDigits));
}
