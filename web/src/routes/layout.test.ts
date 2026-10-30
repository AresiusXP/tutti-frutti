// @vitest-environment jsdom
import { render } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { isAndroidApp } from '$lib/android/context';
import Layout from './+layout.svelte';

vi.mock('$lib/components/OfflineBanner.svelte', async () => ({
	default: (await import('./LayoutStub.svelte')).default
}));
vi.mock('$lib/components/Toasts.svelte', async () => ({
	default: (await import('./LayoutStub.svelte')).default
}));
vi.mock('$lib/components/UpdatePrompt.svelte', async () => ({
	default: (await import('./LayoutStub.svelte')).default
}));
vi.mock('$lib/safety/TermsGate.svelte', async () => ({
	default: (await import('./LayoutStub.svelte')).default
}));

const children = createRawSnippet(() => ({ render: () => '<main>page</main>' }));

describe('the root layout starts the Android context once, before any screen', () => {
	beforeEach(() => sessionStorage.clear());
	afterEach(() => {
		sessionStorage.clear();
		vi.restoreAllMocks();
	});

	it('a launch from the Android app marks this tab', () => {
		vi.spyOn(document, 'referrer', 'get').mockReturnValue('android-app://xyz.aresius.tuttifrutti');
		render(Layout, { children });
		expect(sessionStorage.getItem('tf.android')).toBe('1');
		expect(isAndroidApp()).toBe(true);
	});

	it('an ordinary visit marks nothing', () => {
		vi.spyOn(document, 'referrer', 'get').mockReturnValue('https://www.google.com/');
		render(Layout, { children });
		expect(sessionStorage.getItem('tf.android')).toBeNull();
		expect(isAndroidApp()).toBe(false);
	});

	it('the way back from another page of the site (a different referrer) keeps the marker', () => {
		sessionStorage.setItem('tf.android', '1');
		vi.spyOn(document, 'referrer', 'get').mockReturnValue(
			'https://tuttifrutti.aresius.xyz/privacy'
		);
		render(Layout, { children });
		expect(isAndroidApp()).toBe(true);
	});
});
