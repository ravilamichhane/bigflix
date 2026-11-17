export type NavigateFn = (to: string) => void;

export interface VjsButton {
  controlText: (text: string) => void;
  el: () => HTMLElement;
  on: (event: string, fn: () => void) => void;
  enable: () => void;
  disable: () => void;
}

export interface VjsRemoteTrack {
  track: TextTrack;
  addEventListener: (event: string, fn: () => void) => void;
}

export interface VjsPlayer {
  el: () => HTMLElement;
  play: () => Promise<void> | undefined;
  pause: () => void;
  paused: () => boolean;
  error: (val?: null) => MediaError | null;
  duration: () => number;
  currentTime: (val?: number) => number;
  volume: (val?: number) => number;
  muted: (val?: boolean) => boolean;
  isFullscreen: () => boolean;
  exitFullscreen: () => Promise<void> | undefined;
  requestFullscreen: () => Promise<void> | undefined;
  src: (source: { src: string; type: string }) => void;
  on: (event: string, fn: () => void) => void;
  dispose: () => void;
  bigPlayButton: VjsButton;
  controlBar: {
    playToggle: VjsButton;
    addChild: (name: string, options: Record<string, unknown>) => VjsButton;
  };
  addRemoteTextTrack: (
    options: {
      kind: string;
      src: string;
      label: string;
      srclang: string;
      default?: boolean;
    },
    manualCleanup?: boolean,
  ) => VjsRemoteTrack;
  removeRemoteTextTrack: (track: TextTrack) => void;
}

declare global {
  function videojs(id: string | HTMLElement, options: Record<string, unknown>): VjsPlayer;
  interface Window {
    ManagedMediaSource?: typeof MediaSource;
  }
}
