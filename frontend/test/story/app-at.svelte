<!--
  The real app at one address, over the story core of `api.ts` with the
  modules the toolbar turns on. The core is installed while this component
  initialises, before `App` asks for anything, and a new fleet size or module
  selection draws a new `App` over a new core. The size is `many` unless
  `size` fixes one. The app's route is held
  inside the preview so a reload can reopen the same story.
-->
<script lang="ts">
  import type { RouterNavigation } from '@prusa/connect-ui/router';
  import { onDestroy } from 'svelte';

  import App from '../../src/app.svelte';
  import { type FleetSize, installApi, type Overrides } from './api';
  import { devicePreviewOf } from './device-previews';
  import { installModules, selectedModules } from './modules';

  interface Props {
    path: string;
    size?: FleetSize;
    overrides?: Overrides;
  }

  let { path, size, overrides = {} }: Props = $props();

  const fleet = $derived(size ?? 'many');

  const modules = selectedModules();

  let restore = (): void => undefined;

  // Called from the key below, so the stub is installed before App requests.
  const serve = (fleet: FleetSize, at: string, selection: string): string => {
    restore = installApi(
      fleet,
      installModules(overrides, { selection, modules: modules.modules }),
    );
    return `${fleet} ${at} ${selection}`;
  };

  // Extension details read their query directly from the preview document.
  let appQuery = new URLSearchParams();
  const showQuery = (next: URLSearchParams): void => {
    const preview = new URL(location.href);
    for (const key of appQuery.keys()) preview.searchParams.delete(key);
    appQuery = new URLSearchParams(
      [...next].filter(
        ([key]) => !['id', 'viewMode', 'globals', 'args'].includes(key),
      ),
    );
    for (const [key, value] of appQuery)
      preview.searchParams.append(key, value);
    if (preview.href !== location.href) {
      history.replaceState(history.state, '', preview);
    }
  };

  const previewNavigation = (at: string): RouterNavigation => {
    let address = new URL(at, location.origin);
    showQuery(address.searchParams);
    const move = (_data: unknown, _unused: string, next: string): void => {
      address = new URL(next, address);
      showQuery(address.searchParams);
    };
    return {
      get pathname() {
        return address.pathname;
      },
      get search() {
        return address.search;
      },
      get hash() {
        return address.hash;
      },
      origin: location.origin,
      pushState: move,
      replaceState: move,
      scrollToTop: () => globalThis.scrollTo(0, 0),
      addEventListener: (type, listener) => {
        if (type === 'click') globalThis.addEventListener(type, listener);
      },
      removeEventListener: (type, listener) => {
        if (type === 'click') globalThis.removeEventListener(type, listener);
      },
    };
  };

  onDestroy(() => {
    showQuery(new URLSearchParams());
    restore();
  });
</script>

{#key serve(fleet, path, modules.selection)}
  <App navigation={previewNavigation(path)} deviceThumbnail={devicePreviewOf} />
{/key}
