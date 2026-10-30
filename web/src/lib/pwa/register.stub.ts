// Stands in for the `virtual:pwa-register` module of the PWA plugin, which only exists in a real
// build (vite.config.ts `test.alias`). Tests that care mock it with `vi.mock('virtual:pwa-register')`.
export function registerSW(): (reloadPage?: boolean) => Promise<void> {
	return () => Promise.resolve();
}
