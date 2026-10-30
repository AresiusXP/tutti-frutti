// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { fieldProblems, labelNameMismatches } from '$lib/a11y.testkit';
import { i18n } from '$lib/i18n.svelte';
import { apiError, FakeSafetyApi, TERMS_DUE, TERMS_OK } from './safety.testkit';
import TermsGate from './TermsGate.svelte';
import { TermsStore } from './terms.svelte';

function setup() {
	const api = new FakeSafetyApi();
	const store = new TermsStore({
		api,
		loadSession: () => Promise.resolve({ terms: { ...TERMS_DUE, requiredVersion: '2026-11' } })
	});
	store.apply(TERMS_DUE);
	render(TermsGate, { store });
	return { api, store };
}

beforeEach(() => i18n.set('en'));
afterEach(() => cleanup());

describe('TermsGate', () => {
	it('is not on screen until an intent needs the acceptance', () => {
		setup();
		expect(screen.queryByRole('dialog')).toBeNull();
	});

	it('shows the summary, the link to the full terms, the version, the required box and both buttons', async () => {
		const { store } = setup();
		void store.ensure();
		const dialog = await screen.findByRole('dialog', { name: 'Before you play' });
		expect(dialog.getAttribute('aria-modal')).toBe('true');
		expect(dialog.textContent).toContain('terms of use and the community rules');
		expect(dialog.textContent).toContain('Version 2026-10');
		const link = screen.getByRole('link', { name: 'Read the full terms' });
		expect(link.getAttribute('href')).toBe('/terms#en');
		// a real page load in this browsing context: the Android app comes back with Back
		expect(link.getAttribute('rel')).toBe('external');
		expect(link.getAttribute('target')).toBeNull();
		const box = screen.getByRole('checkbox', { name: 'I am 13 or older and accept the Terms' });
		expect((box as HTMLInputElement).required).toBe(true);
		expect(document.activeElement).toBe(box);
		expect(screen.getByRole('button', { name: 'Accept and continue' })).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Not now' })).toBeTruthy();
	});

	it('every control is at least 48 px and the form is well formed', async () => {
		const { store } = setup();
		void store.ensure();
		const dialog = await screen.findByRole('dialog');
		for (const button of screen.getAllByRole('button'))
			expect(button.className).toMatch(/h-12|size-12|h-14/);
		expect(screen.getByRole('link', { name: 'Read the full terms' }).className).toContain(
			'min-h-12'
		);
		expect(labelNameMismatches(dialog)).toEqual([]);
		expect(fieldProblems(dialog)).toEqual([]);
	});

	it('Accept without the box says so, sends nothing and focuses the box', async () => {
		const { store, api } = setup();
		void store.ensure();
		await fireEvent.click(await screen.findByRole('button', { name: 'Accept and continue' }));
		expect((await screen.findByRole('alert')).textContent).toContain('Tick the box');
		expect(api.accepts).toHaveLength(0);
		expect(document.activeElement).toBe(screen.getByRole('checkbox'));
	});

	it('ticking the box and accepting posts the version and the language and closes the gate', async () => {
		const { store, api } = setup();
		const intent = store.ensure();
		i18n.set('nl');
		await fireEvent.click(await screen.findByRole('checkbox'));
		await fireEvent.click(screen.getByRole('button', { name: 'Accepteren en doorgaan' }));
		expect(await intent).toBe(true);
		expect(api.accepts).toEqual([{ version: '2026-10', locale: 'nl', age13Confirmed: true }]);
		await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
	});

	it('Not now declines the intent', async () => {
		const { store, api } = setup();
		const intent = store.ensure();
		await fireEvent.click(await screen.findByRole('button', { name: 'Not now' }));
		expect(await intent).toBe(false);
		expect(api.accepts).toHaveLength(0);
		expect(screen.queryByRole('dialog')).toBeNull();
	});

	it('Escape declines too', async () => {
		const { store } = setup();
		const intent = store.ensure();
		await screen.findByRole('dialog');
		await fireEvent.keyDown(window, { key: 'Escape' });
		expect(await intent).toBe(false);
	});

	it('offline: says so, keeps the gate and the ticked box, and the same button retries', async () => {
		const { store, api } = setup();
		const intent = store.ensure();
		api.acceptAnswers = [apiError(0, 'network'), TERMS_OK];
		await fireEvent.click(await screen.findByRole('checkbox'));
		await fireEvent.click(screen.getByRole('button', { name: 'Accept and continue' }));
		const alert = await screen.findByRole('alert');
		expect(alert.textContent).toContain("Can't reach the server");
		expect((screen.getByRole('checkbox') as HTMLInputElement).checked).toBe(true);
		await fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
		expect(await intent).toBe(true);
		expect(api.accepts).toHaveLength(2);
	});

	it('a new version while open: tells the player, resets the box and shows the new number', async () => {
		const { store, api } = setup();
		const intent = store.ensure();
		api.acceptAnswers = [apiError(409, 'terms_version_mismatch'), TERMS_OK];
		await fireEvent.click(await screen.findByRole('checkbox'));
		await fireEvent.click(screen.getByRole('button', { name: 'Accept and continue' }));
		expect(
			await screen.findByText('The terms were updated. Read them and accept again.')
		).toBeTruthy();
		expect(screen.getByText('Version 2026-11')).toBeTruthy();
		expect((screen.getByRole('checkbox') as HTMLInputElement).checked).toBe(false);
		await fireEvent.click(screen.getByRole('checkbox'));
		await fireEvent.click(screen.getByRole('button', { name: 'Accept and continue' }));
		expect(await intent).toBe(true);
		expect(api.accepts.map((a) => a.version)).toEqual(['2026-10', '2026-11']);
	});

	it('shows no Spanish in English and no basta anywhere', async () => {
		const { store } = setup();
		void store.ensure();
		const dialog = await screen.findByRole('dialog');
		expect(dialog.textContent).not.toMatch(/[¡¿ñáéíóúü]|basta/i);
	});

	it.each([
		['es', 'Antes de jugar', 'Tengo 13 años o más y acepto los términos'],
		['nl', 'Voordat je speelt', 'Ik ben 13 jaar of ouder en accepteer de voorwaarden']
	] as const)('speaks %s', async (locale, title, label) => {
		i18n.set(locale);
		const { store } = setup();
		void store.ensure();
		await screen.findByRole('dialog', { name: title });
		expect(screen.getByRole('checkbox', { name: label })).toBeTruthy();
		expect(screen.getByRole('link').getAttribute('href')).toBe(`/terms#${locale}`);
	});
});
