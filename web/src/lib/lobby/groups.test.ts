import { describe, expect, it } from 'vitest';
import { i18n } from '$lib/i18n.svelte';
import { listingFor } from './fixtures';
import { groupTitle } from './groups';

describe('groupTitle', () => {
	it('has a title for every catalog group, in the UI language', () => {
		for (const locale of ['es', 'en']) {
			i18n.set(locale);
			for (const id of listingFor('es').groups) {
				expect(groupTitle(id), `${locale}.${id}`).not.toBe(id);
			}
		}
		i18n.set('es');
		expect(groupTitle('personas')).toBe('Personas');
		i18n.set('en');
		expect(groupTitle('personas')).toBe('People');
		i18n.set('es');
	});

	it('shows the id of a group it does not know', () => {
		expect(groupTitle('nueva')).toBe('nueva');
	});
});
