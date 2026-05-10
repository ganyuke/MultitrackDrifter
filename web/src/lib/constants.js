// Shared UI constants for the Svelte frontend. Keeping domain values named here
// avoids scattering timing/layout assumptions through components.
export const DEFAULT_PROJECT_NAME = 'Local review';
export const DEFAULT_GRID_PRESET = '2x2';
export const GRID_PRESET_CELLS = {
  '1x1': 1,
  '1x2': 2,
  '2x2': 4,
  '3x2': 6
};
export const DEFAULT_MEMBER_ROLE = 'editor';

// Must match internal/auth.Palette; the backend rejects colors outside the accessible palette.
export const ACCENT_COLORS = ['#0072B2', '#D55E00', '#009E73', '#CC79A7', '#F0E442', '#56B4E9', '#E69F00', '#8F00FF'];

export const MS_PER_SECOND = 1000;
export const SECONDS_PER_MINUTE = 60;
export const PERCENT_SCALE = 100;

export const TIMING = {
  playbackSyncIntervalMs: 250,
  ingestRefreshIntervalMs: 5000,
  defaultErrorTimeoutMs: 5000,
  colorErrorTimeoutMs: 4000,
  ingestRefreshDelayMs: 1200,
  defaultRegionDurationMs: 5000,
  smallPlayheadJogMs: 100,
  largePlayheadJogMs: 1000,
  topbarFastJogMs: 10000,
  timelineSnapStepMs: 25,
  softNudgeStepMs: 50,
  mediaSeekToleranceSeconds: 0.45,
  minimumClipDurationMs: MS_PER_SECOND,
  timelineMinimumDurationMs: SECONDS_PER_MINUTE * MS_PER_SECOND,
  timelinePaddingMs: 10 * MS_PER_SECOND,
  longTickThresholdMs: 30 * SECONDS_PER_MINUTE * MS_PER_SECOND,
  mediumTickThresholdMs: 8 * SECONDS_PER_MINUTE * MS_PER_SECOND,
  longTickStepMs: SECONDS_PER_MINUTE * MS_PER_SECOND,
  mediumTickStepMs: 30 * MS_PER_SECOND,
  shortTickStepMs: 10 * MS_PER_SECOND
};

export const TIMELINE_LAYOUT = {
  minLaneWidthPx: 900,
  msPerBasePixel: 45,
  minClipWidthPx: 36,
  minRegionWidthPx: 10,
  snapThresholdPx: 10,
  minimumSnapThresholdMs: 40,
  unknownTrackSortIndex: Number.MAX_SAFE_INTEGER
};

export const ZOOM = {
  default: 1,
  min: 0.25,
  max: 4,
  step: 0.25,
  precisionDigits: 2,
  percentScale: 100
};

export const AUDIO = {
  defaultVolume: 0.85,
  minVolume: 0,
  maxVolume: 1,
  volumeStep: 0.05,
  percentScale: 100
};

export const JOBS = {
  maxVisibleRows: 60
};

export const PRESENCE = {
  maxVisibleUsers: 6
};

export const HISTORY = {
  maxUndoEntries: 50
};

export const HTTP_STATUS = {
  notFound: 404
};

export const TIME_FORMAT = {
  isoTimeStart: 11,
  isoTimeEnd: 23
};

export const WAVEFORM = {
  barCount: 16,
  minHeight: 25,
  seedOffset: 3,
  seedMultiplier: 17,
  heightRange: 60
};

export const ANNOTATION_COLORS = {
  markerDefault: '#f6c85f',
  regionDefault: '#8f70ff',
  regionFillAlpha: '33',
  regionBorderAlpha: 'aa'
};
