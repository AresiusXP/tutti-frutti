// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { makeSettings } from '$lib/game/fixtures';
import { i18n } from '$lib/i18n.svelte';
import type { CategoryRef, Settings } from '$lib/protocol';
import { catalog } from './catalog.svelte';
import CategoryPicker from './CategoryPicker.svelte';
import { fixtureFetch, labelsFor } from './fixtures';
import { RECENTS_KEY } from './recents';

const ids = (...list: string[]): CategoryRef[] => list.map((id) => ({ id }));
const settingsWith = (categories: CategoryRef[], over: Partial<Settings> = {}) =>
	makeSettings({ categories, ...over });

/** Renders the picker and keeps `settings` in step with what it asks for (like the server would). */
let lastOnchange: ReturnType<typeof vi.fn<(next: Settings) => void>> | undefined;

function mount(settings: Settings = makeSettings(), rng?: () => number) {
	const onchange = vi.fn<(next: Settings) => void>();
	const view = render(CategoryPicker, {
		settings,
		labels: labelsFor(settings.lang, settings.categories),
		onchange: (next: Settings) => {
			onchange(next);
			void view.rerender({ settings: next, labels: labelsFor(next.lang, next.categories), rng });
		},
		rng
	});
	lastOnchange = onchange;
	return { ...view, onchange };
}

const last = (fn: { mock: { calls: unknown[][] } }) => fn.mock.calls.at(-1)![0] as Settings;
const idList = (s: Settings) => s.categories.map((c) => c.id ?? c.custom);

async function expandGroup(name: RegExp) {
	const header = await screen.findByRole('button', { name });
	if (header.getAttribute('aria-expanded') !== 'true') await fireEvent.click(header);
	return header;
}

