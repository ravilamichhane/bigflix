export type NavigateFn = (to: string) => void;

export interface WebkitVideoElement extends HTMLVideoElement {
  webkitEnterFullscreen?: () => void;
  webkitExitFullscreen?: () => void;
  webkitDisplayingFullscreen?: boolean;
}

declare global {
  interface Window {
    ManagedMediaSource?: typeof MediaSource;
  }
}
