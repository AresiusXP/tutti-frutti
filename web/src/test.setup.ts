/**
 * Vitest setup: the one-time tip cards ("tf.tips", see lib/help/tips.svelte.ts) are marked as seen, so
 * they stay out of the way of the tests of the screens they sit on. The tests of the tips themselves
 * bring them back with `tips.reset()`.
 */
try {
	if (typeof localStorage !== 'undefined')
		localStorage.setItem('tf.tips', JSON.stringify(['playing', 'review']));
} catch {
	// no storage in this environment: the tips are shown, and no test looks for them
}
