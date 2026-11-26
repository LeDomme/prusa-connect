// The modules this Storybook serves, each with its `ui/` directory relative
// to the repository root. The design CLI reads the list too: an app story
// draws every module served, so their sources are part of its fingerprint.
export const SERVED_MODULES = [
  { at: 'fleet-stats', ui: 'extensions/fleet-stats/ui', fromSource: true },
] as const;
