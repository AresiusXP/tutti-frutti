import { redirect } from '@sveltejs/kit';
import { resolve } from '$app/paths';
import type { PageLoad } from './$types';

/**
 * The playground and its fixtures exist only in `vite dev`: the dynamic import
 * sits inside an `import.meta.env.DEV` branch, which Vite replaces with `false`
 * in a production build, so the scenarios (and the catalog fixture JSON) are
 * tree-shaken out of the bundle. (`redirect()` must come AFTER the branch: the
 * bundler cannot know it never returns.) The ci-web workflow greps the build output to guard this.
 */
export const load: PageLoad = async () => {
	if (import.meta.env.DEV) {
		const { scenarios } = await import('$lib/dev/scenarios');
		return { scenarios };
	}
	redirect(307, resolve('/'));
};