describe('CategoryPicker', () => {
	beforeEach(() => {
		i18n.set('es');
		localStorage.clear();
		catalog.reset(fixtureFetch());
	});

	describe('selection', () => {
		it('lists the selection in game order with the counter and the rules', () => {
			mount();
			const items = within(screen.getAllByRole('list')[0]).getAllByRole('listitem');
			expect(items).toHaveLength(6);
			expect(items[0].textContent).toContain('Nombre');
			expect(items[5].textContent).toContain('Marca');
			expect(screen.getByText('6/10 seleccionadas')).toBeTruthy();
			expect(screen.getByText(/Elegí entre 3 y 10/)).toBeTruthy();
			expect(screen.getByRole('button', { name: 'Subir Nombre' }).hasAttribute('disabled')).toBe(
				true
			);
			expect(screen.getByRole('button', { name: 'Bajar Marca' }).hasAttribute('disabled')).toBe(
				true
			);
		});

		it('moves a category up and down by sending the whole new order', async () => {
			const { onchange } = mount();
			await fireEvent.click(screen.getByRole('button', { name: 'Subir País' }));
			expect(last(onchange)).toEqual(
				settingsWith(ids('pais', 'nombre', 'animal', 'comida', 'color', 'marca'))
			);
			await fireEvent.click(screen.getByRole('button', { name: 'Bajar Nombre' }));
			expect(idList(last(onchange))).toEqual([
				'pais',
				'animal',
				'nombre',
				'comida',
				'color',
				'marca'
			]);
		});

		it('announces the new position', async () => {
			mount();
			await fireEvent.click(screen.getByRole('button', { name: 'Bajar Nombre' }));
			await waitFor(() => expect(screen.getByText('Nombre: posición 2 de 6')).toBeTruthy());
		});

		it('removes a category', async () => {
			const { onchange } = mount();
			await fireEvent.click(screen.getByRole('button', { name: 'Quitar Color' }));
			expect(idList(last(onchange))).toEqual(['nombre', 'pais', 'animal', 'comida', 'marca']);
			expect(screen.getByText('5/10 seleccionadas')).toBeTruthy();
		});

		it('stops removing at 3 and explains why', () => {
			mount(settingsWith(ids('nombre', 'pais', 'animal')));
			expect(screen.getByText('3/10 seleccionadas')).toBeTruthy();
			expect(screen.getByText('Mínimo 3: no se pueden sacar más.')).toBeTruthy();
			for (const name of ['Quitar Nombre', 'Quitar País', 'Quitar Animal']) {
				expect(screen.getByRole('button', { name }).hasAttribute('disabled')).toBe(true);
			}
		});

		it('stops adding at 10 and explains why', async () => {
			const ten = ids(
				'nombre',
				'pais',
				'animal',
				'comida',
				'color',
				'marca',
				'ciudad',
				'capital',
				'ave',
				'postre'
			);
			mount(settingsWith(ten));
			expect(screen.getByText('10/10 seleccionadas')).toBeTruthy();
			expect(screen.getByText('Máximo 10: sacá una para sumar otra.')).toBeTruthy();
			await expandGroup(/^Lugares/);
			const chip = await screen.findByRole('button', { name: 'Provincia o estado' });
			expect(chip.hasAttribute('disabled')).toBe(true);
			// a selected chip can still be removed
			expect(
				screen.getByRole('button', { name: /Ciudad/, pressed: true }).hasAttribute('disabled')
			).toBe(false);
			await fireEvent.click(screen.getByRole('button', { name: 'Agregar categoría propia' }));
			expect(screen.getByText('Ya tenés 10 categorías: sacá una para agregar otra.')).toBeTruthy();
			expect(screen.getByRole('button', { name: 'Agregar' }).hasAttribute('disabled')).toBe(true);
		});

		it('marks custom categories with a pencil', () => {
			mount(settingsWith([...ids('nombre', 'pais'), { custom: 'Algo que da risa' }]));
			expect(screen.getAllByRole('img', { name: 'Categoría propia' })).toHaveLength(1);
			expect(screen.getByText('Algo que da risa')).toBeTruthy();
		});
	});

	describe('catalog chips', () => {
		it('shows the 9 groups collapsed, with how many are picked', async () => {
			mount();
			const personas = await screen.findByRole('button', { name: /^Personas/ });
			expect(personas.getAttribute('aria-expanded')).toBe('false');
			expect(within(personas).getByText('1/6')).toBeTruthy();
			expect(screen.getAllByRole('button', { name: /\d+\/\d+$/ })).toHaveLength(9);
			expect(screen.queryByRole('button', { name: 'Apellido' })).toBeNull();
		});

		it('expands a group and toggles chips', async () => {
			const { onchange } = mount();
			await expandGroup(/^Personas/);
			const nombre = screen.getByRole('button', { name: /Nombre/, pressed: true });
			expect(nombre).toBeTruthy();
			await fireEvent.click(screen.getByRole('button', { name: 'Apellido' }));
			expect(idList(last(onchange))).toEqual([
				'nombre',
				'pais',
				'animal',
				'comida',
				'color',
				'marca',
				'apellido'
			]);
			await fireEvent.click(screen.getByRole('button', { name: /Apellido/, pressed: true }));
			expect(idList(last(onchange))).toEqual([
				'nombre',
				'pais',
				'animal',
				'comida',
				'color',
				'marca'
			]);
		});

		it('collapses a group again', async () => {
			mount();
			const header = await expandGroup(/^Personas/);
			expect(header.getAttribute('aria-expanded')).toBe('true');
			await fireEvent.click(header);
			expect(header.getAttribute('aria-expanded')).toBe('false');
		});

		it('searches labels ignoring case and accents and opens the matching groups', async () => {
			mount();
			await screen.findByRole('button', { name: /^Personas/ });
			await fireEvent.input(screen.getByLabelText('Buscar categoría'), {
				target: { value: 'RIO' }
			});
			expect(screen.getByRole('button', { name: 'Río, lago o mar' })).toBeTruthy();
			expect(screen.queryByRole('button', { name: /^Personas/ })).toBeNull();
			expect(screen.getByRole('button', { name: /^Lugares/ }).getAttribute('aria-expanded')).toBe(
				'true'
			);
		});

		it('offers to add the search as a custom category when nothing matches', async () => {
			mount();
			await screen.findByRole('button', { name: /^Personas/ });
			await fireEvent.input(screen.getByLabelText('Buscar categoría'), {
				target: { value: '  zzz  ' }
			});
			expect(screen.getByText('Ninguna categoría coincide con “zzz”')).toBeTruthy();
			await fireEvent.click(
				screen.getByRole('button', { name: 'Agregarla como categoría propia' })
			);
			await waitFor(() =>
				expect((screen.getByLabelText('Nombre de la categoría') as HTMLInputElement).value).toBe(
					'zzz'
				)
			);
		});

		it('labels the chips in the lobby language, whatever the UI language', async () => {
			mount(settingsWith(ids('nombre', 'pais', 'animal'), { lang: 'en' }));
			await expandGroup(/^Personas/);
			expect(screen.getByRole('button', { name: 'Last name' })).toBeTruthy();
		});

		it('refetches when the lobby language changes', async () => {
			const fetcher = fixtureFetch();
			catalog.reset(fetcher);
			const view = render(CategoryPicker, {
				settings: makeSettings(),
				labels: [],
				onchange: () => {}
			});
			await expandGroup(/^Personas/);
			await waitFor(() => expect(catalog.listing('en')).not.toBeNull());
			expect(fetcher.calls()).toBe(2);
			await view.rerender({ settings: makeSettings({ lang: 'en' }), labels: [] });
			await expandGroup(/^Personas/);
			await waitFor(() => expect(screen.getByRole('button', { name: 'Last name' })).toBeTruthy());
			expect(fetcher.calls()).toBe(2);
		});
	});

	describe('catalog loading', () => {
		it('shows a skeleton and disables the presets while loading', () => {
			catalog.reset(fixtureFetch({ hang: true }));
			mount();
			expect(screen.getByText('Cargando las categorías…')).toBeTruthy();
			expect(screen.getByRole('button', { name: 'Clásicas' }).hasAttribute('disabled')).toBe(true);
			expect(screen.getByRole('button', { name: /Sorprendeme/ }).hasAttribute('disabled')).toBe(
				true
			);
			// the selection can still be edited
			expect(screen.getByRole('button', { name: 'Quitar Color' }).hasAttribute('disabled')).toBe(
				false
			);
		});

		it('shows an error with Retry and recovers', async () => {
			catalog.reset(fixtureFetch({ failFirst: 2 }));
			mount();
			const alert = await screen.findByText('No se pudieron cargar las categorías.');
			expect(alert).toBeTruthy();
			await fireEvent.click(screen.getByRole('button', { name: 'Reintentar' }));
			expect(await screen.findByRole('button', { name: /^Personas/ })).toBeTruthy();
			expect(screen.queryByText('No se pudieron cargar las categorías.')).toBeNull();
			expect(screen.getByRole('button', { name: 'Clásicas' }).hasAttribute('disabled')).toBe(false);
		});
	});

	describe('presets', () => {
		it('Clásicas replaces the selection with the six defaults', async () => {
			const { onchange } = mount(settingsWith(ids('ciudad', 'capital', 'ave'), { rounds: 7 }));
			const button = await screen.findByRole('button', { name: 'Clásicas' });
			await waitFor(() => expect(button.hasAttribute('disabled')).toBe(false));
			await fireEvent.click(button);
			expect(last(onchange)).toEqual(
				settingsWith(ids('nombre', 'pais', 'animal', 'comida', 'color', 'marca'), { rounds: 7 })
			);
		});

		it('Sorprendeme picks as many as are selected now, at random', async () => {
			const { onchange } = mount(
				settingsWith(
					ids('nombre', 'pais', 'animal', 'comida', 'color', 'marca', 'ciudad', 'capital')
				),
				() => 0
			);
			const button = await screen.findByRole('button', { name: /Sorprendeme/ });
			await waitFor(() => expect(button.hasAttribute('disabled')).toBe(false));
			await fireEvent.click(button);
			expect(last(onchange).categories).toHaveLength(8);
			expect(new Set(idList(last(onchange))).size).toBe(8);
		});
	});

	describe('custom categories', () => {
		async function openForm(settings?: Settings) {
			mount(settings);
			await screen.findByRole('button', { name: /^Personas/ });
			await waitFor(() => expect(catalog.listing('en')).not.toBeNull());
			await fireEvent.click(screen.getByRole('button', { name: 'Agregar categoría propia' }));
			return screen.getByLabelText('Nombre de la categoría') as HTMLInputElement;
		}
		const type = (input: HTMLInputElement, value: string) =>
			fireEvent.input(input, { target: { value } });
		const add = () => screen.getByRole('button', { name: 'Agregar' });

		it('shows the length counter and keeps Add disabled while empty', async () => {
			const input = await openForm();
			expect(add().hasAttribute('disabled')).toBe(true);
			expect(screen.getByText('0/24')).toBeTruthy();
			expect(input.getAttribute('maxlength')).toBe('24');
			await type(input, 'Algo');
			expect(screen.getByText('4/24')).toBeTruthy();
			expect(add().hasAttribute('disabled')).toBe(false);
		});

		it('rejects a built-in label ignoring case and accents', async () => {
			const input = await openForm();
			await type(input, 'PAIS');
			expect(screen.getByText('Ya existe: País')).toBeTruthy();
			expect(add().hasAttribute('disabled')).toBe(true);
			expect(input.getAttribute('aria-invalid')).toBe('true');
			await type(input, 'Algo nuevo');
			expect(screen.queryByText(/Ya existe/)).toBeNull();
			expect(input.getAttribute('aria-invalid')).toBe('false');
		});

		it('rejects the built-in label of the other language, naming the one in the lobby language', async () => {
			const input = await openForm();
			await type(input, 'country');
			expect(screen.getByText('Ya existe: País')).toBeTruthy();
			await type(input, 'Fruit or vegetable');
			expect(screen.getByText('Ya existe: Fruta o verdura')).toBeTruthy();
		});

		it('rejects a duplicate of another custom category', async () => {
			const input = await openForm(
				settingsWith([...ids('nombre', 'pais', 'animal'), { custom: 'Algo que da risa' }])
			);
			await type(input, '  algo QUE da   risa ');
			expect(screen.getByText('Ya existe: Algo que da risa')).toBeTruthy();
			expect(add().hasAttribute('disabled')).toBe(true);
		});

		it('adds a valid custom category, remembers it and clears the field', async () => {
			const input = await openForm();
			const onchange = lastOnchange!;
			await type(input, '  Algo   que da risa ');
			await fireEvent.click(add());
			expect(last(onchange).categories.at(-1)).toEqual({ custom: 'Algo que da risa' });
			expect(JSON.parse(localStorage.getItem(RECENTS_KEY)!)).toEqual(['Algo que da risa']);
			expect(input.value).toBe('');
			expect(screen.getByText('7/10 seleccionadas')).toBeTruthy();
			// it is selected now, so it is not offered again as a recent
			expect(screen.queryByRole('button', { name: 'Agregar Algo que da risa' })).toBeNull();
		});

		it('submits with Enter', async () => {
			const input = await openForm();
			const onchange = lastOnchange!;
			await type(input, 'Excusas');
			await fireEvent.submit(input.closest('form')!);
			expect(last(onchange).categories.at(-1)).toEqual({ custom: 'Excusas' });
		});

		it('ignores a submit while invalid', async () => {
			const input = await openForm();
			const onchange = lastOnchange!;
			await type(input, 'país');
			await fireEvent.submit(input.closest('form')!);
			expect(onchange).not.toHaveBeenCalled();
		});
	});

	describe('recent custom categories', () => {
		beforeEach(() => {
			localStorage.setItem(RECENTS_KEY, JSON.stringify(['Excusas', 'Ciudad', 'Algo raro']));
		});

		it('offers them as chips and re-adds one with a tap', async () => {
			const { onchange } = mount();
			await fireEvent.click(await screen.findByRole('button', { name: 'Agregar Excusas' }));
			expect(last(onchange).categories.at(-1)).toEqual({ custom: 'Excusas' });
			expect(JSON.parse(localStorage.getItem(RECENTS_KEY)!)[0]).toBe('Excusas');
			expect(screen.queryByRole('button', { name: 'Agregar Excusas' })).toBeNull();
		});

		it('refuses one that now clashes with a built-in label', async () => {
			const { onchange } = mount();
			await waitFor(() => expect(catalog.listing('es')).not.toBeNull());
			await fireEvent.click(screen.getByRole('button', { name: 'Agregar Ciudad' }));
			expect(onchange).not.toHaveBeenCalled();
			expect(screen.getByText('Ya existe: Ciudad')).toBeTruthy();
		});

		it('forgets one with its ✕ control', async () => {
			mount();
			await fireEvent.click(await screen.findByRole('button', { name: 'Olvidar Algo raro' }));
			expect(screen.queryByRole('button', { name: 'Agregar Algo raro' })).toBeNull();
			expect(JSON.parse(localStorage.getItem(RECENTS_KEY)!)).toEqual(['Excusas', 'Ciudad']);
		});

		it('cannot add one when the list is full', async () => {
			mount(
				settingsWith(
					ids(
						'nombre',
						'pais',
						'animal',
						'comida',
						'color',
						'marca',
						'ciudad',
						'capital',
						'ave',
						'postre'
					)
				)
			);
			expect(
				(await screen.findByRole('button', { name: 'Agregar Excusas' })).hasAttribute('disabled')
			).toBe(true);
		});
	});

	it('never fails when localStorage is blocked', async () => {
		const spy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
			throw new Error('blocked');
		});
		mount();
		expect(await screen.findByRole('button', { name: /^Personas/ })).toBeTruthy();
		spy.mockRestore();
	});
});
