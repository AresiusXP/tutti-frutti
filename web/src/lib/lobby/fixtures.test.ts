import { existsSync, readFileSync } from 'node:fs';
import { describe, expect, it, vi } from 'vitest';
import { FIXTURE_CATEGORIES, fixtureFetch, labelsFor, listingFor } from './fixtures';
import { LANGS } from './letters';

const server = new URL('../../../../server/internal/catalog/categories.json', import.meta.url);

describe('catalog fixture', () => {
	it.skipIf(!existsSync(server))('is a copy of the server catalog', () => {
		expect(FIXTURE_CATEGORIES).toEqual(JSON.parse(readFileSync(server, 'utf8')));
	});

	it('has 80 categories, 6 defaults and the 9 groups of the design', () => {
		expect(FIXTURE_CATEGORIES).toHaveLength(80);
		expect(FIXTURE_CATEGORIES.filter((c) => c.default)).toHaveLength(6);
		expect(listingFor('es').groups).toEqual([
			'personas',
			'lugares',
			'naturaleza',
			'comida',
			'cosas',
			'marcas',
			'cultura',
			'trabajo',
			'divertidas'
		]);
	});

	it('has a label in every lobby language for every category', () => {
		for (const category of FIXTURE_CATEGORIES) {
			for (const lang of LANGS) {
				expect(category[lang], `${category.id}.${lang}`).toMatch(/\S/);
			}
		}
		// every category has its own label in each language
		for (const lang of LANGS) {
			const labels = FIXTURE_CATEGORIES.map((c) => c[lang].toLowerCase());
			expect(new Set(labels).size, lang).toBe(labels.length);
		}
	});

	it('serves the Dutch catalog', () => {
		const nl = listingFor('nl');
		expect(nl.lang).toBe('nl');
		expect(nl.categories).toHaveLength(80);
		expect(nl.categories[0]).toEqual({
			id: 'nombre',
			group: 'personas',
			label: 'Naam',
			default: true
		});
		expect(nl.categories.find((c) => c.id === 'adjetivo')?.label).toBe('Bijvoeglijk naamwoord');
		expect(nl.categories.find((c) => c.id === 'lugar_turistico')?.label).toBe(
			'Toeristische attractie'
		);
		expect(labelsFor('nl', [{ id: 'pais' }, { custom: 'IJsberen' }])).toEqual(['Land', 'IJsberen']);
	});

	it('serves labels in the requested language', () => {
		expect(listingFor('es').categories[0]).toEqual({
			id: 'nombre',
			group: 'personas',
			label: 'Nombre',
			default: true
		});
		expect(listingFor('en').categories[0].label).toBe('Name');
		expect(listingFor('en').lang).toBe('en');
	});

	it('labels refs like the server (customs verbatim)', () => {
		expect(labelsFor('en', [{ id: 'pais' }, { custom: 'Excusas' }, { id: 'nope' }, {}])).toEqual([
			'Country',
			'Excusas',
			'nope',
			''
		]);
	});
});

describe('fixtureFetch', () => {
	it('answers the catalog endpoint and counts calls', async () => {
		const fetcher = fixtureFetch();
		const res = await fetcher('/api/categories?lang=en');
		expect(res.ok).toBe(true);
		expect(((await res.json()) as { lang: string }).lang).toBe('en');
		expect(fetcher.calls()).toBe(1);
		const dutch = await fetcher('/api/categories?lang=nl');
		expect(((await dutch.json()) as { lang: string }).lang).toBe('nl');
		expect((await fetcher('/api/categories?lang=fr')).status).toBe(404);
		expect((await fetcher('/api/other?lang=es')).status).toBe(404);
	});

	it('can fail the first requests, delay and hang', async () => {
		const failing = fixtureFetch({ failFirst: 1 });
		expect((await failing('/api/categories?lang=es')).status).toBe(503);
		expect((await failing('/api/categories?lang=es')).status).toBe(200);

		vi.useFakeTimers();
		const slow = fixtureFetch({ delayMs: 500 });
		const pending = slow('/api/categories?lang=es');
		await vi.advanceTimersByTimeAsync(500);
		expect((await pending).ok).toBe(true);
		vi.useRealTimers();

		const controller = new AbortController();
		const hanging = fixtureFetch({ hang: true })('/api/categories?lang=es', {
			signal: controller.signal
		});
		controller.abort();
		await expect(hanging).rejects.toThrow('aborted');
	});
});
