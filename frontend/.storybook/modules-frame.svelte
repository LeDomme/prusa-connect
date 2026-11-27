<!--
  Hands the modules toolbar's selection to the app stories (`AppAt`). Nothing
  is wrapped around the story.
-->
<script lang="ts">
  import type { Snippet } from 'svelte';

  import { provideModules, type StoryModule } from '../test/story/modules';

  interface Props {
    selection: string;
    modules: readonly StoryModule[];
    children?: Snippet;
  }

  const { selection, modules, children }: Props = $props();

  provideModules({
    get selection() {
      return selection;
    },
    get modules() {
      return modules;
    },
  });
</script>

{@render children?.()}
